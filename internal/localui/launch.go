//go:build !nolocalui

package localui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/localconfig"
)

// RunAutoLaunch is the zero-CLI entry point for double-click launches.
//
// startFn is called (on the Fyne goroutine) once the runner should start. It
// receives the fully-populated Config and should: open a StatusWindow, wire up
// signal handling, start the runner goroutine, and call SetupTray — all
// non-blocking. RunAutoLaunch then calls a.Run() which blocks until QuitApp().
//
// Keeping startFn as a callback avoids an import cycle between localui and
// the runner package (executor/local.go imports localui for approval prompts).
func RunAutoLaunch(configPath string, startFn func(*config.Config)) {
	cfg := config.Load() // auto-loads ~/.workflowfiesta/credentials.env if no env vars

	a := getApp()

	if cfg.Token != "" {
		// Already registered: load local config and start immediately.
		localCfg, err := localconfig.Load(configPath)
		if err != nil {
			localCfg = localconfig.Default()
		}
		cfg.ExecutorType = "local"
		cfg.LocalConfig = localCfg
		startFn(cfg) // opens windows + tray before a.Run() — that's fine
	} else {
		// First run: show setup wizard; startFn is called on save.
		showFirstRunWizard(a, configPath, startFn)
	}

	a.Run() // blocks; kept alive by status window / tray
}

