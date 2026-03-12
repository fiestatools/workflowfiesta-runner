//go:build !nolocalui

package localui

import (
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// UseTestApp replaces the shared fyne.App with a test driver that works without
// a display server.  Call this once per test binary (e.g. in TestMain).
func UseTestApp() {
	fyneOnce = sync.Once{}
	fyneApp = nil
	appFactory = func() fyne.App { return test.NewApp() }
}

// NewTestApprovalWindow creates an approval window backed by the test app and
// returns the window and the button references so tests can tap them directly.
func NewTestApprovalWindow(req ApprovalRequest) (win fyne.Window, allow, deny *widget.Button, resultCh <-chan ApprovalResult) {
	s := newApprovalState()
	w := s.buildWindow(req, getApp())
	return w, &s.allowBtn.Button, &s.denyBtn.Button, s.resultCh
}

// NewTestApprovalWindowFull creates an approval window and returns all four button refs.
func NewTestApprovalWindowFull(req ApprovalRequest) (win fyne.Window, allow, deny, allowSession, alwaysAllow *widget.Button, resultCh <-chan ApprovalResult) {
	s := newApprovalState()
	w := s.buildWindow(req, getApp())
	return w, &s.allowBtn.Button, &s.denyBtn.Button, &s.allowSessionBtn.Button, &s.alwaysAllowBtn.Button, s.resultCh
}

// RegisterAPI is the internal callRegisterAPI — exposed so tests can mock the
// HTTP layer by pointing at a test server.
var RegisterAPI = callRegisterAPI

// WriteCredentials exposes writeCredentials for tests.
var WriteCredentials = writeCredentials

// SampleApprovalRequest returns a populated ApprovalRequest for use in tests.
func SampleApprovalRequest(timeout time.Duration) ApprovalRequest {
	return ApprovalRequest{
		JobID:      "test-job-001",
		Script:     "find ~/Documents -name '*.pdf' | head -20",
		RunnerName: "test-runner",
		Timeout:    timeout,
	}
}
