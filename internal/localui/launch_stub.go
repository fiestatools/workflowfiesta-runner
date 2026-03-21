//go:build nolocalui

package localui

import "workflowfiesta-runner/internal/config"

// HasGUI reports whether this build includes the local GUI (Fyne).
// False in headless/server builds; true in the desktop GUI build.
const HasGUI = false

// RunAutoLaunch is unavailable in headless/server builds.
// Use environment variables and the run / run-local subcommands instead.
func RunAutoLaunch(_ string, _ func(*config.Config)) {}
