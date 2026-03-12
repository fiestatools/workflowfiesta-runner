package localui

import (
	"strings"
	"testing"
	"time"
)

// ── truncateScript ────────────────────────────────────────────────────────────

func TestTruncateScript_ShortScript_Unchanged(t *testing.T) {
	script := "ls ~/\necho done"
	got := truncateScript(script, 10)
	if got != script {
		t.Errorf("short script should be unchanged\ngot:  %q\nwant: %q", got, script)
	}
}

func TestTruncateScript_LongScript_Truncated(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "echo line"
	}
	script := strings.Join(lines, "\n")
	got := truncateScript(script, 10)

	if strings.Count(got, "\n") >= 20 {
		t.Error("truncated script should have fewer lines than the original")
	}
	if !strings.Contains(got, "more lines") {
		t.Errorf("truncated script should mention remaining lines, got:\n%s", got)
	}
}

func TestTruncateScript_ExactMaxLines_Unchanged(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "echo line"
	}
	script := strings.Join(lines, "\n")
	got := truncateScript(script, 10)
	if got != script {
		t.Errorf("script with exactly maxLines lines should not be truncated")
	}
}

func TestTruncateScript_SingleLine(t *testing.T) {
	script := "echo hello"
	got := truncateScript(script, 5)
	if got != script {
		t.Errorf("single-line script should be unchanged")
	}
}

func TestTruncateScript_EmptyScript(t *testing.T) {
	got := truncateScript("", 10)
	if got != "" {
		t.Errorf("empty script should return empty, got %q", got)
	}
}

// ── headlessApproval ─────────────────────────────────────────────────────────

func TestHeadlessApproval_Timeout_Denies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in -short mode")
	}
	req := ApprovalRequest{
		JobID:   "test-timeout",
		Script:  "ls ~/",
		Timeout: 200 * time.Millisecond, // very short for testing
	}

	// headlessApproval reads from stdin — with no input it will timeout.
	// We use a very short timeout so the test completes quickly.
	result := make(chan ApprovalResult, 1)
	go func() { result <- headlessApproval(req) }()

	select {
	case approved := <-result:
		if approved != ApprovalDeny {
			t.Error("expected auto-deny on timeout, got approved")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("headlessApproval did not return after timeout")
	}
}

// ── RequestApproval headless dispatch ────────────────────────────────────────

func TestRequestApproval_Headless_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in -short mode")
	}
	old := Headless
	Headless = true
	defer func() { Headless = old }()

	req := ApprovalRequest{
		JobID:   "headless-timeout",
		Script:  "ls",
		Timeout: 200 * time.Millisecond,
	}

	done := make(chan ApprovalResult, 1)
	go func() { done <- RequestApproval(req) }()

	select {
	case result := <-done:
		if result != ApprovalDeny {
			t.Error("expected deny on timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval did not return")
	}
}
