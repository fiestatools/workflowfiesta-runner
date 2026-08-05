//go:build !nolocalui

package localui

import (
	"context"
	"fmt"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/installer"
	"workflowfiesta-runner/internal/interpreter"
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
// non-blocking. It returns the status window so the setup wizard can observe
// connection progress. RunAutoLaunch then calls a.Run() which blocks until QuitApp().
//
// Keeping startFn as a callback avoids an import cycle between localui and
// the runner package (executor/local.go imports localui for approval prompts).
func RunAutoLaunch(configPath string, startFn func(*config.Config) *StatusWindow) {
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
		_ = startFn(cfg) // opens windows + tray before a.Run() — that's fine
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
func showFirstRunWizard(a fyne.App, configPath string, startFn func(*config.Config) *StatusWindow) {
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
	getCodeURL, _ := url.Parse(frontendURLFrom(defaultAPIURL) + "/runners/setup")
	getCodeLink := widget.NewHyperlink("Don't have a code? Get one →", getCodeURL)
	apiURLEntry.OnChanged = func(v string) {
		if u, err := url.Parse(frontendURLFrom(strings.TrimSpace(v)) + "/runners/setup"); err == nil {
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
				// Reveal Next (setStep is declared below; keep nav in sync with regResult)
				nextBtn.Hidden = false
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

	neverTimeoutCheck := widget.NewCheck("Never timeout (wait indefinitely)", func(checked bool) {
		if checked {
			confirmTimeoutEntry.Disable()
		} else {
			confirmTimeoutEntry.Enable()
		}
	})
	neverTimeoutCheck.SetChecked(cfgDefaults.ConfirmNeverTimeout)
	if cfgDefaults.ConfirmNeverTimeout {
		confirmTimeoutEntry.Disable()
	}

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
		neverTimeoutCheck,
		makeLabeledEntryInfo("Max timeout (seconds)", maxTimeoutEntry, maxTimeoutHint, win,
			"Max timeout",
			"Maximum seconds a job is allowed to run, including script execution. The runner stops the job if it runs longer than this cap. Default: 180 seconds."),
		container.NewHBox(soundCheck, makeInfoBtn(win,
			"Sound on approval",
			"Plays a system notification sound when an approval dialog appears.\n\nUseful when the runner window is in the background.")),
	)

	// Transition: launching (not a numbered step) — shows live connection status.
	launchTitle := canvas.NewText("Checking environment…", colorText)
	launchTitle.TextSize = 14
	launchTitle.TextStyle = fyne.TextStyle{Bold: true}
	launchSub := canvas.NewText("Looking for Python and other required tools.", colorMuted)
	launchSub.TextSize = 12
	launchConnDot := canvas.NewCircle(color.NRGBA{R: 59, G: 130, B: 246, A: 255})
	launchConnDot.StrokeWidth = 0
	launchConnStatus := canvas.NewText("Waiting for connection…", colorMuted)
	launchConnStatus.TextSize = 12
	launchConnRow := container.NewHBox(
		container.New(layout.NewGridWrapLayout(fyne.NewSize(8, 8)), launchConnDot),
		container.NewWithoutLayout(launchConnStatus),
	)
	launchConnRow.Hide()

	// Python install log — shown during auto-install, hidden otherwise.
	var launchInstallLogText string
	launchInstallLogLabel := widget.NewLabel("")
	launchInstallLogLabel.Wrapping = fyne.TextWrapWord
	launchInstallLogLabel.TextStyle = fyne.TextStyle{Monospace: true}
	launchInstallScroll := container.NewVScroll(launchInstallLogLabel)
	launchInstallScroll.SetMinSize(fyne.NewSize(0, 100))
	launchInstallSection := container.NewVBox(launchInstallScroll)
	launchInstallSection.Hide()

	step3 := container.NewCenter(container.NewVBox(
		container.NewWithoutLayout(launchTitle),
		container.NewWithoutLayout(launchSub),
		launchInstallSection,
		container.NewPadded(launchConnRow),
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
		// Step 1 (n==0): Next only after Connect & Register succeeds (regResult set).
		// Step 2 (n==1): Next always available to reach timeouts/paths step.
		nextBtn.Hidden = (n != 1) && (n != 0 || regResult == nil)
		saveBtn.Hidden = (n != 2)
		backBtn.Refresh()
		nextBtn.Refresh()
		saveBtn.Refresh()
	}

	backBtn.OnTapped = func() { setStep(currentStep - 1) }
	nextBtn.OnTapped = func() {
		if currentStep == 0 && regResult == nil {
			return
		}
		setStep(currentStep + 1)
	}

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

		localCfg.ConfirmNeverTimeout = neverTimeoutCheck.Checked
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

		// Show transition screen, then check/install Python before starting the runner.
		setStep(3)

		cfg := &config.Config{
			APIURL:       regResult.APIURL,
			Token:        regResult.Token,
			RunnerID:     regResult.RunnerUID,
			Name:         regResult.RunnerName,
			ExecutorType: "local",
			LocalConfig:  localCfg,
		}

		go func() {
			// Carried into the status window's log so it survives the wizard
			// window auto-hiding a couple seconds after connecting.
			var pythonWarning string

			// Check for Python and auto-install if missing.
			infos := interpreter.Discover(context.Background())
			if !hasRealPython(infos) {
				fyne.Do(func() {
					launchTitle.Text = "Installing Python…"
					launchTitle.Refresh()
					launchSub.Text = "Python is required for script steps. Installing now."
					launchSub.Refresh()
					launchInstallLogText = ""
					launchInstallLogLabel.SetText("")
					launchInstallSection.Show()
					launchInstallSection.Refresh()
				})

				emit := func(line string) {
					fyne.Do(func() {
						launchInstallLogText += line + "\n"
						launchInstallLogLabel.SetText(launchInstallLogText)
						launchInstallScroll.ScrollToBottom()
					})
				}

				installErr := installer.InstallPython(context.Background(), emit)

				// Re-probe regardless of installErr — some package managers exit
				// non-zero on a no-op (already installed) yet Python is present.
				recheck := interpreter.Discover(context.Background())
				if cfg.LocalConfig.Interpreters == nil {
					cfg.LocalConfig.Interpreters = make(map[string]string)
				}
				for _, info := range recheck {
					if (info.Name == "python3" || info.Name == "python") && info.Status == interpreter.StatusFound {
						cfg.LocalConfig.Interpreters[info.Name] = info.Path
					}
				}
				// Re-save config so Interpreters is persisted for future runs.
				_ = localconfig.Save(cfg.LocalConfig, configPath)

				if installErr != nil || !hasRealPython(recheck) {
					reason := "install failed"
					if installErr != nil {
						reason = installErr.Error()
					}
					pythonWarning = fmt.Sprintf("[runner] Python auto-install did not complete (%s) — scripts that invoke python may fail. Install manually: https://www.python.org/downloads/\n", reason)
					fyne.Do(func() {
						launchInstallLogText += "Python install failed — install manually then restart the runner.\n"
						launchInstallLogLabel.SetText(launchInstallLogText)
						launchInstallScroll.ScrollToBottom()
						launchSub.Text = "Python install failed — see log above. Continuing without Python."
						launchSub.Refresh()
					})
				} else {
					fyne.Do(func() {
						launchInstallLogText += "✓ Python installed successfully.\n"
						launchInstallLogLabel.SetText(launchInstallLogText)
						launchInstallScroll.ScrollToBottom()
						launchInstallSection.Hide()
						launchInstallSection.Refresh()
					})
				}
			}

			var bashWarning string
			if !hasRealBash(infos) {
				fyne.Do(func() {
					launchTitle.Text = "Installing Git for Windows…"
					launchTitle.Refresh()
					launchSub.Text = "A real bash is required to run job scripts. Installing now."
					launchSub.Refresh()
					launchInstallLogText = ""
					launchInstallLogLabel.SetText("")
					launchInstallSection.Show()
					launchInstallSection.Refresh()
				})

				emit := func(line string) {
					fyne.Do(func() {
						launchInstallLogText += line + "\n"
						launchInstallLogLabel.SetText(launchInstallLogText)
						launchInstallScroll.ScrollToBottom()
					})
				}

				installErr := installer.InstallGitBash(context.Background(), emit)

				recheck := interpreter.Discover(context.Background())
				if cfg.LocalConfig.Interpreters == nil {
					cfg.LocalConfig.Interpreters = make(map[string]string)
				}
				for _, info := range recheck {
					if info.Name == "bash" && info.Status == interpreter.StatusFound {
						cfg.LocalConfig.Interpreters[info.Name] = info.Path
					}
				}
				// Re-save config so Interpreters is persisted for future runs.
				_ = localconfig.Save(cfg.LocalConfig, configPath)

				if installErr != nil || !hasRealBash(recheck) {
					reason := "install failed"
					if installErr != nil {
						reason = installErr.Error()
					}
					bashWarning = fmt.Sprintf("[runner] bash auto-install did not complete (%s) — job scripts will fail to run. Install manually: https://git-scm.com/download/win\n", reason)
					fyne.Do(func() {
						launchInstallLogText += "Git for Windows install failed — install manually then restart the runner.\n"
						launchInstallLogLabel.SetText(launchInstallLogText)
						launchInstallScroll.ScrollToBottom()
						launchSub.Text = "Git for Windows install failed — see log above. Continuing without bash."
						launchSub.Refresh()
					})
				} else {
					fyne.Do(func() {
						launchInstallLogText += "✓ Git for Windows installed successfully.\n"
						launchInstallLogLabel.SetText(launchInstallLogText)
						launchInstallScroll.ScrollToBottom()
						launchInstallSection.Hide()
						launchInstallSection.Refresh()
					})
				}
			}

			fyne.Do(func() {
				launchTitle.Text = "Starting runner…"
				launchTitle.Refresh()
				launchSub.Text = "Connecting to WorkflowFiesta. This usually takes a few seconds."
				launchSub.Refresh()
				launchConnRow.Show()
				launchConnRow.Refresh()

				sw := startFn(cfg)
				if sw != nil {
					if pythonWarning != "" {
						sw.AppendLog(pythonWarning)
					}
					if bashWarning != "" {
						sw.AppendLog(bashWarning)
					}
					sw.SetOnAPIConnected(func() {
						fyne.Do(func() {
							launchTitle.Text = "Connected!"
							launchTitle.Refresh()
							launchSub.Text = "Your runner is online and ready to receive jobs."
							launchSub.Refresh()
							launchConnDot.FillColor = colorSuccess
							canvas.Refresh(launchConnDot)
							launchConnStatus.Text = "✓ Connected to WorkflowFiesta"
							launchConnStatus.Color = colorSuccess
							launchConnStatus.Refresh()
							time.AfterFunc(2*time.Second, func() {
								fyne.Do(win.Hide)
							})
						})
					})
					go func() {
						time.Sleep(90 * time.Second)
						fyne.Do(func() {
							if sw.APIConnected() || currentStep != 3 {
								return
							}
							launchSub.Text = "Still connecting. Check your network and API URL in the status window."
							launchSub.Refresh()
							launchConnStatus.Text = "Connection is taking longer than expected"
							launchConnStatus.Color = colorAmber
							launchConnStatus.Refresh()
						})
					}()
				} else {
					win.Hide()
				}
			})
		}()
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
