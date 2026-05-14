//go:build !nolocalui

package localui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// cursorButton is a widget.Button that shows a pointer cursor on hover.
type cursorButton struct {
	widget.Button
	// OnEscape is optional; when set and this button is focused, Escape invokes it.
	OnEscape func()
	// OnArrow is optional; when set, Left/Up pass -1 and Right/Down pass +1.
	OnArrow func(delta int)
}

func (b *cursorButton) Cursor() desktop.Cursor { return desktop.PointerCursor }

// TypedKey extends the stock button (Space only) with Enter/Return and optional Escape/arrows.
func (b *cursorButton) TypedKey(ev *fyne.KeyEvent) {
	switch ev.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		if !b.Disabled() {
			b.Tapped(nil)
		}
		return
	case fyne.KeyEscape:
		if b.OnEscape != nil {
			b.OnEscape()
			return
		}
	case fyne.KeyLeft, fyne.KeyUp:
		if b.OnArrow != nil {
			b.OnArrow(-1)
			return
		}
	case fyne.KeyRight, fyne.KeyDown:
		if b.OnArrow != nil {
			b.OnArrow(1)
			return
		}
	}
	b.Button.TypedKey(ev)
}

// newButton creates a cursorButton (drop-in for widget.NewButton).
func newButton(label string, tapped func()) *cursorButton {
	btn := &cursorButton{}
	btn.Text = label
	btn.OnTapped = tapped
	btn.ExtendBaseWidget(btn)
	return btn
}
