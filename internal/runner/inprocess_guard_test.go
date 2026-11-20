package runner_test

import (
	"context"
	"strings"
	"testing"

	"workflowfiesta-runner/internal/api"
	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/runner"
)

func TestInProcessGuard_RefusesToolAndGitJobsWhenDisabled(t *testing.T) {
	cases := []api.Job{
		{JobID: "tool", ToolName: "read_file", ToolArgs: map[string]interface{}{"path": "/proc/self/environ"}},
		{JobID: "git", Script: "ls", GitRepoURL: "https://example.invalid/repo.git"},
	}
	for _, job := range cases {
		t.Run(job.JobID, func(t *testing.T) {
			sink := &recordingSink{}
			mock := &mockExecutor{}
			r := runner.NewForTestWithConfig(mock, &config.Config{ExecutorType: "kubernetes", AllowInProcessTools: false})
			r.WithSink(sink)
			runner.RunHandleJob(r, context.Background(), job)

			if len(mock.calls) != 0 {
				t.Errorf("executor should not run, got %d call(s)", len(mock.calls))
			}
			sink.mu.Lock()
			defer sink.mu.Unlock()
			if len(sink.jobsComplete) != 1 || sink.idle == 0 {
				t.Errorf("job should complete as failed and go idle: complete=%v idle=%d", sink.jobsComplete, sink.idle)
			}
			if !strings.Contains(strings.Join(sink.logs, ""), "WORKFLOWFIESTA_ALLOW_IN_PROCESS_TOOLS") {
				t.Errorf("refusal reason missing from logs: %q", sink.logs)
			}
		})
	}
}

func TestInProcessGuard_PlainScriptStillRuns(t *testing.T) {
	mock := &mockExecutor{}
	r := runner.NewForTestWithConfig(mock, &config.Config{ExecutorType: "kubernetes", AllowInProcessTools: false})
	runner.RunHandleJob(r, context.Background(), api.Job{JobID: "bash", Script: "echo hi", TimeoutSeconds: 5})
	if len(mock.calls) != 1 {
		t.Fatalf("plain script should reach the executor, got %d call(s)", len(mock.calls))
	}
}

func TestConfigLoad_InProcessToolsDefaultOffForKubernetes(t *testing.T) {
	t.Setenv("CONTAINER_RUNTIME", "kubernetes")
	t.Setenv("WORKFLOWFIESTA_ALLOW_IN_PROCESS_TOOLS", "")
	if config.Load().AllowInProcessTools {
		t.Error("kubernetes executor should default to no in-process tools")
	}
	t.Setenv("WORKFLOWFIESTA_ALLOW_IN_PROCESS_TOOLS", "true")
	if !config.Load().AllowInProcessTools {
		t.Error("explicit opt-in should be honoured")
	}
	t.Setenv("CONTAINER_RUNTIME", "local")
	t.Setenv("WORKFLOWFIESTA_ALLOW_IN_PROCESS_TOOLS", "")
	if !config.Load().AllowInProcessTools {
		t.Error("local executor keeps in-process tools by default")
	}
}