// showFirstRunWizard opens a 3-step wizard:
//
//	Step 0 — Connect & Register
//	Step 1 — Local permissions
//	Step 2 — "Starting…" transition (calls startFn, hides wizard)
//
// Uses win.Show() — NOT ShowAndRun — so the caller owns the event loop.
func showFirstRunWizard(a fyne.App, configPath string, startFn func(*config.Config)) {
	win := a.NewWindow("WorkflowFiesta · Setup")
	win.Resize(fyne.NewSize(520, 460))
	win.CenterOnScreen()
	win.SetCloseIntercept(func() { win.Hide(); QuitApp() })

	const numSteps = 3
	var currentStep int

	// ── progress dots ─────────────────────────────────────────────────────────
	dots := make([]*canvas.Rectangle, numSteps)
	for i := range dots {
		r := canvas.NewRectangle(colorBorder)
		r.CornerRadius = 4
		dots[i] = r
	}
	dotCells := make([]fyne.CanvasObject, numSteps)
	for i, d := range dots {
		dotCells[i] = container.New(layout.NewGridWrapLayout(fyne.NewSize(8, 8)), d)
	}
	dotsRow := container.NewHBox(append([]fyne.CanvasObject{}, dotCells...)...)

	stepCountLabel := canvas.NewText("Step 1 of 3", colorLabel)
	stepCountLabel.TextSize = 11

	bodyHolder := container.NewStack()

	refreshDots := func(n int) {
		for i, d := range dots {
			switch {
			case i < n:
				d.FillColor = colorSuccess
			case i == n:
				d.FillColor = color.NRGBA{R: 59, G: 130, B: 246, A: 255}
			default:
				d.FillColor = colorBorder
			}
			d.Refresh()
			sz := fyne.NewSize(8, 8)
			if i == n {
				sz = fyne.NewSize(24, 8)
			}
			dotCells[i] = container.New(layout.NewGridWrapLayout(sz), d)
		}
		objs := make([]fyne.CanvasObject, len(dotCells))
		copy(objs, dotCells)
		dotsRow.Objects = objs
		dotsRow.Refresh()
	}

	// ── nav buttons ───────────────────────────────────────────────────────────
	backBtn := newButton("← Back", nil)
	nextBtn := newButton("Next →", nil)
	saveBtn := newButton("Save & Start", nil)
	saveBtn.Importance = widget.HighImportance
	saveBtn.Hide()
	nextBtn.Hide() // hidden until registration succeeds

	navRow := container.NewHBox(
		container.NewWithoutLayout(stepCountLabel),
		layout.NewSpacer(),
		backBtn, nextBtn, saveBtn,
	)

	// ── step bodies ───────────────────────────────────────────────────────────

	// Step 0: Connect & Register
	apiURLEntry := widget.NewEntry()
	apiURLEntry.SetText("http://localhost:3001")
	apiURLEntry.SetPlaceHolder("https://your-instance.workflowfiesta.com")

	orgIDEntry := widget.NewEntry()
	orgIDEntry.SetPlaceHolder("your-organization-id")

	nameEntry := widget.NewEntry()
	nameEntry.SetText(defaultRunnerName())

	regStatusText := canvas.NewText("", colorMuted)
	regStatusText.TextSize = 12

	setRegStatus := func(msg string, col color.Color) {
		regStatusText.Text = msg
		regStatusText.Color = col
		regStatusText.Refresh()
	}

	// Advanced Options (hidden by default)
	envIDEntry := widget.NewEntry()
	envIDEntry.SetPlaceHolder("leave blank to auto-create")
	advancedBody := container.NewVBox(
		widget.NewSeparator(),
		makeFieldItem("Environment ID (optional)", envIDEntry),
	)
	advancedBody.Hide()

	advancedBtn := newButton("▸ Advanced Options", nil)
	advancedBtn.OnTapped = func() {
		if advancedBody.Hidden {
			advancedBody.Show()
			advancedBtn.SetText("▾ Advanced Options")
		} else {
			advancedBody.Hide()
			advancedBtn.SetText("▸ Advanced Options")
		}
	}

	var regResult *RegistrationResult

	connectBtn := newButton("Connect & Register", nil)
	connectBtn.Importance = widget.HighImportance
	connectBtn.OnTapped = func() {
		apiURL := strings.TrimRight(strings.TrimSpace(apiURLEntry.Text), "/")
		orgID := strings.TrimSpace(orgIDEntry.Text)
		name := strings.TrimSpace(nameEntry.Text)
		if apiURL == "" || orgID == "" || name == "" {
			setRegStatus("All fields are required.", colorAmber)
			return
		}
		connectBtn.Disable()
		setRegStatus("Connecting…", colorMuted)
		envID := strings.TrimSpace(envIDEntry.Text)
		go func() {
			r, err := callRegisterAPI(apiURL, name, orgID, envID)
			if err != nil {
				fyne.Do(func() {
					connectBtn.Enable()
					setRegStatus(err.Error(), colorDanger)
				})
				return
			}
			regResult = r
			fyne.Do(func() {
				setRegStatus("Registered! Token saved automatically.", colorSuccess)
				nextBtn.Importance = widget.HighImportance
				nextBtn.Show()
				nextBtn.Refresh()
			})
		}()
	}

	step0 := container.NewVBox(
		makeStepHeading("Welcome to WorkflowFiesta", "Connect this machine as a self-hosted runner."),
		makeFieldItem("API URL", apiURLEntry),
		makeFieldItem("Organization ID", orgIDEntry),
		makeFieldItem("Runner Name", nameEntry),
		connectBtn,
		container.NewWithoutLayout(regStatusText),
		advancedBtn,
		advancedBody,
	)

	// Step 1: Local permissions
	confirmRadio := widget.NewRadioGroup(
		[]string{"Always (every job)", "Risky operations only (recommended)", "Never"},
		nil,
	)
	confirmRadio.SetSelected("Risky operations only (recommended)")

	networkRadio := widget.NewRadioGroup(
		[]string{"Allow all (recommended)", "Local only", "Block all"},
		nil,
	)
	networkRadio.SetSelected("Allow all (recommended)")

	step1 := container.NewVBox(
		makeStepHeading("Local Permissions", "Control what scripts can do on this machine."),
		makeSectionLabel("Approval Prompt"),
		confirmRadio,
		makeSectionLabel("Network Access"),
		networkRadio,
	)

	// Step 2: Launching (transition screen)
	launchTitle := canvas.NewText("Runner is starting…", colorText)
	launchTitle.TextSize = 14
	launchTitle.TextStyle = fyne.TextStyle{Bold: true}
	launchSub := canvas.NewText("The status window will appear shortly.", colorMuted)
	launchSub.TextSize = 12

	step2 := container.NewCenter(container.NewVBox(
		container.NewWithoutLayout(launchTitle),
		container.NewWithoutLayout(launchSub),
	))

	steps := []fyne.CanvasObject{step0, step1, step2}

	setStep := func(n int) {
		currentStep = n
		stepCountLabel.Text = fmt.Sprintf("Step %d of 3", n+1)
		stepCountLabel.Refresh()
		refreshDots(n)
		bodyHolder.Objects = []fyne.CanvasObject{steps[n]}
		bodyHolder.Refresh()
		backBtn.Hidden = (n == 0 || n == 2)
		nextBtn.Hidden = (n != 0)
		saveBtn.Hidden = (n != 1)
		backBtn.Refresh()
		nextBtn.Refresh()
		saveBtn.Refresh()
	}

	backBtn.OnTapped = func() { setStep(currentStep - 1) }
	nextBtn.OnTapped = func() { setStep(currentStep + 1) }

	saveBtn.OnTapped = func() {
		if regResult == nil {
			dialog.ShowError(fmt.Errorf("complete Step 1 first"), win)
			return
		}

		localCfg := localconfig.Default()
		switch confirmRadio.Selected {
		case "Always (every job)":
			localCfg.Confirm = "always"
		case "Never":
			localCfg.Confirm = "never"
		default:
			localCfg.Confirm = "destructive"
		}
		switch networkRadio.Selected {
		case "Local only":
			localCfg.Network = "localhost"
		case "Block all":
			localCfg.Network = "none"
		default:
			localCfg.Network = "all"
		}
		if regResult.EnvironmentID != "" {
			localCfg.EnvironmentID = regResult.EnvironmentID
		}

		if err := localconfig.Save(localCfg, configPath); err != nil {
			dialog.ShowError(fmt.Errorf("save config: %w", err), win)
			return
		}
		credPath := filepath.Join(filepath.Dir(configPath), "credentials.env")
		if err := writeCredentials(credPath, regResult); err != nil {
			dialog.ShowError(fmt.Errorf("save credentials: %w", err), win)
			return
		}

		// Show transition screen, start runner, hide wizard.
		setStep(2)

		cfg := &config.Config{
			APIURL:       regResult.APIURL,
			Token:        regResult.Token,
			RunnerID:     regResult.RunnerID,
			Name:         regResult.RunnerName,
			ExecutorType: "local",
			LocalConfig:  localCfg,
		}
		startFn(cfg) // opens status window + tray (non-blocking)
		win.Hide()
	}

	// ── layout ────────────────────────────────────────────────────────────────
	headerBg := canvas.NewRectangle(colorCard)
	headerBg.StrokeColor = colorBorder
	headerBg.StrokeWidth = 1
	dotsHeader := container.NewStack(headerBg, container.NewPadded(
		container.NewHBox(dotsRow, layout.NewSpacer(), container.NewWithoutLayout(stepCountLabel)),
	))

	navBg := canvas.NewRectangle(colorCard)
	navBg.StrokeColor = colorBorder
	navBg.StrokeWidth = 1
	navArea := container.NewStack(navBg, container.NewPadded(navRow))

	win.SetContent(container.NewBorder(dotsHeader, navArea, nil, nil,
		container.NewPadded(bodyHolder),
	))

	setStep(0)
	win.Show()
}
