package runner_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"workflowfiesta-runner/internal/api"
	"workflowfiesta-runner/internal/executor"
	"workflowfiesta-runner/internal/runner"
)

// ── scriptBlurb ───────────────────────────────────────────────────────────────

func TestScriptBlurb_FirstNonEmptyLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"echo hello", "echo hello"},
		{"\n\necho hello", "echo hello"},
		{"# comment\necho hello", "echo hello"},
		{"", ""},
		{"  \n  \n  echo spaced  ", "echo spaced"},
	}
	for _, tt := range tests {
		got := runner.ScriptBlurb(tt.input)
		if got != tt.want {
			t.Errorf("ScriptBlurb(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestScriptBlurb_TruncatesLongLine(t *testing.T) {
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'x'
	}
	got := runner.ScriptBlurb(string(long))
	if len([]rune(got)) > 80 {
		t.Errorf("ScriptBlurb length = %d, want ≤ 80", len([]rune(got)))
	}
}

func TestScriptBlurb_ExactlyEightyChars_NoTruncation(t *testing.T) {
	exactly80 := make([]byte, 80)
	for i := range exactly80 {
		exactly80[i] = 'a'
	}
	got := runner.ScriptBlurb(string(exactly80))
	if got != string(exactly80) {
		t.Error("80-char script should not be truncated")
	}
}

func TestScriptBlurb_SkipsCommentLines(t *testing.T) {
	script := "# this is a comment\n# another comment\nls -la"
	got := runner.ScriptBlurb(script)
	if got != "ls -la" {
		t.Errorf("ScriptBlurb = %q, want %q", got, "ls -la")
	}
}

// ── StatusSink mock ───────────────────────────────────────────────────────────

type recordingSink struct {
	mu           sync.Mutex
	connected    []bool
	idle         int
	jobsRunning  []string
	jobsComplete []string
	logs         []string
}

func (r *recordingSink) SetConnecting()   {}
func (r *recordingSink) SetReconnecting() {}
func (r *recordingSink) SetConnected(connected bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connected = append(r.connected, connected)
}
func (r *recordingSink) SetIdle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idle++
}
func (r *recordingSink) SetJobRunning(jobID, _, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobsRunning = append(r.jobsRunning, jobID)
}
func (r *recordingSink) SetJobComplete(jobID, _ string, _ int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobsComplete = append(r.jobsComplete, jobID)
}
func (r *recordingSink) AppendLog(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, line)
}

// ── Mock executor ─────────────────────────────────────────────────────────────

type mockExecutor struct {
	exitCode int
	err      error
	output   string
	mu       sync.Mutex
	calls    []executor.Input
}

func (m *mockExecutor) Execute(_ context.Context, input executor.Input) (int, error) {
	m.mu.Lock()
	m.calls = append(m.calls, input)
	m.mu.Unlock()
	if input.OutputChan != nil && m.output != "" {
		input.OutputChan <- m.output
	}
	return m.exitCode, m.err
}

// ── Runner.WithSink ───────────────────────────────────────────────────────────

func TestRunner_WithSink_ReturnsSelf(t *testing.T) {
	r := runner.NewForTest(nil, nil)
	sink := &recordingSink{}
	got := r.WithSink(sink)
	if got == nil {
		t.Fatal("WithSink should return the runner")
	}
}

func TestRunner_MultipleSinks_AllReceiveEvents(t *testing.T) {
	sink1 := &recordingSink{}
	sink2 := &recordingSink{}

	mock := &mockExecutor{exitCode: 0, output: "hello\n"}
	r := runner.NewForTest(mock, nil)
	r.WithSink(sink1).WithSink(sink2)

	job := api.Job{
		JobID:          "multi-sink-job",
		DockerImage:    "alpine:latest",
		Script:         "echo hello",
		TimeoutSeconds: 5,
	}
	runner.RunHandleJob(r, context.Background(), job)

	sink1.mu.Lock()
	s1running := len(sink1.jobsRunning)
	sink1.mu.Unlock()

	sink2.mu.Lock()
	s2running := len(sink2.jobsRunning)
	sink2.mu.Unlock()

	if s1running == 0 {
		t.Error("sink1 should have received SetJobRunning")
	}
	if s2running == 0 {
		t.Error("sink2 should have received SetJobRunning")
	}
}

// ── handleJob lifecycle ───────────────────────────────────────────────────────

