//go:build nolocalui

package localui

import "fmt"

// RegistrationResult is defined here for nolocalui builds.
type RegistrationResult struct {
	RunnerID   string
	Token      string
	RunnerName string
	APIURL     string
}

// RunRegisterWizard is unavailable in nolocalui builds.
func RunRegisterWizard(configPath string) (*RegistrationResult, error) {
	return nil, fmt.Errorf("register-local wizard requires a GUI build (omit the nolocalui build tag)")
}
