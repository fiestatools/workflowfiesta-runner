package executor

import (
	"context"

	"k8s.io/client-go/kubernetes"
)

// Expose internal helpers for testing.
var ScriptFingerprint = scriptFingerprint

// CurrentOS allows tests to override the OS name without needing a real host.
var CurrentOS = &currentOS

// SanitizeJobName exposes sanitizeJobName for unit tests.
var SanitizeJobName = sanitizeJobName

// WaitForPodWithClient exposes kubernetesExecutor.waitForPod for unit tests,
// accepting an explicit client so tests can inject a fake.
func WaitForPodWithClient(ctx context.Context, client kubernetes.Interface, namespace, jobName string) (string, error) {
	k := &kubernetesExecutor{}
	return k.waitForPod(ctx, client, namespace, jobName)
}

// GetPodExitCodeWithClient exposes kubernetesExecutor.getPodExitCode for unit
// tests, accepting an explicit client so tests can inject a fake.
func GetPodExitCodeWithClient(ctx context.Context, client kubernetes.Interface, namespace, podName string) (int, error) {
	k := &kubernetesExecutor{}
	return k.getPodExitCode(ctx, client, namespace, podName)
}