func TestHandleJob_ExitZero_NotifiesComplete(t *testing.T) {
	sink := &recordingSink{}
	mock := &mockExecutor{exitCode: 0, output: "done\n"}
	r := runner.NewForTest(mock, nil)
	r.WithSink(sink)

	job := api.Job{
		JobID:          "job-exit-0",
		DockerImage:    "alpine:latest",
		Script:         "echo done",
		TimeoutSeconds: 5,
	}
	runner.RunHandleJob(r, context.Background(), job)

	sink.mu.Lock()
	complete := len(sink.jobsComplete)
	idle := sink.idle
	sink.mu.Unlock()

	if complete == 0 {
		t.Error("SetJobComplete should have been called")
	}
	if idle == 0 {
		t.Error("SetIdle should have been called after completion")
	}
}

func TestHandleJob_NonZeroExit_NotifiesComplete(t *testing.T) {
	sink := &recordingSink{}
	mock := &mockExecutor{exitCode: 1}
	r := runner.NewForTest(mock, nil)
	r.WithSink(sink)

	job := api.Job{
		JobID:          "job-exit-1",
		DockerImage:    "alpine:latest",
		Script:         "exit 1",
		TimeoutSeconds: 5,
	}
	runner.RunHandleJob(r, context.Background(), job)

	sink.mu.Lock()
	complete := len(sink.jobsComplete)
	sink.mu.Unlock()

	if complete == 0 {
		t.Error("SetJobComplete should have been called even for non-zero exit")
	}
}

func TestHandleJob_Error_ReportsFailure(t *testing.T) {
	sink := &recordingSink{}
	mock := &mockExecutor{exitCode: -1, err: errors.New("mock executor error")}
	r := runner.NewForTest(mock, nil)
	r.WithSink(sink)

	job := api.Job{
		JobID:          "job-err",
		DockerImage:    "alpine:latest",
		Script:         "fail",
		TimeoutSeconds: 5,
	}
	runner.RunHandleJob(r, context.Background(), job)

	sink.mu.Lock()
	complete := len(sink.jobsComplete)
	idle := sink.idle
	sink.mu.Unlock()

	if complete == 0 {
		t.Error("SetJobComplete should have been called on executor error")
	}
	if idle == 0 {
		t.Error("SetIdle should have been called after error")
	}
}

func TestHandleJob_OutputStreamed_ToSink(t *testing.T) {
	sink := &recordingSink{}
	mock := &mockExecutor{exitCode: 0, output: "streamed-output\n"}
	r := runner.NewForTest(mock, nil)
	r.WithSink(sink)

	job := api.Job{
		JobID:          "job-output",
		DockerImage:    "alpine:latest",
		Script:         "echo streamed-output",
		TimeoutSeconds: 5,
	}
	runner.RunHandleJob(r, context.Background(), job)

	sink.mu.Lock()
	logs := append([]string{}, sink.logs...)
	sink.mu.Unlock()

	var combined string
	for _, l := range logs {
		combined += l
	}
	if combined == "" {
		t.Error("AppendLog should have been called with streamed output")
	}
}

// ── Semaphore / concurrency limiting ─────────────────────────────────────────

func TestSemaphore_LimitsConcurrency(t *testing.T) {
	const maxJobs = 2
	const totalJobs = 5

	var (
		active    int64
		maxActive int64
		mu        sync.Mutex
	)

	slowExec := &funcExecutor{fn: func(input executor.Input) (int, error) {
		cur := atomic.AddInt64(&active, 1)
		mu.Lock()
		if cur > maxActive {
			maxActive = cur
		}
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt64(&active, -1)
		return 0, nil
	}}

	r := runner.NewForTestWithConcurrency(slowExec, nil, maxJobs)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < totalJobs; i++ {
		wg.Add(1)
		jobID := fmt.Sprintf("sem-job-%d", i)
		go func(id string) {
			defer wg.Done()
			// Acquire semaphore slot manually (mirrors what Run() does).
			runner.RunHandleJobWithSemaphore(r, ctx, api.Job{
				JobID:          id,
				DockerImage:    "alpine:latest",
				Script:         "echo hi",
				TimeoutSeconds: 5,
			})
		}(jobID)
	}
	wg.Wait()

	mu.Lock()
	peak := maxActive
	mu.Unlock()

	if peak > int64(maxJobs) {
		t.Errorf("peak concurrency = %d, exceeds semaphore limit %d", peak, maxJobs)
	}
}

