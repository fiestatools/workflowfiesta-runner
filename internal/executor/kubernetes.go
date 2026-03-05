package executor

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"workflowfiesta-runner/internal/config"
)

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitizeJobName(name string) string {
	s := strings.ToLower(name)
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

func newK8sClient() (kubernetes.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to KUBECONFIG
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("k8s config: %w", err)
		}
	}
	return kubernetes.NewForConfig(cfg)
}

type kubernetesExecutor struct {
	cfg *config.Config
}

func (k *kubernetesExecutor) Execute(ctx context.Context, input Input) (int, error) {
	client, err := newK8sClient()
	if err != nil {
		return -1, err
	}

	namespace := k.cfg.KubeNamespace
	suffix := fmt.Sprintf("%06x", rand.Int63n(0xFFFFFF))
	jobName := sanitizeJobName(fmt.Sprintf("wf-%s", suffix))

	backoffLimit := int32(0)
	ttl := int32(300) // clean up after 5 minutes

	envVars := make([]corev1.EnvVar, 0, len(input.EnvVars))
	for key, val := range input.EnvVars {
		envVars = append(envVars, corev1.EnvVar{Name: key, Value: val})
	}

	jobSpec := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "workflowfiesta-runner",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:    "runner",
						Image:   input.Image,
						Command: []string{"/bin/sh", "-c", input.Script},
						Env:     envVars,
					}},
				},
			},
		},
	}

	if k.cfg.KubeImagePullSecret != "" {
		jobSpec.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: k.cfg.KubeImagePullSecret},
		}
	}

	log.Infof("Creating Kubernetes Job %s/%s (image: %s)", namespace, jobName, input.Image)

	_, err = client.BatchV1().Jobs(namespace).Create(ctx, jobSpec, metav1.CreateOptions{})
	if err != nil {
		return -1, fmt.Errorf("create job: %w", err)
	}

	// Clean up job when done
	propagation := metav1.DeletePropagationForeground
	defer func() {
		client.BatchV1().Jobs(namespace).Delete(context.Background(), jobName, metav1.DeleteOptions{
			PropagationPolicy: &propagation,
		})
	}()

	// Wait for the job's pod to appear and become Running, then stream logs
	timeoutCtx, cancel := context.WithTimeout(ctx, input.Timeout)
	defer cancel()

	podName, err := k.waitForPod(timeoutCtx, client, namespace, jobName)
	if err != nil {
		return -1, fmt.Errorf("waiting for pod: %w", err)
	}

	// Stream logs
	if err := k.streamLogs(timeoutCtx, client, namespace, podName, input.OutputChan); err != nil {
		log.Warnf("Log streaming error for job %s: %v", jobName, err)
	}

	// Get final exit code from pod status
	return k.getPodExitCode(timeoutCtx, client, namespace, podName)
}

func (k *kubernetesExecutor) waitForPod(ctx context.Context, client kubernetes.Interface, namespace, jobName string) (string, error) {
	watcher, err := client.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
		Watch:         true,
	})
	if err != nil {
		return "", fmt.Errorf("watch pods: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return "", fmt.Errorf("pod watch channel closed")
			}
			if event.Type == watch.Added || event.Type == watch.Modified {
				pod, ok := event.Object.(*corev1.Pod)
				if !ok {
					continue
				}
				phase := pod.Status.Phase
				if phase == corev1.PodRunning || phase == corev1.PodSucceeded || phase == corev1.PodFailed {
					return pod.Name, nil
				}
			}
		case <-time.After(5 * time.Minute):
			return "", fmt.Errorf("pod did not start within 5 minutes")
		}
	}
}

func (k *kubernetesExecutor) streamLogs(ctx context.Context, client kubernetes.Interface, namespace, podName string, outputChan chan<- string) error {
	follow := true
	req := client.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Follow: follow,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("log stream: %w", err)
	}
	defer stream.Close()

	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if n > 0 && outputChan != nil {
			outputChan <- string(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return nil
}

func (k *kubernetesExecutor) getPodExitCode(ctx context.Context, client kubernetes.Interface, namespace, podName string) (int, error) {
	// Poll for terminated status
	for i := 0; i < 30; i++ {
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return -1, fmt.Errorf("get pod: %w", err)
		}
		if len(pod.Status.ContainerStatuses) > 0 {
			s := pod.Status.ContainerStatuses[0].State
			if s.Terminated != nil {
				return int(s.Terminated.ExitCode), nil
			}
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return -1, fmt.Errorf("pod did not reach terminated state")
}
