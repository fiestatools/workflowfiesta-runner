//go:build !nolocalui

package localui

import (
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// approvalState manages the result channel for a single approval request.
type approvalState struct {
	resultCh        chan ApprovalResult
	once            sync.Once
	allowBtn        *cursorButton
	allowSessionBtn *cursorButton
	alwaysAllowBtn  *cursorButton
	denyBtn         *cursorButton
	stopTick        func()
}

// footerButtons returns action buttons in left-to-right visual order for Tab / arrows.
func (s *approvalState) footerButtons() []*cursorButton {
	return []*cursorButton{s.denyBtn, s.allowSessionBtn, s.alwaysAllowBtn, s.allowBtn}
}

func newApprovalState() *approvalState {
	return &approvalState{
		resultCh: make(chan ApprovalResult, 1),
		stopTick: func() {},
	}
}

func (s *approvalState) decide(result ApprovalResult) {
	s.once.Do(func() { s.resultCh <- result })
}

// buildWindow constructs the approval Fyne window, wires buttons and countdown.
func (s *approvalState) buildWindow(req ApprovalRequest, a fyne.App) fyne.Window {
	win := a.NewWindow("WorkflowFiesta · Job Request")
	win.SetFixedSize(false)

	prefs := a.Preferences()
	win.Resize(loadWindowSize(prefs, approvalWindowSizeSpec))

	// ── header ──────────────────────────────────────────────────────────────
	iconBg := canvas.NewRectangle(colorAmberDim)
	iconBg.CornerRadius = 4
	iconBg.StrokeColor = colorAmber
	iconBg.StrokeWidth = 1
	iconGlyph := canvas.NewText("⚠", colorAmber)
	iconGlyph.TextSize = 16
	iconCell := container.NewStack(
		container.New(layout.NewGridWrapLayout(fyne.NewSize(32, 32)), iconBg),
		container.NewCenter(iconGlyph),
	)

	titleText := canvas.NewText("Job Approval Required", colorText)
	titleText.TextSize = 13
	titleText.TextStyle = fyne.TextStyle{Bold: true}
	subText := canvas.NewText("Review the script before allowing execution", colorMuted)
	subText.TextSize = 11

	headerRow := container.NewHBox(
		iconCell,
		container.NewVBox(
			container.NewWithoutLayout(titleText),
			container.NewWithoutLayout(subText),
		),
	)
	headerBg := canvas.NewRectangle(colorCard)
	headerBg.StrokeColor = colorBorder
	headerBg.StrokeWidth = 1
	header := container.NewStack(headerBg, container.NewPadded(headerRow))

	// ── body ────────────────────────────────────────────────────────────────
	fromText := canvas.NewText("From workflow on  "+req.RunnerName, colorMuted)
	fromText.TextSize = 11

	scriptText := truncateScript(req.Script, 10)
	scriptLabel := widget.NewLabelWithStyle(scriptText, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	scriptLabel.Wrapping = fyne.TextWrapWord
	scriptBg := canvas.NewRectangle(colorTermBg)
	scriptBg.CornerRadius = 4
	scriptBg.StrokeColor = colorBorder
	scriptBg.StrokeWidth = 1
	scriptScroll := container.NewVScroll(scriptLabel)
	scriptScroll.SetMinSize(fyne.NewSize(420, 90))
	scriptBlock := container.NewStack(scriptBg, container.NewPadded(scriptScroll))

	neverTimeout := req.Timeout <= 0
	var countdownLabel *canvas.Text
	var progress *widget.ProgressBar
	if neverTimeout {
		countdownLabel = canvas.NewText("Waiting for your approval...", colorMuted)
	} else {
		countdownLabel = canvas.NewText(fmt.Sprintf("Auto-deny in  %ds", int(req.Timeout.Seconds())), colorMuted)
	}
	countdownLabel.TextSize = 11

	progress = widget.NewProgressBar()
	progress.Min = 0
	progress.Max = float64(req.Timeout)
	progress.Value = float64(req.Timeout)
	if neverTimeout {
		progress.Hide()
	}

	body := container.NewPadded(container.NewVBox(
		container.NewWithoutLayout(fromText),
		widget.NewSeparator(),
		scriptBlock,
		container.NewWithoutLayout(countdownLabel),
		progress,
	))

	// ── footer buttons ───────────────────────────────────────────────────────
	s.denyBtn = newButton("✕  Deny", func() {
		s.decide(ApprovalDeny)
	})

	s.allowBtn = newButton("✓  Allow", func() {
		s.decide(ApprovalAllow)
	})
	s.allowBtn.Importance = widget.SuccessImportance

	s.allowSessionBtn = newButton("Allow for session", func() {
		s.decide(ApprovalAllowSession)
	})
	s.allowSessionBtn.Importance = widget.LowImportance

	s.alwaysAllowBtn = newButton("Always allow", func() {
		s.decide(ApprovalAlwaysAllow)
	})
	s.alwaysAllowBtn.Importance = widget.LowImportance

	footerBg := canvas.NewRectangle(colorCard)
	footerBg.StrokeColor = colorBorder
	footerBg.StrokeWidth = 1
	footerRow := container.NewHBox(layout.NewSpacer(), s.denyBtn, s.allowSessionBtn, s.alwaysAllowBtn, s.allowBtn)
	footer := container.NewStack(footerBg, container.NewPadded(footerRow))

	win.SetContent(container.NewVBox(header, body, footer))
	win.SetOnClosed(func() { s.decide(ApprovalDeny) })

	// Keyboard: Fyne never dispatches canvas CustomShortcut with modifier 0, and
	// widget.Button only activates on Space. Wire Escape / arrows on footer buttons.
	c := win.Canvas()
	escape := func() { fyne.Do(func() { s.decide(ApprovalDeny) }) }
	arrow := func(delta int) { s.moveFooterFocus(c, delta) }
	for _, btn := range []*cursorButton{s.denyBtn, s.allowSessionBtn, s.alwaysAllowBtn, s.allowBtn} {
		btn.OnEscape = escape
		btn.OnArrow = arrow
	}

	// ── countdown ticker ─────────────────────────────────────────────────────
	stopped := make(chan struct{})
	s.stopTick = sync.OnceFunc(func() { close(stopped) })

	if !neverTimeout {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			remaining := int(req.Timeout.Seconds())
			for {
				select {
				case <-stopped:
					return
				case <-ticker.C:
					remaining--
					if remaining <= 0 {
						s.decide(ApprovalDeny)
						win.Close()
						return
					}
					fyne.Do(func() {
						countdownLabel.Text = fmt.Sprintf("Auto-deny in  %ds", remaining)
						countdownLabel.Refresh()
						progress.SetValue(float64(remaining))
					})
				}
			}
		}()
	}

	// ── size persistence — only save after 3 consecutive stable readings to avoid
	// spurious saves from content reflows mid-job ────────────────────────────
	var resizeSaveW, resizeSaveH float32
	var resizeStable int
	go func() {
		sizeTicker := time.NewTicker(time.Second)
		defer sizeTicker.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-sizeTicker.C:
				fyne.Do(func() {
					logical := logicalWindowSize(win)
					if logical.Width == resizeSaveW && logical.Height == resizeSaveH {
						resizeStable++
						if resizeStable == 3 {
							w, h := clampWindowSize(logical.Width, logical.Height, approvalWindowSizeSpec)
							prefs.SetFloat(approvalWindowSizeSpec.prefW, float64(w))
							prefs.SetFloat(approvalWindowSizeSpec.prefH, float64(h))
						}
					} else {
						resizeSaveW, resizeSaveH = logical.Width, logical.Height
						resizeStable = 0
					}
				})
			}
		}
	}()

	return win
}

