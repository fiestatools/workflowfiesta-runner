//go:build !nolocalui

package localui

import (
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// cursorButton is a widget.Button that shows a pointer cursor on hover.
type cursorButton struct {
	widget.Button
}

func (b *cursorButton) Cursor() desktop.Cursor { return desktop.PointerCursor }

// newButton creates a cursorButton (drop-in for widget.NewButton).
func newButton(label string, tapped func()) *cursorButton {
	btn := &cursorButton{}
	btn.Text = label
	btn.OnTapped = tapped
	btn.ExtendBaseWidget(btn)
	return btn
}
