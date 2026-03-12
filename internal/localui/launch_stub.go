//go:build nolocalui

package localui

import "workflowfiesta-runner/internal/config"

// RunAutoLaunch is unavailable in headless/server builds.
// Use environment variables and the run / run-local subcommands instead.
func RunAutoLaunch(_ string, _ func(*config.Config)) {}
