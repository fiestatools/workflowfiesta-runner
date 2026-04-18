//go:build !nolocalui

package localui

import (
	"fmt"
	"image/color"
	"net/url"
	"os"
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

// HasGUI reports whether this build includes the local GUI (Fyne).
// True in the desktop GUI build; false in headless/server builds.
const HasGUI = true

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
//	Step 1 — Connect & Register
//	Step 2 — Local permissions
//	Step 3 — "Starting…" transition (calls startFn, hides wizard)
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

	// Step 1 of 3: Connect & Register (single-field flow)
	defaultAPIURL := os.Getenv("WORKFLOWFIESTA_API_URL")
	if defaultAPIURL == "" {
		defaultAPIURL = config.DefaultAPIURL
	}

	apiURLEntry := widget.NewEntry()
	apiURLEntry.SetText(defaultAPIURL)
	apiURLEntry.SetPlaceHolder("https://app.workflowfiesta.com")

	codeEntry := widget.NewEntry()
	codeEntry.SetPlaceHolder("RNR-XXXXXXX-XXXXXXX-XXXXXXX-XXXXXXX-XXXXXX")
	codeEntry.TextStyle = fyne.TextStyle{Monospace: true}

	nameEntry := widget.NewEntry()
	nameEntry.SetText(defaultRunnerName())

	getCodeURL, _ := url.Parse(strings.TrimRight(defaultAPIURL, "/") + "/runners/setup")
	getCodeLink := widget.NewHyperlink("Don't have a code? Get one →", getCodeURL)
	apiURLEntry.OnChanged = func(v string) {
		if u, err := url.Parse(strings.TrimRight(strings.TrimSpace(v), "/") + "/runners/setup"); err == nil {
			getCodeLink.SetURL(u)
		}
	}

	regStatusText := canvas.NewText("", colorMuted)
	regStatusText.TextSize = 12

	setRegStatus := func(msg string, col color.Color) {
		regStatusText.Text = msg
		regStatusText.Color = col
		regStatusText.Refresh()
	}

	// Advanced Options (collapsed by default — for self-hosted users + custom names)
	advancedBody := container.NewVBox(
		widget.NewSeparator(),
		makeFieldItem("API URL (only change for self-hosted)", apiURLEntry),
		makeFieldItem("Runner Name (defaults to hostname)", nameEntry),
	)
	advancedBody.Hide()

	advancedBtn := newButton("▸ Advanced", nil)
	advancedBtn.OnTapped = func() {
		if advancedBody.Hidden {
			advancedBody.Show()
			advancedBtn.SetText("▾ Advanced")
		} else {
			advancedBody.Hide()
			advancedBtn.SetText("▸ Advanced")
		}
	}

	var regResult *RegistrationResult

	connectBtn := newButton("Connect & Register", nil)
	connectBtn.Importance = widget.HighImportance
	connectBtn.OnTapped = func() {
		apiURL := strings.TrimRight(strings.TrimSpace(apiURLEntry.Text), "/")
		code := strings.Join(strings.Fields(codeEntry.Text), "")
		name := strings.TrimSpace(nameEntry.Text)
		if apiURL == "" {
			setRegStatus("API URL is required.", colorAmber)
			return
		}
		if code == "" {
			setRegStatus("Registration code is required. Click 'Get one →' if you need one.", colorAmber)
			return
		}
		if name == "" {
			name = defaultRunnerName()
		}
		connectBtn.Disable()
		setRegStatus("Registering…", colorMuted)
		go func() {
			r, err := callRegisterAPI(apiURL, code, name)
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
		makeStepHeading("Step 1: Welcome to WorkflowFiesta", "Paste your one-time registration code. The code embeds your organization, so this is all you need."),
		makeFieldItem("Registration Code", codeEntry),
		container.NewHBox(getCodeLink),
		connectBtn,
		container.NewWithoutLayout(regStatusText),
		advancedBtn,
		advancedBody,
	)

	// Step 2 of 3: Local permissions
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
		makeStepHeading("Step 2: Local Permissions", "Control what scripts can do on this machine."),
		makeSectionLabel("Approval Prompt"),
		confirmRadio,
		makeSectionLabel("Network Access"),
		networkRadio,
	)

	// Step 3 of 3: Launching (transition screen)
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
		if regResult.EnvironmentUID != "" {
			localCfg.EnvironmentID = regResult.EnvironmentUID
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
			RunnerID:     regResult.RunnerUID,
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
