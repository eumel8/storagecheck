package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// TestLoggingMiddleware verifies that the middleware logs and passes along the response.
func TestLoggingMiddleware(t *testing.T) {
	// Dummy handler that simply writes "OK"
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	handler := LoggingMiddleware(dummyHandler)
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", rec.Code)
	}
	if rec.Body.String() != "OK" {
		t.Errorf("Expected response body 'OK', got '%s'", rec.Body.String())
	}
}

// TestCleanupPreviousChecks creates fake pods and PVCs, calls cleanupPreviousChecks, and ensures they are deleted.
func TestCleanupPreviousChecks(t *testing.T) {
	namespace := "test-namespace"
	clientset := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pod",
				Labels: map[string]string{
					"app": "storage-check",
				},
			},
		},
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-pvc",
				Labels: map[string]string{
					"app": "storage-check",
				},
			},
		},
	)

	cleanupPreviousChecks(clientset, namespace)

	// Verify pods have been deleted
	pods, err := clientset.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=storage-check",
	})
	if err != nil {
		t.Fatalf("Error listing pods: %v", err)
	}
	if len(pods.Items) != 0 {
		t.Errorf("Expected 0 pods after cleanup, got %d", len(pods.Items))
	}

	// Verify PVCs have been deleted
	pvcs, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=storage-check",
	})
	if err != nil {
		t.Fatalf("Error listing PVCs: %v", err)
	}
	if len(pvcs.Items) != 0 {
		t.Errorf("Expected 0 PVCs after cleanup, got %d", len(pvcs.Items))
	}
}

// TestDoStorageCheckSuccess fakes the pod status so that the storage check loop sees a Succeeded pod.
func TestDoStorageCheckSuccess(t *testing.T) {
	namespace := "test-namespace"
	storageClass := "local-path"
	image := "busybox"
	clientset := fake.NewSimpleClientset()

	// Prepend a reactor so that any "get" operation on pods returns a pod with a Succeeded status.
	clientset.PrependReactor("get", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		// Type-assert to GetAction to access the GetName method.
		getAction, ok := action.(ktesting.GetAction)
		if !ok {
			return false, nil, nil
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: getAction.GetName(),
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodSucceeded,
			},
		}
		return true, pod, nil
	})

	// Record initial value of the checkSuccess counter.
	var metricBefore = &dto.Metric{}
	if err := checkSuccess.Write(metricBefore); err != nil {
		t.Fatalf("Error writing metric: %v", err)
	}
	initialValue := metricBefore.GetCounter().GetValue()

	// Run doStorageCheck in a goroutine so that the test does not block indefinitely.
	done := make(chan struct{})
	go func() {
		doStorageCheck(clientset, storageClass, namespace, image)
		close(done)
	}()

	// Wait for the doStorageCheck routine to complete.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("doStorageCheck did not complete in time")
	}

	// Check that the checkSuccess counter has increased.
	var metricAfter = &dto.Metric{}
	if err := checkSuccess.Write(metricAfter); err != nil {
		t.Fatalf("Error writing metric: %v", err)
	}
	finalValue := metricAfter.GetCounter().GetValue()

	if finalValue <= initialValue {
		t.Errorf("Expected checkSuccess counter to increase, but it did not: initial=%f, final=%f", initialValue, finalValue)
	}
}
