//go:build nolocalui

package localui

import "fmt"

// RunWizard is unavailable in nolocalui builds.
func RunWizard(configPath string) error {
	return fmt.Errorf("init-local wizard requires a GUI build (omit the nolocalui build tag)")
}
