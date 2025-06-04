package main

import (
        "context"
        "net/http"
        "net/http/httptest"
        "testing"
        "time"

        "github.com/prometheus/client_golang/prometheus"
        dto "github.com/prometheus/client_model/go"
        corev1 "k8s.io/api/core/v1"
        storagev1 "k8s.io/api/storage/v1"
        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
        "k8s.io/apimachinery/pkg/runtime"
        "k8s.io/client-go/kubernetes/fake"
        ktesting "k8s.io/client-go/testing"
)

func TestLoggingMiddleware(t *testing.T) {
        tests := []struct {
                name           string
                method         string
                path           string
                handlerStatus  int
                handlerBody    string
                expectedStatus int
                expectedBody   string
        }{
                {
                        name:           "GET request with OK response",
                        method:         "GET",
                        path:           "/test",
                        handlerStatus:  http.StatusOK,
                        handlerBody:    "OK",
                        expectedStatus: http.StatusOK,
                        expectedBody:   "OK",
                },
                {
                        name:           "POST request with created response",
                        method:         "POST",
                        path:           "/api/create",
                        handlerStatus:  http.StatusCreated,
                        handlerBody:    "Created",
                        expectedStatus: http.StatusCreated,
                        expectedBody:   "Created",
                },
                {
                        name:           "Error response handling",
                        method:         "GET",
                        path:           "/error",
                        handlerStatus:  http.StatusInternalServerError,
                        handlerBody:    "Internal Server Error",
                        expectedStatus: http.StatusInternalServerError,
                        expectedBody:   "Internal Server Error",
                },
        }

        for _, tt := range tests {
                t.Run(tt.name, func(t *testing.T) {
                        dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                                w.WriteHeader(tt.handlerStatus)
                                w.Write([]byte(tt.handlerBody))
                        })

                        handler := LoggingMiddleware(dummyHandler)
                        req := httptest.NewRequest(tt.method, tt.path, nil)
                        rec := httptest.NewRecorder()

                        handler.ServeHTTP(rec, req)

                        if rec.Code != tt.expectedStatus {
                                t.Errorf("Expected status code %d, got %d", tt.expectedStatus, rec.Code)
                        }
                        if rec.Body.String() != tt.expectedBody {
                                t.Errorf("Expected response body %q, got %q", tt.expectedBody, rec.Body.String())
                        }
                })
        }
}

