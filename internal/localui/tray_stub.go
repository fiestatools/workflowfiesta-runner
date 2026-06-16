//go:build nolocalui

package localui

import "workflowfiesta-runner/internal/localconfig"

// QuitApp is a no-op in nolocalui builds.
func QuitApp() {}

// SetupTray is a no-op in nolocalui builds.
func SetupTray(runnerName string, onStop func(), onClearConfig func(deleteAuditLogs, deleteScripts bool) error, sw *StatusWindow, cfg *localconfig.LocalConfig, onConfigSaved func(*localconfig.LocalConfig)) {
}

// StartTray is a no-op in nolocalui builds — call this only from run-local,
// which should use --headless when Fyne is not compiled in.
func StartTray(runnerName string, onStop func(), onClearConfig func(deleteAuditLogs, deleteScripts bool) error, sw *StatusWindow, cfg *localconfig.LocalConfig, onConfigSaved func(*localconfig.LocalConfig)) {
}
