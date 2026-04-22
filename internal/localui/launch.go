//go:build !nolocalui

package localui

import (
	"fmt"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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

// showFirstRunWizard opens three numbered setup steps, then a non-numbered
// "Starting…" transition (calls startFn, hides wizard).
//
//	Screen 0 — Connect & Register          → Step 1 of 3
//	Screen 1 — Approval prompt & network  → Step 2 of 3
//	Screen 2 — Timeouts, sound, paths     → Step 3 of 3
//	Screen 3 — Starting… (not counted)
//
// Uses win.Show() — NOT ShowAndRun — so the caller owns the event loop.
func showFirstRunWizard(a fyne.App, configPath string, startFn func(*config.Config)) {
	win := a.NewWindow("WorkflowFiesta · Setup")
	win.Resize(fyne.NewSize(520, 560))
	win.CenterOnScreen()
	win.SetCloseIntercept(func() { win.Hide(); QuitApp() })

	const numSetupSteps = 3
	var currentStep int

	// ── progress dots (one per setup step; all filled on transition) ──────────
	dots := make([]*canvas.Rectangle, numSetupSteps)
	for i := range dots {
		r := canvas.NewRectangle(colorBorder)
		r.CornerRadius = 4
		dots[i] = r
	}
	dotCells := make([]fyne.CanvasObject, numSetupSteps)
	for i, d := range dots {
		dotCells[i] = container.New(layout.NewGridWrapLayout(fyne.NewSize(8, 8)), d)
	}
	dotsRow := container.NewHBox(append([]fyne.CanvasObject{}, dotCells...)...)

	stepCountLabel := canvas.NewText("Step 1 of 3", colorLabel)
	stepCountLabel.TextSize = 11

	bodyHolder := container.NewStack()

	// screenIndex 0–2 = setup; 3 = transition (all dots complete).
	refreshDots := func(screenIndex int) {
		for i, d := range dots {
			var fill color.Color
			var wide bool
			switch {
			case screenIndex >= numSetupSteps:
				fill = colorSuccess
				wide = false
			case i < screenIndex:
				fill = colorSuccess
				wide = false
			case i == screenIndex:
				fill = color.NRGBA{R: 59, G: 130, B: 246, A: 255}
				wide = true
			default:
				fill = colorBorder
				wide = false
			}
			d.FillColor = fill
			d.Refresh()
			sz := fyne.NewSize(8, 8)
			if wide {
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

	// The runner's name was chosen at code-issuance time on the platform's
	// /runners/setup page, so the binary doesn't need to ask for one.
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

	// Advanced Options (collapsed by default — for self-hosted users)
	advancedBody := container.NewVBox(
		widget.NewSeparator(),
		makeFieldItem("API URL (only change for self-hosted)", apiURLEntry),
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
		if apiURL == "" {
			setRegStatus("API URL is required.", colorAmber)
			return
		}
		if code == "" {
			setRegStatus("Registration code is required. Click 'Get one →' if you need one.", colorAmber)
			return
		}
		connectBtn.Disable()
		setRegStatus("Registering…", colorMuted)
		go func() {
			r, err := callRegisterAPI(apiURL, code)
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

	cfgDefaults := localconfig.Default()

	// Step 2: Approval & network
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

	// Step 3: Timeouts, sound, allowed paths (matches Settings + register-local)
	confirmTimeoutEntry := widget.NewEntry()
	confirmTimeoutEntry.SetText(strconv.Itoa(cfgDefaults.ConfirmTimeout))
	confirmTimeoutHint := canvas.NewText(
		"How long the approval dialog waits for you before the job is auto-denied.",
		colorMuted,
	)
	confirmTimeoutHint.TextSize = 10

	maxTimeoutEntry := widget.NewEntry()
	maxTimeoutEntry.SetText(strconv.Itoa(cfgDefaults.MaxTimeout))
	maxTimeoutHint := canvas.NewText(
		"Upper limit on how long any single job may run before it is stopped.",
		colorMuted,
	)
	maxTimeoutHint.TextSize = 10

	soundCheck := widget.NewCheck("Play sound on approval request", nil)
	soundCheck.SetChecked(cfgDefaults.SoundOnApproval)

	pathsEntry := widget.NewMultiLineEntry()
	pathsEntry.SetPlaceHolder("One path per line, e.g.\n~/projects\n~/Documents:ro")
	pathsEntry.SetText(strings.Join(cfgDefaults.AllowedPaths, "\n"))
	pathsEntry.SetMinRowsVisible(3)
	browseBtn := newButton("Browse…", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			current := pathsEntry.Text
			if current != "" && current[len(current)-1] != '\n' {
				current += "\n"
			}
			pathsEntry.SetText(current + uri.Path())
		}, win)
	})
	pathHint := canvas.NewText("Append :ro for read-only access.", colorMuted)
	pathHint.TextSize = 10

	step2 := container.NewVBox(
		makeStepHeading("Safety & folders", "Time limits for approvals and jobs, plus which directories scripts may use."),
		makeSectionRow("Allowed paths", win,
			"Allowed paths",
			"Directories this runner may read and write. Jobs that touch paths outside this list can be blocked or require approval.\n\nAppend :ro for read-only (e.g. ~/Documents:ro)."),
		pathsEntry,
		container.NewHBox(browseBtn, container.NewWithoutLayout(pathHint)),
		widget.NewSeparator(),
		makeLabeledEntryInfo("Confirm timeout (seconds)", confirmTimeoutEntry, confirmTimeoutHint, win,
			"Confirm timeout",
			"Number of seconds the approval dialog stays open waiting for you.\n\nIf you do not choose Allow or Deny in time, the job is treated as denied. Default: 120 seconds."),
		makeLabeledEntryInfo("Max timeout (seconds)", maxTimeoutEntry, maxTimeoutHint, win,
			"Max timeout",
			"Maximum seconds a job is allowed to run, including script execution. The runner stops the job if it runs longer than this cap. Default: 180 seconds."),
		container.NewHBox(soundCheck, makeInfoBtn(win,
			"Sound on approval",
			"Plays a system notification sound when an approval dialog appears.\n\nUseful when the runner window is in the background.")),
	)

	// Transition: launching (not a numbered step)
	launchTitle := canvas.NewText("Runner is starting…", colorText)
	launchTitle.TextSize = 14
	launchTitle.TextStyle = fyne.TextStyle{Bold: true}
	launchSub := canvas.NewText("The status window will appear shortly.", colorMuted)
	launchSub.TextSize = 12

	step3 := container.NewCenter(container.NewVBox(
		container.NewWithoutLayout(launchTitle),
		container.NewWithoutLayout(launchSub),
	))

	steps := []fyne.CanvasObject{step0, step1, step2, step3}

	setStep := func(n int) {
		currentStep = n
		if n < numSetupSteps {
			stepCountLabel.Text = fmt.Sprintf("Step %d of %d", n+1, numSetupSteps)
		} else {
			stepCountLabel.Text = ""
		}
		stepCountLabel.Refresh()
		refreshDots(n)
		bodyHolder.Objects = []fyne.CanvasObject{steps[n]}
		bodyHolder.Refresh()
		backBtn.Hidden = (n == 0 || n == 3)
		nextBtn.Hidden = (n != 0 && n != 1)
		saveBtn.Hidden = (n != 2)
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

		if v, err := strconv.Atoi(strings.TrimSpace(confirmTimeoutEntry.Text)); err == nil && v > 0 {
			localCfg.ConfirmTimeout = v
		}
		if v, err := strconv.Atoi(strings.TrimSpace(maxTimeoutEntry.Text)); err == nil && v > 0 {
			localCfg.MaxTimeout = v
		}
		localCfg.SoundOnApproval = soundCheck.Checked
		var paths []string
		for _, line := range strings.Split(pathsEntry.Text, "\n") {
			if strings.TrimSpace(line) != "" {
				paths = append(paths, strings.TrimSpace(line))
			}
		}
		if len(paths) > 0 {
			localCfg.AllowedPaths = paths
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
		setStep(3)

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
		container.NewVScroll(container.NewPadded(bodyHolder)),
	))

	setStep(0)
	win.Show()
}