func TestCleanupPreviousChecks(t *testing.T) {
        tests := []struct {
                name          string
                namespace     string
                pods          []corev1.Pod
                pvcs          []corev1.PersistentVolumeClaim
                expectedPods  int
                expectedPVCs  int
                expectCleanup bool
        }{
                {
                        name:      "cleanup single pod and pvc",
                        namespace: "test-namespace",
                        pods: []corev1.Pod{
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name:      "test-pod",
                                                Namespace: "test-namespace",
                                                Labels: map[string]string{
                                                        "app": "storage-check",
                                                },
                                        },
                                },
                        },
                        pvcs: []corev1.PersistentVolumeClaim{
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name:      "test-pvc",
                                                Namespace: "test-namespace",
                                                Labels: map[string]string{
                                                        "app": "storage-check",
                                                },
                                        },
                                },
                        },
                        expectedPods:  0,
                        expectedPVCs:  0,
                        expectCleanup: true,
                },
                {
                        name:          "no resources to cleanup",
                        namespace:     "empty-namespace",
                        pods:          []corev1.Pod{},
                        pvcs:          []corev1.PersistentVolumeClaim{},
                        expectedPods:  0,
                        expectedPVCs:  0,
                        expectCleanup: false,
                },
                {
                        name:      "multiple resources cleanup",
                        namespace: "multi-namespace",
                        pods: []corev1.Pod{
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name:      "test-pod-1",
                                                Namespace: "multi-namespace",
                                                Labels: map[string]string{
                                                        "app": "storage-check",
                                                },
                                        },
                                },
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name:      "test-pod-2",
                                                Namespace: "multi-namespace",
                                                Labels: map[string]string{
                                                        "app": "storage-check",
                                                },
                                        },
                                },
                        },
                        pvcs: []corev1.PersistentVolumeClaim{
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name:      "test-pvc-1",
                                                Namespace: "multi-namespace",
                                                Labels: map[string]string{
                                                        "app": "storage-check",
                                                },
                                        },
                                },
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name:      "test-pvc-2",
                                                Namespace: "multi-namespace",
                                                Labels: map[string]string{
                                                        "app": "storage-check",
                                                },
                                        },
                                },
                        },
                        expectedPods:  0,
                        expectedPVCs:  0,
                        expectCleanup: true,
                },
        }

        for _, tt := range tests {
                t.Run(tt.name, func(t *testing.T) {
                        objects := []runtime.Object{}
                        for i := range tt.pods {
                                objects = append(objects, &tt.pods[i])
                        }
                        for i := range tt.pvcs {
                                objects = append(objects, &tt.pvcs[i])
                        }

                        clientset := fake.NewSimpleClientset(objects...)

                        initialCleanupSuccess := getCounterValue(t, cleanupSuccess)
                        initialCleanupFailure := getCounterValue(t, cleanupFailure)

                        cleanupPreviousChecks(clientset, tt.namespace)

                        pods, err := clientset.CoreV1().Pods(tt.namespace).List(context.Background(), metav1.ListOptions{
                                LabelSelector: "app=storage-check",
                        })
                        if err != nil {
                                t.Fatalf("Error listing pods: %v", err)
                        }
                        if len(pods.Items) != tt.expectedPods {
                                t.Errorf("Expected %d pods after cleanup, got %d", tt.expectedPods, len(pods.Items))
                        }

                        pvcs, err := clientset.CoreV1().PersistentVolumeClaims(tt.namespace).List(context.Background(), metav1.ListOptions{
                                LabelSelector: "app=storage-check",
                        })
                        if err != nil {
                                t.Fatalf("Error listing PVCs: %v", err)
                        }
                        if len(pvcs.Items) != tt.expectedPVCs {
                                t.Errorf("Expected %d PVCs after cleanup, got %d", tt.expectedPVCs, len(pvcs.Items))
                        }

                        if tt.expectCleanup {
                                finalCleanupSuccess := getCounterValue(t, cleanupSuccess)
                                if finalCleanupSuccess <= initialCleanupSuccess {
                                        t.Errorf("Expected cleanup success counter to increase")
                                }
                        }

                        finalCleanupFailure := getCounterValue(t, cleanupFailure)
                        if finalCleanupFailure != initialCleanupFailure {
                                t.Errorf("Expected no cleanup failures, but counter increased")
                        }
                })
        }
}

