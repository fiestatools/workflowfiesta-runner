// Package localui provides the user-interface layer for local executor mode:
// a system tray icon, an approval popup, and a first-run setup wizard.
//
// When built with the "nolocalui" tag (server / CI deployments), all UI
// functions become no-ops or fall back to terminal prompts and the Fyne
// dependency is not pulled in.
package localui

import "time"

// Headless disables GUI windows and uses terminal prompts instead.
// Set to true before calling any UI functions when running without a display.
var Headless bool

// ApprovalRequest carries the information shown in the approval dialog.
type ApprovalRequest struct {
	JobID      string
	Script     string
	RunnerName string
	Timeout    time.Duration
}
