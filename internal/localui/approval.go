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
	resultCh chan bool
	once     sync.Once
	allowBtn *cursorButton
	denyBtn  *cursorButton
	stopTick func()
}

func newApprovalState() *approvalState {
	return &approvalState{
		resultCh: make(chan bool, 1),
		stopTick: func() {},
	}
}

func (s *approvalState) decide(approved bool) {
	s.once.Do(func() { s.resultCh <- approved })
}

// buildWindow constructs the approval Fyne window, wires buttons and countdown.
func (s *approvalState) buildWindow(req ApprovalRequest, a fyne.App) fyne.Window {
	win := a.NewWindow("WorkflowFiesta · Job Request")
	win.SetFixedSize(true)
	win.Resize(fyne.NewSize(460, 280))

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

	countdownLabel := canvas.NewText(fmt.Sprintf("Auto-deny in  %ds", int(req.Timeout.Seconds())), colorMuted)
	countdownLabel.TextSize = 11

	progress := widget.NewProgressBar()
	progress.Min = 0
	progress.Max = float64(req.Timeout)
	progress.Value = float64(req.Timeout)

	body := container.NewPadded(container.NewVBox(
		container.NewWithoutLayout(fromText),
		widget.NewSeparator(),
		scriptBlock,
		container.NewWithoutLayout(countdownLabel),
		progress,
	))

	// ── footer buttons ───────────────────────────────────────────────────────
	s.denyBtn = newButton("✕  Deny", func() {
		s.decide(false)
	})

	s.allowBtn = newButton("✓  Allow", func() {
		s.decide(true)
	})
	s.allowBtn.Importance = widget.SuccessImportance

	footerBg := canvas.NewRectangle(colorCard)
	footerBg.StrokeColor = colorBorder
	footerBg.StrokeWidth = 1
	footerRow := container.NewHBox(layout.NewSpacer(), s.denyBtn, s.allowBtn)
	footer := container.NewStack(footerBg, container.NewPadded(footerRow))

	win.SetContent(container.NewVBox(header, body, footer))
	win.SetOnClosed(func() { s.decide(false) })

	// ── countdown ticker ─────────────────────────────────────────────────────
	stopped := make(chan struct{})
	s.stopTick = sync.OnceFunc(func() { close(stopped) })

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
					s.decide(false)
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

	return win
}

// RequestApproval shows an approval dialog and blocks until the user decides or timeout.
func RequestApproval(req ApprovalRequest) bool {
	if Headless {
		return headlessApproval(req)
	}
	return fyneApproval(req)
}

func fyneApproval(req ApprovalRequest) bool {
	s := newApprovalState()
	win := s.buildWindow(req, getApp())
	positionBottomRight(win)
	win.Show()
	result := <-s.resultCh
	s.stopTick()
	win.Close()
	return result
}

// positionBottomRight is a no-op; fyne.Window does not expose Move in v2.5.
func positionBottomRight(_ fyne.Window) {}