func TestLookupStorageClass(t *testing.T) {
        tests := []struct {
                name           string
                storageClasses []storagev1.StorageClass
                expectedName   string
                expectError    bool
        }{
                {
                        name: "single non-retain class",
                        storageClasses: []storagev1.StorageClass{
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name: "fast-storage",
                                        },
                                        ReclaimPolicy: func() *corev1.PersistentVolumeReclaimPolicy {
                                                r := corev1.PersistentVolumeReclaimDelete
                                                return &r
                                        }(),
                                },
                        },
                        expectedName: "fast-storage",
                        expectError:  false,
                },
                {
                        name: "multiple classes with retain policy filtering",
                        storageClasses: []storagev1.StorageClass{
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name: "retain-storage",
                                        },
                                        ReclaimPolicy: func() *corev1.PersistentVolumeReclaimPolicy {
                                                r := corev1.PersistentVolumeReclaimRetain
                                                return &r
                                        }(),
                                },
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name: "delete-storage",
                                        },
                                        ReclaimPolicy: func() *corev1.PersistentVolumeReclaimPolicy {
                                                r := corev1.PersistentVolumeReclaimDelete
                                                return &r
                                        }(),
                                },
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name: "recycle-storage",
                                        },
                                        ReclaimPolicy: func() *corev1.PersistentVolumeReclaimPolicy {
                                                r := corev1.PersistentVolumeReclaimRecycle
                                                return &r
                                        }(),
                                },
                        },
                        expectedName: "delete-storage",
                        expectError:  false,
                },
                {
                        name:           "no storage classes",
                        storageClasses: []storagev1.StorageClass{},
                        expectedName:   "",
                        expectError:    false,
                },
                {
                        name: "all retain storage classes",
                        storageClasses: []storagev1.StorageClass{
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name: "retain-storage-1",
                                        },
                                        ReclaimPolicy: func() *corev1.PersistentVolumeReclaimPolicy {
                                                r := corev1.PersistentVolumeReclaimRetain
                                                return &r
                                        }(),
                                },
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name: "retain-storage-2",
                                        },
                                        ReclaimPolicy: func() *corev1.PersistentVolumeReclaimPolicy {
                                                r := corev1.PersistentVolumeReclaimRetain
                                                return &r
                                        }(),
                                },
                        },
                        expectedName: "",
                        expectError:  false,
                },
                {
                        name: "nil reclaim policy defaults to delete",
                        storageClasses: []storagev1.StorageClass{
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name: "default-storage",
                                        },
                                        ReclaimPolicy: nil,
                                },
                        },
                        expectedName: "",
                        expectError:  false,
                },
        }

        for _, tt := range tests {
                t.Run(tt.name, func(t *testing.T) {
                        client := fake.NewSimpleClientset()

                        for _, sc := range tt.storageClasses {
                                _, err := client.StorageV1().StorageClasses().Create(context.TODO(), &sc, metav1.CreateOptions{})
                                if err != nil {
                                        t.Fatalf("Failed to seed storage class: %v", err)
                                }
                        }

                        name, err := lookupStorageClass(client)
                        if tt.expectError && err == nil {
                                t.Errorf("Expected error but got none")
                        }
                        if !tt.expectError && err != nil {
                                t.Errorf("Unexpected error: %v", err)
                        }
                        if name != tt.expectedName {
                                t.Errorf("Expected name %q, got %q", tt.expectedName, name)
                        }
                })
        }
}

