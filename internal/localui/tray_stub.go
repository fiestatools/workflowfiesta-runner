//go:build nolocalui

package localui

// QuitApp is a no-op in nolocalui builds.
func QuitApp() {}

// StartTray is a no-op in nolocalui builds — call this only from run-local,
// which should use --headless when Fyne is not compiled in.
func StartTray(runnerName string, onStop func(), sw *StatusWindow) {}
