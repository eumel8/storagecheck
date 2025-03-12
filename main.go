package main

import (
        "context"
        "net/http"
        "os"
        "strconv"
        "time"

        "github.com/prometheus/client_golang/prometheus"
        "github.com/prometheus/client_golang/prometheus/promhttp"
        corev1 "k8s.io/api/core/v1"
        "k8s.io/apimachinery/pkg/api/resource"
        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
        "k8s.io/client-go/kubernetes"
        "k8s.io/client-go/rest"
)

// Metrics
var (
        checkSuccess = prometheus.NewCounter(
                prometheus.CounterOpts{
                        Name: "storage_check_success_total",
                        Help: "Total number of successful storage checks",
                },
        )
        checkFailure = prometheus.NewCounter(
                prometheus.CounterOpts{
                        Name: "storage_check_failure_total",
                        Help: "Total number of failed storage checks",
                },
        )
        checkDuration = prometheus.NewHistogram(
                prometheus.HistogramOpts{
                        Name:    "storage_check_duration_seconds",
                        Help:    "Duration of storage checks in seconds",
                        Buckets: prometheus.DefBuckets,
                },
        )
)

func init() {
        prometheus.MustRegister(checkSuccess, checkFailure, checkDuration)
}

func main() {
        storageClass := os.Getenv("STORAGE_CLASS")
        intervalStr := os.Getenv("CHECK_INTERVAL")
        if storageClass == "" {
                storageClass = "local-path"
        }
        interval, err := strconv.Atoi(intervalStr)
        if err != nil || interval <= 0 {
                interval = 3600 // default: 1 hour
        }

        // Prometheus endpoint
        go func() {
                http.Handle("/metrics", promhttp.Handler())
                http.ListenAndServe(":2112", nil)
        }()

        // Kubernetes client
        config, err := rest.InClusterConfig()
        if err != nil {
                panic(err.Error())
        }
        clientset, err := kubernetes.NewForConfig(config)
        if err != nil {
                panic(err.Error())
        }

        ticker := time.NewTicker(time.Duration(interval) * time.Second)
        for {
                doStorageCheck(clientset, storageClass)
                <-ticker.C
        }
}

func doStorageCheck(clientset kubernetes.Interface, storageClass string) {
        start := time.Now()
        ctx := context.Background()

        pvc := &corev1.PersistentVolumeClaim{
                ObjectMeta: metav1.ObjectMeta{
                        GenerateName: "storage-check-pvc-",
                },
                Spec: corev1.PersistentVolumeClaimSpec{
                        AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
                        Resources: corev1.VolumeResourceRequirements{
                                Requests: corev1.ResourceList{
                                        "storage": resource.MustParse("1Gi"),
                                },
                        },
                        StorageClassName: &storageClass,
                },
        }

        createdPVC, err := clientset.CoreV1().PersistentVolumeClaims("default").Create(ctx, pvc, metav1.CreateOptions{})
        if err != nil {
                checkFailure.Inc()
                return
        }
        defer clientset.CoreV1().PersistentVolumeClaims("default").Delete(ctx, createdPVC.Name, metav1.DeleteOptions{})

        pod := &corev1.Pod{
                ObjectMeta: metav1.ObjectMeta{
                        GenerateName: "storage-check-pod-",
                },
                Spec: corev1.PodSpec{
                        RestartPolicy: corev1.RestartPolicyNever,
                        Containers: []corev1.Container{
                                {
                                        Name:  "checker",
                                        Image: "busybox",
                                        Command: []string{"sh", "-c", "echo hello > /mnt/testfile && cat /mnt/testfile"},
                                        VolumeMounts: []corev1.VolumeMount{
                                                {
                                                        MountPath: "/mnt",
                                                        Name:      "testvol",
                                                },
                                        },
                                },
                        },
                        Volumes: []corev1.Volume{
                                {
                                        Name: "testvol",
                                        VolumeSource: corev1.VolumeSource{
                                                PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
                                                        ClaimName: createdPVC.Name,
                                                },
                                        },
                                },
                        },
                },
        }

        createdPod, err := clientset.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{})
        if err != nil {
                checkFailure.Inc()
                return
        }
        defer clientset.CoreV1().Pods("default").Delete(ctx, createdPod.Name, metav1.DeleteOptions{})

        // Wait for pod to complete
        for {
                p, _ := clientset.CoreV1().Pods("default").Get(ctx, createdPod.Name, metav1.GetOptions{})
                if p.Status.Phase == corev1.PodSucceeded {
                        checkSuccess.Inc()
                        checkDuration.Observe(time.Since(start).Seconds())
                        return
                } else if p.Status.Phase == corev1.PodFailed {
                        checkFailure.Inc()
                        return
                }
                time.Sleep(2 * time.Second)
        }
}
