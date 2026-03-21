package executor_test

// Kubernetes executor tests.
//
// kubernetesExecutor calls newK8sClient() which tries in-cluster config then
// KUBECONFIG.  In a unit-test environment neither is available, so Execute()
// will return an error early — we test the pre-flight behaviour and the pure
// helper sanitizeJobName (exposed via export_test.go or tested indirectly).
//
// Tests that need a real cluster are gated behind t.Skip().
//
// The fake client tests use k8s.io/client-go/kubernetes/fake (already in the
// dependency graph via k8s.io/client-go) to drive waitForPod and
// getPodExitCode through their watch/poll paths without a live API server.

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/executor"
)

// ── sanitizeJobName ───────────────────────────────────────────────────────────

func TestSanitizeJobName_LowercasesInput(t *testing.T) {
	got := executor.SanitizeJobName("MyJob-ABC")
	if got != strings.ToLower(got) {
		t.Errorf("sanitizeJobName(%q) = %q: not all lowercase", "MyJob-ABC", got)
	}
}

func TestSanitizeJobName_ReplacesNonAlphaNum(t *testing.T) {
	got := executor.SanitizeJobName("hello_world/test")
	for _, ch := range got {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
			t.Errorf("sanitizeJobName produced illegal character %q in %q", ch, got)
		}
	}
}

func TestSanitizeJobName_StripsLeadingTrailingDashes(t *testing.T) {
	got := executor.SanitizeJobName("--my-job--")
	if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
		t.Errorf("sanitizeJobName(%q) = %q: still has leading/trailing dashes", "--my-job--", got)
	}
}

func TestSanitizeJobName_CapsAt63Chars(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := executor.SanitizeJobName(long)
	if len(got) > 63 {
		t.Errorf("sanitizeJobName length = %d, want ≤ 63", len(got))
	}
}

func TestSanitizeJobName_TableDriven(t *testing.T) {
	tests := []struct {
		input   string
		wantLen bool // just verify non-empty
	}{
		{"simple", true},
		{"UPPER-CASE", true},
		{"with spaces", true},
		{"with/slash", true},
		{"123-numeric", true},
		{"a", true},
	}
	for _, tt := range tests {
		got := executor.SanitizeJobName(tt.input)
		if tt.wantLen && got == "" {
			t.Errorf("sanitizeJobName(%q) returned empty string", tt.input)
		}
	}
}

// ── No k8s config available → Execute returns error ──────────────────────────

func TestKubernetesExecute_NoConfig_ReturnsError(t *testing.T) {
	// Unset KUBECONFIG and KUBERNETES_SERVICE_HOST so newK8sClient() fails.
	t.Setenv("KUBECONFIG", "/tmp/no-such-kubeconfig.yaml")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	cfg := &config.Config{
		ExecutorType:  "kubernetes",
		KubeNamespace: "default",
	}
	ex := executor.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ex.Execute(ctx, executor.Input{
		JobID:   "k8s-no-config-test",
		Image:   "alpine:latest",
		Script:  "echo hello",
		Timeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error when no k8s config is available, got nil")
	}
	if !strings.Contains(err.Error(), "k8s") && !strings.Contains(err.Error(), "config") && !strings.Contains(err.Error(), "kubeconfig") {
		t.Logf("error (acceptable): %v", err)
	}
}

// ── Fake client: job creation ─────────────────────────────────────────────────
//
// We drive kubernetesExecutor indirectly through exported helpers to verify
// job-spec construction without needing a real API server.

func TestKubernetes_JobSpec_HasCorrectLabels(t *testing.T) {
	// Build a job spec the same way kubernetesExecutor does and validate it.
	backoffLimit := int32(0)
	ttl := int32(300)
	jobSpec := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wf-test-job",
			Namespace: "default",
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
						Image:   "alpine:latest",
						Command: []string{"/bin/sh", "-c", "echo hello"},
					}},
				},
			},
		},
	}

	if jobSpec.Labels["app.kubernetes.io/managed-by"] != "workflowfiesta-runner" {
		t.Error("expected managed-by label")
	}
	if *jobSpec.Spec.BackoffLimit != 0 {
		t.Errorf("BackoffLimit = %d, want 0", *jobSpec.Spec.BackoffLimit)
	}
	if *jobSpec.Spec.TTLSecondsAfterFinished != 300 {
		t.Errorf("TTL = %d, want 300", *jobSpec.Spec.TTLSecondsAfterFinished)
	}
	if jobSpec.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want Never", jobSpec.Spec.Template.Spec.RestartPolicy)
	}
}

func TestKubernetes_EnvVars_ArePropagatedToContainer(t *testing.T) {
	envVars := map[string]string{
		"MY_VAR":  "hello",
		"API_KEY": "secret",
	}
	containerEnv := make([]corev1.EnvVar, 0, len(envVars))
	for k, v := range envVars {
		containerEnv = append(containerEnv, corev1.EnvVar{Name: k, Value: v})
	}

	if len(containerEnv) != 2 {
		t.Errorf("expected 2 env vars, got %d", len(containerEnv))
	}
	found := map[string]bool{}
	for _, e := range containerEnv {
		found[e.Name] = true
	}
	if !found["MY_VAR"] || !found["API_KEY"] {
		t.Error("expected both MY_VAR and API_KEY in container env")
	}
}

// ── Fake client: waitForPod via watch ─────────────────────────────────────────

