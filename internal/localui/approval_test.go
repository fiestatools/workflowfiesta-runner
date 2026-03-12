//go:build !nolocalui

package localui_test

import (
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"workflowfiesta-runner/internal/localui"
)

func TestMain(m *testing.M) {
	localui.UseTestApp()
	os.Exit(m.Run())
}

// ── Fyne approval widget ──────────────────────────────────────────────────────

func TestApprovalWindow_AllowButton_ReturnsTrue(t *testing.T) {
	req := localui.SampleApprovalRequest(5 * time.Second)
	win, allowBtn, _, resultCh := localui.NewTestApprovalWindow(req)
	win.Show()
	defer win.Close()

	test.Tap(allowBtn)

	select {
	case result := <-resultCh:
		if result == localui.ApprovalDeny {
			t.Error("expected Allow to return true")
		}
	case <-time.After(time.Second):
		t.Fatal("no result after tapping Allow")
	}
}

func TestApprovalWindow_DenyButton_ReturnsFalse(t *testing.T) {
	req := localui.SampleApprovalRequest(5 * time.Second)
	win, _, denyBtn, resultCh := localui.NewTestApprovalWindow(req)
	win.Show()
	defer win.Close()

	test.Tap(denyBtn)

	select {
	case result := <-resultCh:
		if result != localui.ApprovalDeny {
			t.Error("expected Deny to return false")
		}
	case <-time.After(time.Second):
		t.Fatal("no result after tapping Deny")
	}
}

func TestApprovalWindow_CloseWithoutDecision_ReturnsFalse(t *testing.T) {
	req := localui.SampleApprovalRequest(5 * time.Second)
	win, _, _, resultCh := localui.NewTestApprovalWindow(req)
	win.Show()
	win.Close() // triggers SetOnClosed → decide(false)

	select {
	case result := <-resultCh:
		if result != localui.ApprovalDeny {
			t.Error("closing window should return false (deny)")
		}
	case <-time.After(time.Second):
		t.Fatal("no result after closing window")
	}
}

func TestApprovalWindow_ResultChannelBuffered(t *testing.T) {
	// Tapping both buttons in quick succession should not deadlock.
	req := localui.SampleApprovalRequest(5 * time.Second)
	win, allowBtn, denyBtn, resultCh := localui.NewTestApprovalWindow(req)
	win.Show()
	defer win.Close()

	test.Tap(allowBtn)
	test.Tap(denyBtn) // second tap should be a no-op (once.Do)

	select {
	case result := <-resultCh:
		if result == localui.ApprovalDeny {
			t.Error("first tap (Allow) should win")
		}
	case <-time.After(time.Second):
		t.Fatal("deadlock: no result received")
	}
}

func TestApprovalWindow_ShowsRunnerName(t *testing.T) {
	req := localui.SampleApprovalRequest(5 * time.Second)
	req.RunnerName = "my-test-runner"
	win, _, denyBtn, _ := localui.NewTestApprovalWindow(req)
	win.Show()
	defer win.Close()

	// Just verify it renders without panic.
	test.Tap(denyBtn)
}

func TestApprovalWindow_LongScript_Truncated(t *testing.T) {
	req := localui.SampleApprovalRequest(5 * time.Second)
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "echo line"
	}
	req.Script = ""
	for _, l := range lines {
		req.Script += l + "\n"
	}

	win, _, denyBtn, _ := localui.NewTestApprovalWindow(req)
	win.Show()
	defer win.Close()
	test.Tap(denyBtn)
}

func TestApprovalWindow_AllowSessionButton_ReturnsAllowSession(t *testing.T) {
	req := localui.SampleApprovalRequest(5 * time.Second)
	win, _, _, allowSessionBtn, _, resultCh := localui.NewTestApprovalWindowFull(req)
	win.Show()
	defer win.Close()
	test.Tap(allowSessionBtn)
	select {
	case result := <-resultCh:
		if result != localui.ApprovalAllowSession {
			t.Errorf("expected ApprovalAllowSession, got %v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("no result after tapping AllowSession")
	}
}

func TestApprovalWindow_AlwaysAllowButton_ReturnsAlwaysAllow(t *testing.T) {
	req := localui.SampleApprovalRequest(5 * time.Second)
	win, _, _, _, alwaysAllowBtn, resultCh := localui.NewTestApprovalWindowFull(req)
	win.Show()
	defer win.Close()
	test.Tap(alwaysAllowBtn)
	select {
	case result := <-resultCh:
		if result != localui.ApprovalAlwaysAllow {
			t.Errorf("expected ApprovalAlwaysAllow, got %v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("no result after tapping AlwaysAllow")
	}
}

// ── RequestApproval headless dispatch ────────────────────────────────────────

func TestRequestApproval_HeadlessTrue_UsesFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	old := localui.Headless
	localui.Headless = true
	defer func() { localui.Headless = old }()

	req := localui.ApprovalRequest{
		JobID:   "headless-dispatch-test",
		Script:  "ls ~/",
		Timeout: 150 * time.Millisecond,
	}

	done := make(chan localui.ApprovalResult, 1)
	go func() { done <- localui.RequestApproval(req) }()

	select {
	case result := <-done:
		// Should auto-deny on timeout (no stdin input).
		if result != localui.ApprovalDeny {
			t.Error("expected auto-deny from headless fallback")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval did not return")
	}
}
