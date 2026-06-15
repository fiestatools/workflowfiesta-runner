//go:build nolocalui

package localui

import "workflowfiesta-runner/internal/localconfig"

// OpenSettingsWindow is a no-op stub in nolocalui builds.
func OpenSettingsWindow(cfg *localconfig.LocalConfig, configPath string, onSave func(*localconfig.LocalConfig), onResetRunner func(deleteAuditLogs, deleteScripts bool) error) interface{} {
	return nil
}