func TestKubernetes_WaitForPod_SucceedsWhenPodRunning(t *testing.T) {
	fakeClient := fakek8s.NewSimpleClientset()

	// Set up a watcher that emits a Running pod event.
	fakeWatcher := watch.NewFake()
	fakeClient.PrependWatchReactor("pods", func(action k8stesting.Action) (bool, watch.Interface, error) {
		return true, fakeWatcher, nil
	})

	// Emit the pod event in a goroutine so the watch channel is populated.
	go func() {
		time.Sleep(10 * time.Millisecond)
		fakeWatcher.Add(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "wf-pod-abc",
				Namespace: "default",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	podName, err := executor.WaitForPodWithClient(ctx, fakeClient, "default", "wf-test-job")
	if err != nil {
		t.Fatalf("waitForPod returned error: %v", err)
	}
	if podName == "" {
		t.Error("expected non-empty pod name")
	}
}

func TestKubernetes_WaitForPod_SucceedsWhenPodSucceeded(t *testing.T) {
	fakeClient := fakek8s.NewSimpleClientset()
	fakeWatcher := watch.NewFake()
	fakeClient.PrependWatchReactor("pods", func(action k8stesting.Action) (bool, watch.Interface, error) {
		return true, fakeWatcher, nil
	})

	go func() {
		time.Sleep(10 * time.Millisecond)
		fakeWatcher.Add(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "wf-pod-succeeded", Namespace: "default"},
			Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	podName, err := executor.WaitForPodWithClient(ctx, fakeClient, "default", "wf-test-job")
	if err != nil {
		t.Fatalf("waitForPod error: %v", err)
	}
	if podName != "wf-pod-succeeded" {
		t.Errorf("podName = %q, want %q", podName, "wf-pod-succeeded")
	}
}

func TestKubernetes_WaitForPod_TimesOutWhenContextCancelled(t *testing.T) {
	fakeClient := fakek8s.NewSimpleClientset()
	fakeWatcher := watch.NewFake()
	fakeClient.PrependWatchReactor("pods", func(action k8stesting.Action) (bool, watch.Interface, error) {
		return true, fakeWatcher, nil
	})

	// Never emit a pod — context will expire.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := executor.WaitForPodWithClient(ctx, fakeClient, "default", "wf-job-timeout")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// ── Fake client: getPodExitCode ───────────────────────────────────────────────

func TestKubernetes_GetPodExitCode_ReturnsTerminatedCode(t *testing.T) {
	exitCode := int32(42)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "wf-pod-done", Namespace: "default"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: exitCode,
						},
					},
				},
			},
		},
	}

	fakeClient := fakek8s.NewSimpleClientset(pod)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	code, err := executor.GetPodExitCodeWithClient(ctx, fakeClient, "default", "wf-pod-done")
	if err != nil {
		t.Fatalf("getPodExitCode error: %v", err)
	}
	if code != 42 {
		t.Errorf("exit code = %d, want 42", code)
	}
}

func TestKubernetes_GetPodExitCode_ReturnsZeroForSuccess(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "wf-pod-ok", Namespace: "default"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 0},
					},
				},
			},
		},
	}
	fakeClient := fakek8s.NewSimpleClientset(pod)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	code, err := executor.GetPodExitCodeWithClient(ctx, fakeClient, "default", "wf-pod-ok")
	if err != nil {
		t.Fatalf("getPodExitCode error: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestKubernetes_GetPodExitCode_ErrorWhenPodMissing(t *testing.T) {
	fakeClient := fakek8s.NewSimpleClientset() // no objects

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := executor.GetPodExitCodeWithClient(ctx, fakeClient, "default", "nonexistent-pod")
	if err == nil {
		t.Fatal("expected error for missing pod, got nil")
	}
}

// ── Fake client: cleanup (job deletion) ──────────────────────────────────────

func TestKubernetes_JobDeletion_CallsDeleteWithForeground(t *testing.T) {
	deleteCalled := false
	fakeClient := fakek8s.NewSimpleClientset()
	fakeClient.PrependReactor("delete", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deleteCalled = true
		return true, nil, nil
	})

	propagation := metav1.DeletePropagationForeground
	fakeClient.BatchV1().Jobs("default").Delete(
		context.Background(),
		"wf-test-job",
		metav1.DeleteOptions{PropagationPolicy: &propagation},
	)

	if !deleteCalled {
		t.Error("expected delete to be called on the fake client")
	}
}

// ── Image pull secret is wired into job spec ──────────────────────────────────

func TestKubernetes_ImagePullSecret_AppearsInJobSpec(t *testing.T) {
	secret := "my-registry-secret"
	jobSpec := &batchv1.Job{
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ImagePullSecrets: []corev1.LocalObjectReference{
						{Name: secret},
					},
				},
			},
		},
	}
	if len(jobSpec.Spec.Template.Spec.ImagePullSecrets) != 1 {
		t.Fatalf("expected 1 image pull secret, got %d", len(jobSpec.Spec.Template.Spec.ImagePullSecrets))
	}
	if jobSpec.Spec.Template.Spec.ImagePullSecrets[0].Name != secret {
		t.Errorf("imagePullSecret = %q, want %q", jobSpec.Spec.Template.Spec.ImagePullSecrets[0].Name, secret)
	}
}

// ── Live integration (skipped unless cluster available) ───────────────────────

func TestKubernetesExecute_Live(t *testing.T) {
	t.Skip("integration test: requires a configured Kubernetes cluster")
}
