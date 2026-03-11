//go:build nolocalui

package localui

// RunAutoLaunch is unavailable in headless/server builds.
// Use environment variables and the run / run-local subcommands instead.
func RunAutoLaunch(_ string) {}
