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
// It is kept as a struct so tests can inject decisions without needing a display.
type approvalState struct {
	resultCh chan bool
	once     sync.Once
	allowBtn *widget.Button
	denyBtn  *widget.Button
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
	win.Resize(fyne.NewSize(440, 200))

	scriptText := truncateScript(req.Script, 10)
	scriptLabel := widget.NewLabelWithStyle(scriptText, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	scriptLabel.Wrapping = fyne.TextWrapWord

	fromLabel := widget.NewLabel("From: " + req.RunnerName)
	fromLabel.TextStyle = fyne.TextStyle{Italic: true}

	progress := widget.NewProgressBar()
	progress.Min = 0
	progress.Max = float64(req.Timeout)
	progress.Value = float64(req.Timeout)
	countdownLabel := widget.NewLabel(fmt.Sprintf("Auto-deny in %ds", int(req.Timeout.Seconds())))

	s.denyBtn = widget.NewButton("  Deny  ", func() {
		s.decide(false)
		win.Close()
	})
	s.allowBtn = widget.NewButton("  Allow  ", func() {
		s.decide(true)
		win.Close()
	})

	allowBg := canvas.NewRectangle(approveGreen)
	allowBg.CornerRadius = 4
	allowContainer := container.NewStack(allowBg, s.allowBtn)

	btnRow := container.NewHBox(layout.NewSpacer(), s.denyBtn, allowContainer)
	content := container.NewVBox(
		scriptLabel,
		widget.NewSeparator(),
		fromLabel,
		widget.NewSeparator(),
		countdownLabel,
		progress,
		btnRow,
	)
	win.SetContent(container.NewPadded(content))
	win.SetOnClosed(func() { s.decide(false) })

	// Countdown ticker — stopped via s.stopTick().
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
				countdownLabel.SetText(fmt.Sprintf("Auto-deny in %ds", remaining))
				progress.SetValue(float64(remaining))
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
