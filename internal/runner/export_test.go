package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"workflowfiesta-runner/internal/api"
	"workflowfiesta-runner/internal/executor"
	"workflowfiesta-runner/internal/localconfig"
)

// NewForTest creates a Runner with an injected executor and a stub API client
// (not connected — all send calls will silently fail).
// The semaphore defaults to 4 concurrent jobs.
func NewForTest(exec executor.Executor, _ interface{}) *Runner {
	return &Runner{
		client:    api.New("http://localhost:0", "test-token"),
		executor:  exec,
		semaphore: make(chan struct{}, 4),
	}
}

// NewForTestWithConcurrency creates a Runner with a specific semaphore size.
func NewForTestWithConcurrency(exec executor.Executor, _ interface{}, maxJobs int) *Runner {
	if maxJobs <= 0 {
		maxJobs = 4
	}
	return &Runner{
		client:    api.New("http://localhost:0", "test-token"),
		executor:  exec,
		semaphore: make(chan struct{}, maxJobs),
	}
}

// RunHandleJob calls handleJob directly, bypassing the WS listener and
// semaphore acquisition — used for single-job lifecycle tests.
func RunHandleJob(r *Runner, ctx context.Context, job api.Job) {
	r.handleJob(ctx, job)
}

// RunHandleJobTracked calls handleJob after registering job in activeJobs so
// that duplicate-dispatch tests work correctly.  The second call with the same
// jobID is a no-op (returns false) to simulate what Run() does.
func RunHandleJobTracked(r *Runner, ctx context.Context, job api.Job) bool {
	if _, loaded := r.activeJobs.LoadOrStore(job.JobID, struct{}{}); loaded {
		return false // duplicate — skipped
	}
	defer r.activeJobs.Delete(job.JobID)
	r.handleJob(ctx, job)
	return true
}

// RunHandleJobWithSemaphore acquires the semaphore before calling handleJob,
// mirroring what the Run() loop does.  Used by concurrency tests.
func RunHandleJobWithSemaphore(r *Runner, ctx context.Context, job api.Job) {
	select {
	case r.semaphore <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-r.semaphore }()
	r.handleJob(ctx, job)
}

// RunHandleRunLocalScript calls handleRunLocalScript directly for unit tests.
func RunHandleRunLocalScript(r *Runner, ctx context.Context, job api.Job) {
	r.handleRunLocalScript(ctx, job)
}

// NewForTestWithToolHandler creates a Runner with an injected executor and tool handler.
func NewForTestWithToolHandler(exec executor.Executor, handler *executor.ToolHandler) *Runner {
	return &Runner{
		client:      api.New("http://localhost:0", "test-token"),
		executor:    exec,
		toolHandler: handler,
		semaphore:   make(chan struct{}, 4),
	}
}

// NewToolHandlerForTest creates a ToolHandler backed by a temp directory containing
// a test script "test-script.sh". Returns the handler and the script content.
func NewToolHandlerForTest(t *testing.T) (*executor.ToolHandler, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := localconfig.Default()
	orgID := "testorg-runner-" + t.Name()
	cfg.AllowedPaths = []string{dir}

	handler := executor.NewToolHandler(cfg)
	handler.SetOrgID(orgID)

	// Create the script library directory and the test script
	home, _ := os.UserHomeDir()
	libDir := filepath.Join(home, ".workflowfiesta", "scripts", orgID)
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir libDir: %v", err)
	}

	scriptContent := "#!/bin/bash\necho hello from local script"
	scriptPath := filepath.Join(libDir, "test-script.sh")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write test script: %v", err)
	}

	// Register cleanup to remove the script after test
	t.Cleanup(func() {
		os.RemoveAll(libDir)
	})

	return handler, scriptContent
}

// ScriptBlurb exposes the package-level scriptBlurb for unit tests.
var ScriptBlurb = scriptBlurb

// ReposCacheDir exposes the package-level reposCacheDir for unit tests.
var ReposCacheDir = reposCacheDir

// WorktreePath exposes the package-level worktreePath for unit tests.
var WorktreePath = worktreePath
