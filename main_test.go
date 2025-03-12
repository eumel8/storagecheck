package main

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Utility to read counter value
func getCounterValue(c prometheus.Counter) float64 {
	metric := &dto.Metric{}
	if err := c.Write(metric); err != nil {
		return 0
	}
	return metric.GetCounter().GetValue()
}

func TestDoStorageCheckSuccess(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	storageClass := "test-storage"

	// Run doStorageCheck asynchronously and simulate pod success
	go func() {
		time.Sleep(500 * time.Millisecond) // Give pod time to be created

		pods, _ := clientset.CoreV1().Pods("default").List(context.TODO(), metav1.ListOptions{})
		if len(pods.Items) > 0 {
			p := pods.Items[0]
			p.Status.Phase = corev1.PodSucceeded
			_, _ = clientset.CoreV1().Pods("default").UpdateStatus(context.TODO(), &p, metav1.UpdateOptions{})
		}
	}()

	doStorageCheck(clientset, storageClass)

	success := getCounterValue(checkSuccess)
	fail := getCounterValue(checkFailure)

	if success == 0 {
		t.Errorf("Expected checkSuccess > 0, got %v", success)
	}
	if fail != 0 {
		t.Errorf("Expected checkFailure == 0, got %v", fail)
	}
}

