//go:build !nolocalui

package localui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// wfTheme wraps Fyne's dark theme and overrides the primary colour to the
// WorkflowFiesta brand orange (#F3610C) so all HighImportance buttons and
// interactive highlights match the logo rather than the default blue.
type wfTheme struct{ base fyne.Theme }

func (t wfTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNamePrimary {
		return color.NRGBA{R: 0xF3, G: 0x61, B: 0x0C, A: 0xFF}
	}
	return t.base.Color(n, v)
}

func (t wfTheme) Font(s fyne.TextStyle) fyne.Resource  { return t.base.Font(s) }
func (t wfTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return t.base.Icon(n) }
func (t wfTheme) Size(n fyne.ThemeSizeName) float32    { return t.base.Size(n) }
