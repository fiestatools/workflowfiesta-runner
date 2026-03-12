//go:build !nolocalui

package localui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed assets/icon.svg
var logoSVG []byte

// LogoResource returns the WorkflowFiesta logo as a Fyne static resource.
// Fyne renders SVG resources natively at any size.
func LogoResource() fyne.Resource {
	return fyne.NewStaticResource("workflowfiesta-icon.svg", logoSVG)
}