type funcExecutor struct {
	fn func(executor.Input) (int, error)
}

func (f *funcExecutor) Execute(_ context.Context, input executor.Input) (int, error) {
	if f.fn != nil {
		return f.fn(input)
	}
	return 0, nil
}

// ── Deduplication via activeJobs ──────────────────────────────────────────────

func TestActiveJobs_DuplicateDispatch_IsSkipped(t *testing.T) {
	var callCount int64
	countingExec := &funcExecutor{fn: func(input executor.Input) (int, error) {
		atomic.AddInt64(&callCount, 1)
		time.Sleep(50 * time.Millisecond)
		return 0, nil
	}}
	r := runner.NewForTest(countingExec, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runner.RunHandleJobTracked(r, ctx, api.Job{
			JobID:          "dup-job-1",
			DockerImage:    "alpine:latest",
			Script:         "echo first",
			TimeoutSeconds: 5,
		})
	}()

	// Give first goroutine time to register in activeJobs.
	time.Sleep(5 * time.Millisecond)

	// Second dispatch of the same jobID should be skipped.
	runner.RunHandleJobTracked(r, ctx, api.Job{
		JobID:          "dup-job-1",
		DockerImage:    "alpine:latest",
		Script:         "echo duplicate",
		TimeoutSeconds: 5,
	})
	wg.Wait()

	got := atomic.LoadInt64(&callCount)
	if got != 1 {
		t.Errorf("executor called %d times, want 1 (deduplication should suppress second dispatch)", got)
	}
}

// ── Timeout default when TimeoutSeconds is 0 ─────────────────────────────────

func TestHandleJob_ZeroTimeoutSeconds_DefaultsToFiveMinutes(t *testing.T) {
	var capturedTimeout time.Duration
	capturingExec := &funcExecutor{fn: func(input executor.Input) (int, error) {
		capturedTimeout = input.Timeout
		return 0, nil
	}}
	r := runner.NewForTest(capturingExec, nil)

	runner.RunHandleJob(r, context.Background(), api.Job{
		JobID:          "zero-timeout-job",
		DockerImage:    "alpine:latest",
		Script:         "echo hi",
		TimeoutSeconds: 0,
	})

	if capturedTimeout != 5*time.Minute {
		t.Errorf("timeout = %v, want 5m", capturedTimeout)
	}
}

func TestHandleJob_ExplicitTimeout_IsHonoured(t *testing.T) {
	var capturedTimeout time.Duration
	capturingExec := &funcExecutor{fn: func(input executor.Input) (int, error) {
		capturedTimeout = input.Timeout
		return 0, nil
	}}
	r := runner.NewForTest(capturingExec, nil)

	runner.RunHandleJob(r, context.Background(), api.Job{
		JobID:          "explicit-timeout-job",
		DockerImage:    "alpine:latest",
		Script:         "echo hi",
		TimeoutSeconds: 30,
	})

	if capturedTimeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", capturedTimeout)
	}
}

// ── EnvVars forwarded to executor ─────────────────────────────────────────────

func TestHandleJob_EnvVars_ForwardedToExecutor(t *testing.T) {
	var capturedInput executor.Input
	capturingExec := &funcExecutor{fn: func(input executor.Input) (int, error) {
		capturedInput = input
		return 0, nil
	}}
	r := runner.NewForTest(capturingExec, nil)

	job := api.Job{
		JobID:          "env-forward-job",
		DockerImage:    "alpine:latest",
		Script:         "echo $MY_VAR",
		EnvVars:        map[string]string{"MY_VAR": "from-runner"},
		TimeoutSeconds: 5,
	}
	runner.RunHandleJob(r, context.Background(), job)

	if capturedInput.EnvVars["MY_VAR"] != "from-runner" {
		t.Errorf("EnvVars not forwarded correctly: got %v", capturedInput.EnvVars)
	}
}

// ── handleRunLocalScript ──────────────────────────────────────────────────────