func TestDoStorageCheck(t *testing.T) {
        tests := []struct {
                name                 string
                namespace            string
                image                string
                storageClasses       []storagev1.StorageClass
                podPhase             corev1.PodPhase
                setupPodReactor      bool
                expectSuccess        bool
                expectError          bool
                expectCheckIncrement bool
        }{
                {
                        name:      "successful storage check",
                        namespace: "test-namespace",
                        image:     "busybox",
                        storageClasses: []storagev1.StorageClass{
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name: "fast-storage",
                                        },
                                        ReclaimPolicy: func() *corev1.PersistentVolumeReclaimPolicy {
                                                r := corev1.PersistentVolumeReclaimDelete
                                                return &r
                                        }(),
                                },
                        },
                        podPhase:             corev1.PodSucceeded,
                        setupPodReactor:      true,
                        expectSuccess:        true,
                        expectCheckIncrement: true,
                },
                {
                        name:      "failed storage check",
                        namespace: "test-namespace",
                        image:     "busybox",
                        storageClasses: []storagev1.StorageClass{
                                {
                                        ObjectMeta: metav1.ObjectMeta{
                                                Name: "fast-storage",
                                        },
                                        ReclaimPolicy: func() *corev1.PersistentVolumeReclaimPolicy {
                                                r := corev1.PersistentVolumeReclaimDelete
                                                return &r
                                        }(),
                                },
                        },
                        podPhase:             corev1.PodFailed,
                        setupPodReactor:      true,
                        expectSuccess:        false,
                        expectCheckIncrement: true,
                },
                {
                        name:                 "no suitable storage class",
                        namespace:            "test-namespace",
                        image:                "busybox",
                        storageClasses:       []storagev1.StorageClass{},
                        setupPodReactor:      false,
                        expectSuccess:        false,
                        expectCheckIncrement: true,
                },
        }

        for _, tt := range tests {
                t.Run(tt.name, func(t *testing.T) {
                        objects := []runtime.Object{}
                        for i := range tt.storageClasses {
                                objects = append(objects, &tt.storageClasses[i])
                        }

                        clientset := fake.NewSimpleClientset(objects...)

                        if tt.setupPodReactor {
                                clientset.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
                                        getAction, ok := action.(ktesting.GetAction)
                                        if !ok {
                                                return false, nil, nil
                                        }
                                        pod := &corev1.Pod{
                                                ObjectMeta: metav1.ObjectMeta{
                                                        Name:      getAction.GetName(),
                                                        Namespace: getAction.GetNamespace(),
                                                },
                                                Status: corev1.PodStatus{
                                                        Phase: tt.podPhase,
                                                },
                                        }
                                        return true, pod, nil
                                })
                        }

                        initialSuccess := getCounterValue(t, checkSuccess)
                        initialFailure := getCounterValue(t, checkFailure)

                        done := make(chan struct{})
                        go func() {
                                doStorageCheck(clientset, tt.namespace, tt.image)
                                close(done)
                        }()

                        select {
                        case <-done:
                        case <-time.After(5 * time.Second):
                                t.Fatal("doStorageCheck did not complete in time")
                        }

                        finalSuccess := getCounterValue(t, checkSuccess)
                        finalFailure := getCounterValue(t, checkFailure)

                        if tt.expectSuccess {
                                if finalSuccess <= initialSuccess {
                                        t.Errorf("Expected checkSuccess counter to increase, but it did not: initial=%f, final=%f", initialSuccess, finalSuccess)
                                }
                                if finalFailure != initialFailure {
                                        t.Errorf("Expected checkFailure counter to remain unchanged, but it increased: initial=%f, final=%f", initialFailure, finalFailure)
                                }
                        } else {
                                if finalFailure <= initialFailure {
                                        t.Errorf("Expected checkFailure counter to increase, but it did not: initial=%f, final=%f", initialFailure, finalFailure)
                                }
                                if finalSuccess != initialSuccess {
                                        t.Errorf("Expected checkSuccess counter to remain unchanged, but it increased: initial=%f, final=%f", initialSuccess, finalSuccess)
                                }
                        }
                })
        }
}

func TestPrometheusMetrics(t *testing.T) {
        tests := []struct {
                name   string
                metric prometheus.Counter
        }{
                {
                        name:   "checkSuccess metric exists",
                        metric: checkSuccess,
                },
                {
                        name:   "checkFailure metric exists",
                        metric: checkFailure,
                },
                {
                        name:   "cleanupSuccess metric exists",
                        metric: cleanupSuccess,
                },
                {
                        name:   "cleanupFailure metric exists",
                        metric: cleanupFailure,
                },
        }

        for _, tt := range tests {
                t.Run(tt.name, func(t *testing.T) {
                        if tt.metric == nil {
                                t.Errorf("Metric %s is nil", tt.name)
                        }

                        var metricDTO = &dto.Metric{}
                        if err := tt.metric.Write(metricDTO); err != nil {
                                t.Errorf("Failed to write metric %s: %v", tt.name, err)
                        }

                        if metricDTO.GetCounter() == nil {
                                t.Errorf("Metric %s does not have a counter", tt.name)
                        }
                })
        }
}

func TestCheckDurationHistogram(t *testing.T) {
        if checkDuration == nil {
                t.Fatal("checkDuration histogram is nil")
        }

        var metricDTO = &dto.Metric{}
        if err := checkDuration.Write(metricDTO); err != nil {
                t.Fatalf("Failed to write checkDuration metric: %v", err)
        }

        if metricDTO.GetHistogram() == nil {
                t.Error("checkDuration does not have a histogram")
        }
}

func getCounterValue(t *testing.T, counter prometheus.Counter) float64 {
        var metricDTO = &dto.Metric{}
        if err := counter.Write(metricDTO); err != nil {
                t.Fatalf("Error writing metric: %v", err)
        }
        return metricDTO.GetCounter().GetValue()
}