// moveFooterFocus moves keyboard focus among the four footer action buttons.
func (s *approvalState) moveFooterFocus(c fyne.Canvas, delta int) {
	buttons := s.footerButtons()
	n := len(buttons)
	if n == 0 {
		return
	}
	fyne.Do(func() {
		idx := 0
		if f := c.Focused(); f != nil {
			for i, b := range buttons {
				if f == b {
					idx = i
					break
				}
			}
		}
		idx = ((idx+delta)%n + n) % n
		c.Focus(buttons[idx])
	})
}

// RequestApproval shows an approval dialog and blocks until the user decides or timeout.
func RequestApproval(req ApprovalRequest) ApprovalResult {
	if Headless {
		return headlessApproval(req)
	}
	return fyneApproval(req)
}

func fyneApproval(req ApprovalRequest) ApprovalResult {
	s := newApprovalState()
	win := s.buildWindow(req, getApp())
	restoreApprovalPosition(win)
	win.Show()
	win.RequestFocus()
	fyne.Do(func() {
		win.Canvas().Focus(s.allowBtn)
	})

	if req.PlaySound {
		playApprovalSound()
	}
	if req.Callbacks.OnPending != nil {
		req.Callbacks.OnPending()
	}

	result := <-s.resultCh
	s.stopTick()

	if req.Callbacks.OnResolved != nil {
		req.Callbacks.OnResolved(result != ApprovalDeny)
	}

	win.Close()
	return result
}

// restoreApprovalPosition restores the approval window position from Fyne preferences,
// centering on screen if no saved position is available.
func restoreApprovalPosition(w fyne.Window) {
	// Fyne's Window interface does not expose a Move() method; position is set
	// by the OS window manager. We center as the best cross-platform default.
	// Users configure preferred screen quadrant via the Settings window which
	// stores approval.window.x/y for use when Fyne exposes positioning.
	w.CenterOnScreen()
}
