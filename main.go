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
	cleanupSuccess = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "storage_check_cleanup_success_total",
			Help: "Total number of successful cleanups of previous checks",
		},
	)
	cleanupFailure = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "storage_check_cleanup_failure_total",
			Help: "Total number of failed cleanups of previous checks",
		},
	)
)

func init() {
	prometheus.MustRegister(checkSuccess, checkFailure, checkDuration, cleanupSuccess, cleanupFailure)
}

func main() {
	storageClass := os.Getenv("STORAGE_CLASS")
	intervalStr := os.Getenv("CHECK_INTERVAL")
	namespace := os.Getenv("NAMESPACE")
	image := os.Getenv("CHECK_IMAGE")
	if image == "" {
		image = "mtr.devops.telekom.de/mcsps/busybox:main"
	}
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
		http.ListenAndServe(":8080", nil)
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
		// Clean up any existing resources from previous checks before proceeding
		cleanupPreviousChecks(clientset, namespace)
		doStorageCheck(clientset, storageClass, namespace, image)
		<-ticker.C
	}
}

func cleanupPreviousChecks(clientset kubernetes.Interface, namespace string) {
	ctx := context.Background()
	//namespace := namespace

	// Find and delete pods from previous checks
	podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=storage-check",
	})

	if err == nil && len(podList.Items) > 0 {
		for _, pod := range podList.Items {
			err := clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
			if err != nil {
				cleanupFailure.Inc()
			} else {
				cleanupSuccess.Inc()
			}
		}
	}

	// Find and delete PVCs from previous checks
	pvcList, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=storage-check",
	})

	if err == nil && len(pvcList.Items) > 0 {
		for _, pvc := range pvcList.Items {
			err := clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{})
			if err != nil {
				cleanupFailure.Inc()
			} else {
				cleanupSuccess.Inc()
			}
		}
	}
}

func doStorageCheck(clientset kubernetes.Interface, storageClass string, namespace string, image string) {

	var user = int64(1000)
	var privledged = bool(true)
	var readonly = bool(true)
	var nonroot = bool(false)

	start := time.Now()
	ctx := context.Background()

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "storage-check-pvc-",
			Labels: map[string]string{
				"app": "storage-check",
			},
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

	createdPVC, err := clientset.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		checkFailure.Inc()
		return
	}
	defer clientset.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, createdPVC.Name, metav1.DeleteOptions{})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "storage-check-pod-",
			Labels: map[string]string{
				"app": "storage-check",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "checker",
					Image:   image,
					Command: []string{"sh", "-c", "echo hello > /mnt/testfile && cat /mnt/testfile"},

					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: &privledged,
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL",
								"CAP_NET_RAW"},
						},
						Privileged:             &privledged,
						ReadOnlyRootFilesystem: &readonly,
						RunAsGroup:             &user,
						RunAsUser:              &user,
						RunAsNonRoot:           &nonroot,
					},

					VolumeMounts: []corev1.VolumeMount{
						{
							MountPath: "/mnt",
							Name:      "testvol",
						},
					},
				},
			},
			SecurityContext: &corev1.PodSecurityContext{
				FSGroup:            &user,
				RunAsGroup:         &user,
				RunAsUser:          &user,
				RunAsNonRoot:       &nonroot,
				SupplementalGroups: []int64{1000},
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
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

	createdPod, err := clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		checkFailure.Inc()
		return
	}
	defer clientset.CoreV1().Pods(namespace).Delete(ctx, createdPod.Name, metav1.DeleteOptions{})

	// Wait for pod to complete
	for {
		p, _ := clientset.CoreV1().Pods(namespace).Get(ctx, createdPod.Name, metav1.GetOptions{})
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
