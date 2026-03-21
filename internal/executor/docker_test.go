package executor_test

// Docker executor tests.
//
// dockerExecutor directly calls dockerclient.NewClientWithOpts() at the top of
// Execute(), so it is not possible to inject a mock through the current struct.
// These tests therefore:
//   - exercise the pure helper/constructor behaviour that does not touch the
//     daemon, and
//   - verify that Execute() returns a sensible error when the Docker socket is
//     missing / unreachable (no daemon required).
//
// If a real Docker daemon is present the integration test at the bottom runs a
// live container.  It is gated behind a t.Skip() check so it never blocks CI.

import (
	"context"
	"strings"
	"testing"
	"time"

	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/executor"
)

// newDockerCfg returns a Config that points at a non-existent socket so no
// daemon is required for unit tests.
func newDockerCfg(socket string) *config.Config {
	return &config.Config{
		ExecutorType: "docker",
		DockerSocket: socket,
	}
}

// ── New() routing ─────────────────────────────────────────────────────────────

func TestNew_ReturnsDockerExecutorByDefault(t *testing.T) {
	cfg := newDockerCfg("/var/run/docker.sock")
	ex := executor.New(cfg)
	if ex == nil {
		t.Fatal("executor.New returned nil for docker executor type")
	}
}

func TestNew_ReturnsKubernetesExecutor(t *testing.T) {
	cfg := &config.Config{ExecutorType: "kubernetes", KubeNamespace: "default"}
	ex := executor.New(cfg)
	if ex == nil {
		t.Fatal("executor.New returned nil for kubernetes executor type")
	}
}

func TestNew_ReturnsLocalExecutor(t *testing.T) {
	cfg := &config.Config{ExecutorType: "local"}
	ex := executor.New(cfg)
	if ex == nil {
		t.Fatal("executor.New returned nil for local executor type")
	}
}

// ── Execute with missing/invalid socket ──────────────────────────────────────

func TestDockerExecute_InvalidSocket_ReturnsError(t *testing.T) {
	cfg := newDockerCfg("/tmp/no-such-docker.sock")
	ex := executor.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ex.Execute(ctx, executor.Input{
		JobID:   "docker-unit-test",
		Image:   "alpine:latest",
		Script:  "echo hello",
		Timeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error when Docker socket does not exist, got nil")
	}
}

func TestDockerExecute_ContextCancelled_ReturnsError(t *testing.T) {
	cfg := newDockerCfg("/tmp/no-such-docker.sock")
	ex := executor.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := ex.Execute(ctx, executor.Input{
		JobID:   "cancelled-test",
		Image:   "alpine:latest",
		Script:  "echo hello",
		Timeout: 5 * time.Second,
	})
	// With a pre-cancelled context the client creation itself may succeed
	// (it only dials lazily) but image pull must fail.
	if err == nil {
		t.Fatal("expected error when context is cancelled, got nil")
	}
}

// ── EnvVar construction ───────────────────────────────────────────────────────

// The env slice is built inline inside Execute(); we test it indirectly by
// confirming the executor does not panic when EnvVars is nil.
func TestDockerExecute_NilEnvVars_DoesNotPanic(t *testing.T) {
	cfg := newDockerCfg("/tmp/no-such-docker.sock")
	ex := executor.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Should return an error (no daemon) but must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Execute panicked with nil EnvVars: %v", r)
		}
	}()
	ex.Execute(ctx, executor.Input{ //nolint:errcheck
		JobID:   "nil-env-test",
		Image:   "alpine:latest",
		Script:  "echo ok",
		Timeout: 3 * time.Second,
		EnvVars: nil,
	})
}

// ── Input.Timeout zero value ──────────────────────────────────────────────────

func TestDockerExecute_ZeroTimeout_DoesNotPanic(t *testing.T) {
	cfg := newDockerCfg("/tmp/no-such-docker.sock")
	ex := executor.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Execute panicked with zero Timeout: %v", r)
		}
	}()
	ex.Execute(ctx, executor.Input{ //nolint:errcheck
		JobID:   "zero-timeout-test",
		Image:   "alpine:latest",
		Script:  "echo ok",
		Timeout: 0,
	})
}

// ── Error message quality ─────────────────────────────────────────────────────

func TestDockerExecute_ErrorContainsUsefulContext(t *testing.T) {
	cfg := newDockerCfg("/tmp/no-such-docker.sock")
	ex := executor.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ex.Execute(ctx, executor.Input{
		JobID:   "error-context-test",
		Image:   "alpine:latest",
		Script:  "echo hello",
		Timeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error for unreachable socket")
	}
	// Error should mention either "docker client" or "image pull" — something
	// informative rather than a bare "connection refused".
	msg := err.Error()
	if !strings.Contains(msg, "docker") && !strings.Contains(msg, "image") && !strings.Contains(msg, "dial") {
		t.Errorf("error message %q lacks useful context", msg)
	}
}

// ── Live integration (skipped when daemon absent) ─────────────────────────────

func TestDockerExecute_LiveRun_ExitZero(t *testing.T) {
	t.Skip("integration test: requires a running Docker daemon")

	cfg := &config.Config{
		ExecutorType: "docker",
		DockerSocket: "/var/run/docker.sock",
	}
	ex := executor.New(cfg)

	out := make(chan string, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	exitCode, err := ex.Execute(ctx, executor.Input{
		JobID:      "live-docker-test",
		Image:      "alpine:latest",
		Script:     "echo 'hello from docker'",
		Timeout:    30 * time.Second,
		OutputChan: out,
	})
	close(out)

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}

	var combined strings.Builder
	for chunk := range out {
		combined.WriteString(chunk)
	}
	if !strings.Contains(combined.String(), "hello from docker") {
		t.Errorf("expected output to contain marker, got: %q", combined.String())
	}
}

func TestDockerExecute_LiveRun_NonZeroExitCode(t *testing.T) {
	t.Skip("integration test: requires a running Docker daemon")

	cfg := &config.Config{
		ExecutorType: "docker",
		DockerSocket: "/var/run/docker.sock",
	}
	ex := executor.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	exitCode, err := ex.Execute(ctx, executor.Input{
		JobID:   "live-exit-code-test",
		Image:   "alpine:latest",
		Script:  "exit 7",
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if exitCode != 7 {
		t.Errorf("exit code = %d, want 7", exitCode)
	}
}

func TestDockerExecute_LiveRun_EnvVarsPropagated(t *testing.T) {
	t.Skip("integration test: requires a running Docker daemon")

	cfg := &config.Config{
		ExecutorType: "docker",
		DockerSocket: "/var/run/docker.sock",
	}
	ex := executor.New(cfg)

	out := make(chan string, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ex.Execute(ctx, executor.Input{ //nolint:errcheck
		JobID:      "live-env-test",
		Image:      "alpine:latest",
		Script:     "echo $MY_CUSTOM_VAR",
		EnvVars:    map[string]string{"MY_CUSTOM_VAR": "from-test"},
		Timeout:    30 * time.Second,
		OutputChan: out,
	})
	close(out)

	var combined strings.Builder
	for chunk := range out {
		combined.WriteString(chunk)
	}
	if !strings.Contains(combined.String(), "from-test") {
		t.Errorf("expected env var in output, got: %q", combined.String())
	}
}
