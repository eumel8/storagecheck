package main

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	log "github.com/gookit/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	port        = "8080"
	logTemplate = "[{{datetime}}] [{{level}}] {{caller}} {{message}} \n"
	timeout     = 10 * time.Second
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
	logLevel := os.Getenv("LOG_LEVEL")
	storageClass := os.Getenv("STORAGE_CLASS")
	intervalStr := os.Getenv("CHECK_INTERVAL")
	namespace := os.Getenv("NAMESPACE")
	image := os.Getenv("CHECK_IMAGE")

	switch logLevel {
	case "fatal":
		log.SetLogLevel(log.FatalLevel)
	case "trace":
		log.SetLogLevel(log.TraceLevel)
	case "debug":
		log.SetLogLevel(log.DebugLevel)
	case "error":
		log.SetLogLevel(log.ErrorLevel)
	case "warn":
		log.SetLogLevel(log.WarnLevel)
	case "info":
		log.SetLogLevel(log.InfoLevel)
	default:
		log.SetLogLevel(log.InfoLevel)
	}

	log.GetFormatter().(*log.TextFormatter).SetTemplate(logTemplate)

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
		log.Info("Starting Prometheus endpoint on port " + port)
		http.Handle("/metrics", LoggingMiddleware(promhttp.Handler()))
		http.Handle("/healthz", LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("I'm OK. And you?"))
		})))
		http.ListenAndServe(":"+port, nil)
	}()

	// Kubernetes client
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Error("Failed to get in-cluster config: %v", err)
		panic(err.Error())
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Error("Failed to create Kubernetes client: %v", err)
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

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		log.Infof("Received %s request for %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
		duration := time.Since(startTime)
		log.Infof("Handled request for %s in %v", r.URL.Path, duration)
	})
}

func cleanupPreviousChecks(clientset kubernetes.Interface, namespace string) {

	log.Debug("Cleaning up previous checks")
	ctx := context.Background()

	// Find and delete pods from previous checks
	podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=storage-check",
	})

	if err == nil && len(podList.Items) > 0 {
		for _, pod := range podList.Items {
			err := clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
			if err != nil {
				log.Error("Failed to delete pod %s: %v", pod.Name, err)
				cleanupFailure.Inc()
			} else {
				log.Debug("Deleted pod %s", pod.Name)
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
				log.Error("Failed to delete PVC %s: %v", pvc.Name, err)
				cleanupFailure.Inc()
			} else {
				log.Debug("Deleted PVC %s", pvc.Name)
				cleanupSuccess.Inc()
			}
		}
	}
}

func doStorageCheck(clientset kubernetes.Interface, storageClass string, namespace string, image string) {

	log.Debug("Starting storage check")
	var user = int64(1000)
	var priviledged = bool(false)
	var readonly = bool(true)
	var noneroot = bool(true)

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
		log.Error("Failed to create PVC: %v", err)
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

					LivenessProbe: &corev1.Probe{
						InitialDelaySeconds: 5,
						PeriodSeconds:       5,
						TimeoutSeconds:      1,
						SuccessThreshold:    1,
						FailureThreshold:    3,
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/healthz",
								Port: intstr.FromInt(8080),
							},
						},
					},
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("200Mi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("48Mi"),
						},
					},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: &priviledged,
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL",
								"CAP_NET_RAW"},
						},
						Privileged:             &priviledged,
						ReadOnlyRootFilesystem: &readonly,
						RunAsGroup:             &user,
						RunAsUser:              &user,
						RunAsNonRoot:           &noneroot,
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
				RunAsNonRoot:       &noneroot,
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
		log.Error("Failed to create pod: %v", err)
		checkFailure.Inc()
		return
	}
	defer clientset.CoreV1().Pods(namespace).Delete(ctx, createdPod.Name, metav1.DeleteOptions{})

	// Wait for pod to complete
	for {
		p, _ := clientset.CoreV1().Pods(namespace).Get(ctx, createdPod.Name, metav1.GetOptions{})
		if p.Status.Phase == corev1.PodSucceeded {
			log.Debug("Storage check completed successfully")
			checkSuccess.Inc()
			checkDuration.Observe(time.Since(start).Seconds())
			return
		} else if p.Status.Phase == corev1.PodFailed {
			log.Debug("Storage check failed")
			checkFailure.Inc()
			return
		}
		time.Sleep(2 * time.Second)
	}
}