func TestHandleRunLocalScript_EnvVarsForwarded(t *testing.T) {
	// Capture the executor input to verify env_vars from tool_args are merged.
	var capturedInput executor.Input
	capturingExec := &funcExecutor{fn: func(input executor.Input) (int, error) {
		capturedInput = input
		return 0, nil
	}}

	// Set up a tool handler with a real temp dir containing a test script.
	toolHandler, scriptContent := runner.NewToolHandlerForTest(t)

	r := runner.NewForTestWithToolHandler(capturingExec, toolHandler)

	job := api.Job{
		JobID:    "run-local-env-job",
		ToolName: "run_local_script",
		ToolArgs: map[string]interface{}{
			"name": "test-script.sh",
			"env_vars": map[string]interface{}{
				"MY_VAR": "hello",
			},
		},
		TimeoutSeconds: 5,
	}
	runner.RunHandleRunLocalScript(r, context.Background(), job)

	if capturedInput.Script != scriptContent {
		t.Errorf("script content mismatch: got %q, want %q", capturedInput.Script, scriptContent)
	}
	if capturedInput.EnvVars["MY_VAR"] != "hello" {
		t.Errorf("env var MY_VAR not forwarded: got %v", capturedInput.EnvVars)
	}
}

func TestHandleRunLocalScript_MissingName(t *testing.T) {
	sink := &recordingSink{}
	mock := &mockExecutor{}
	r := runner.NewForTest(mock, nil)
	r.WithSink(sink)

	job := api.Job{
		JobID:    "missing-name-job",
		ToolName: "run_local_script",
		ToolArgs: map[string]interface{}{}, // no "name" key
	}
	runner.RunHandleRunLocalScript(r, context.Background(), job)

	// Should notify idle (not crash)
	sink.mu.Lock()
	idle := sink.idle
	sink.mu.Unlock()
	if idle == 0 {
		t.Error("SetIdle should have been called after missing name failure")
	}
}

// ── handleAuthRevoked ─────────────────────────────────────────────────────────

func TestHandleAuthRevoked_SingleFailure_ReturnsNil(t *testing.T) {
	r := runner.NewForTestWithRevoked(func(_ string) {
		t.Error("onRevoked should not fire on first auth failure")
	})
	err := r.HandleAuthRevoked(api.AuthRevokedError(401, "/test", nil))
	if err != nil {
		t.Errorf("expected nil on first failure, got %v", err)
	}
}

func TestHandleAuthRevoked_ThresholdReached_ReturnsError(t *testing.T) {
	var called int32
	r := runner.NewForTestWithRevoked(func(_ string) { atomic.AddInt32(&called, 1) })

	authErr := api.AuthRevokedError(401, "/test", nil)

	// First failure — below threshold
	if err := r.HandleAuthRevoked(authErr); err != nil {
		t.Fatalf("expected nil on first failure, got %v", err)
	}
	// Second failure — hits threshold
	if err := r.HandleAuthRevoked(authErr); !errors.Is(err, runner.ErrRegistrationRevoked) {
		t.Errorf("expected ErrRegistrationRevoked on second failure, got %v", err)
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("onRevoked called %d times, want 1", called)
	}
}

func TestHandleAuthRevoked_ResetsOnSuccess(t *testing.T) {
	var called int32
	r := runner.NewForTestWithRevoked(func(_ string) { atomic.AddInt32(&called, 1) })

	authErr := api.AuthRevokedError(401, "/test", nil)

	// One failure, then a success resets the counter
	_ = r.HandleAuthRevoked(authErr)
	_ = r.HandleAuthRevoked(nil) // success — resets authFailures to 0
	// One more failure — should not trigger (counter reset)
	if err := r.HandleAuthRevoked(authErr); err != nil {
		t.Errorf("expected nil after reset, got %v", err)
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Error("onRevoked should not have fired after counter reset")
	}
}

func TestHandleAuthRevoked_BothGoroutinesReturnError(t *testing.T) {
	var callbackCount int32
	r := runner.NewForTestWithRevoked(func(_ string) { atomic.AddInt32(&callbackCount, 1) })

	authErr := api.AuthRevokedError(401, "/test", nil)

	// Saturate the threshold first
	_ = r.HandleAuthRevoked(authErr)

	// Now fire concurrently — both should get ErrRegistrationRevoked,
	// but the callback must fire exactly once.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = r.HandleAuthRevoked(authErr)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, runner.ErrRegistrationRevoked) {
			t.Errorf("goroutine %d: expected ErrRegistrationRevoked, got %v", i, err)
		}
	}
	if n := atomic.LoadInt32(&callbackCount); n != 1 {
		t.Errorf("onRevoked called %d times, want exactly 1", n)
	}
}
