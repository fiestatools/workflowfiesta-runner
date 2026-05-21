//go:build !nolocalui

package localui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"net/http"
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
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/localconfig"
	"workflowfiesta-runner/internal/platform"
)

// RegistrationResult holds the credentials returned from a successful registration.
type RegistrationResult struct {
	RunnerUID      string
	Token          string
	RunnerName     string
	APIURL         string
	OrgUID         string
	EnvironmentUID string
}

// RunRegisterWizard opens the combined registration + local-config setup wizard.
// On success it writes runner.yaml to configPath and saves credentials to
// ~/.workflowfiesta/credentials.env. It returns the registration result.
func RunRegisterWizard(configPath string) (*RegistrationResult, error) {
	if !hasDisplay() {
		return nil, fmt.Errorf("no display available; run register-local on a machine with a GUI")
	}

	a := getApp()
	win := a.NewWindow("WorkflowFiesta · Register Runner")
	win.Resize(fyne.NewSize(520, 500))
	win.CenterOnScreen()

	var regResult *RegistrationResult
	var saveErr error

	const numSteps = 4
	var currentStep int

	// ── progress dots ────────────────────────────────────────────────────────
	dots := make([]*canvas.Rectangle, numSteps)
	for i := range dots {
		r := canvas.NewRectangle(colorBorder)
		r.CornerRadius = 4
		dots[i] = r
	}
	refreshDots := func(active int) {
		for i, d := range dots {
			switch {
			case i < active:
				d.FillColor = colorSuccess
			case i == active:
				d.FillColor = color.NRGBA{R: 59, G: 130, B: 246, A: 255}
			default:
				d.FillColor = colorBorder
			}
			d.Refresh()
		}
	}

	dotCells := make([]fyne.CanvasObject, numSteps)
	for i, d := range dots {
		dotCells[i] = container.New(layout.NewGridWrapLayout(fyne.NewSize(8, 8)), d)
	}
	dotsRow := container.NewHBox(append([]fyne.CanvasObject{}, dotCells...)...)

	// ── nav buttons ──────────────────────────────────────────────────────────
	stepCountLabel := canvas.NewText("Step 1 of 4", colorLabel)
	stepCountLabel.TextSize = 11

	backBtn := newButton("← Back", nil)
	nextBtn := newButton("Next →", nil)
	saveBtn := newButton("Save & Start", nil)
	saveBtn.Importance = widget.HighImportance

	navRow := container.NewHBox(
		container.NewWithoutLayout(stepCountLabel),
		layout.NewSpacer(),
		backBtn, nextBtn, saveBtn,
	)

	// ── step bodies ──────────────────────────────────────────────────────────
	steps := make([]fyne.CanvasObject, numSteps)

	show := func(n int) {
		currentStep = n
		stepCountLabel.Text = fmt.Sprintf("Step %d of %d", n+1, numSteps)
		stepCountLabel.Refresh()
		refreshDots(n)

		// Rebuild dot row widths (active dot is wider pill)
		for i, d := range dots {
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

		backBtn.Hidden = (n == 0 || n == numSteps-1)
		nextBtn.Hidden = (n == 0 || n == numSteps-1)
		saveBtn.Hidden = (n != numSteps-1)
		backBtn.Refresh()
		nextBtn.Refresh()
		saveBtn.Refresh()
	}

	// ── Step 1 of 4: Connect & Register ──────────────────────────────────────
	// Default API URL: env override > production SaaS. Self-hosted users open
	// "Advanced" to override. The vast majority of users never touch it.
	defaultAPIURL := os.Getenv("WORKFLOWFIESTA_API_URL")
	if defaultAPIURL == "" {
		defaultAPIURL = config.DefaultAPIURL
	}

	apiURLEntry := widget.NewEntry()
	apiURLEntry.SetText(defaultAPIURL)
	apiURLEntry.SetPlaceHolder("https://api.workflowfiesta.com")

	codeEntry := widget.NewEntry()
	codeEntry.SetPlaceHolder("RNR-XXXXXXX-XXXXXXX-XXXXXXX-XXXXXXX-XXXXXX")
	codeEntry.TextStyle = fyne.TextStyle{Monospace: true}

	getCodeURL, _ := url.Parse(frontendURLFrom(defaultAPIURL) + "/runners/setup")
	getCodeLink := widget.NewHyperlink("Don't have a code? Get one →", getCodeURL)
	apiURLEntry.OnChanged = func(v string) {
		if u, err := url.Parse(frontendURLFrom(strings.TrimSpace(v)) + "/runners/setup"); err == nil {
			getCodeLink.SetURL(u)
		}
	}

	connectAdvancedContent := container.NewVBox(
		makeFieldItem("API URL (only change for self-hosted)", apiURLEntry),
	)
	connectAdvancedAccordion := widget.NewAccordion(
		widget.NewAccordionItem("Advanced (self-hosted only)", connectAdvancedContent),
	)

	regStatusText := canvas.NewText("", colorMuted)
	regStatusText.TextSize = 12

	setRegStatus := func(msg string, col color.Color) {
		regStatusText.Text = msg
		regStatusText.Color = col
		regStatusText.Refresh()
	}

	registerBtn := newButton("Connect & Register", nil)
	registerBtn.Importance = widget.HighImportance

	registerBtn.OnTapped = func() {
		apiURL := strings.TrimRight(strings.TrimSpace(apiURLEntry.Text), "/")
		// Strip whitespace from pasted code; the parser also strips dashes.
		code := strings.Join(strings.Fields(codeEntry.Text), "")
		if apiURL == "" {
			setRegStatus("API URL is required.", colorAmber)
			return
		}
		if code == "" {
			setRegStatus("Registration code is required. Click 'Get one →' if you need one.", colorAmber)
			return
		}
		registerBtn.Disable()
		setRegStatus("Registering…", colorMuted)
		go func() {
			r, err := callRegisterAPI(apiURL, code)
			if err != nil {
				fyne.Do(func() {
					registerBtn.Enable()
					setRegStatus(err.Error(), colorDanger)
				})
				return
			}
			regResult = r
			fyne.Do(func() {
				setRegStatus("Registered! Proceeding to next step…", colorSuccess)
				show(1)
			})
		}()
	}

	steps[0] = container.NewVBox(
		makeStepHeading("Step 1: Connect to WorkflowFiesta", "Paste your one-time registration code. The code embeds your organization, so this is all you need."),
		makeFieldItem("Registration Code", codeEntry),
		container.NewHBox(getCodeLink),
		registerBtn,
		container.NewWithoutLayout(regStatusText),
		widget.NewSeparator(),
		connectAdvancedAccordion,
	)

	// ── Step 2 of 4: Token display ────────────────────────────────────────────
	tokenDisplay := widget.NewLabel("")
	tokenDisplay.TextStyle = fyne.TextStyle{Monospace: true}
	tokenDisplay.Wrapping = fyne.TextWrapWord
	tokenBg := canvas.NewRectangle(colorTermBg)
	tokenBg.CornerRadius = 4
	tokenBg.StrokeColor = colorBorder
	tokenBg.StrokeWidth = 1
	tokenScroll := container.NewVScroll(tokenDisplay)
	tokenScroll.SetMinSize(fyne.NewSize(440, 60))
	tokenBlock := container.NewStack(tokenBg, container.NewPadded(tokenScroll))

	runnerIDDisplay := canvas.NewText("", colorMuted)
	runnerIDDisplay.TextSize = 11

	copyBtn := newButton("Copy Token", func() {
		if regResult != nil {
			win.Clipboard().SetContent(regResult.Token)
		}
	})
	savedNote := canvas.NewText("Token saved automatically to credentials.env", colorLabel)
	savedNote.TextSize = 10

	steps[1] = container.NewVBox(
		makeStepHeading("Step 2: Runner Registered!", "Save this token — it will not be shown again."),
		container.NewPadded(container.NewWithoutLayout(runnerIDDisplay)),
		tokenBlock,
		container.NewHBox(copyBtn, container.NewWithoutLayout(savedNote)),
		widget.NewLabel("Click Next to configure local permissions."),
	)

	// ── Step 3 of 4: Local config ─────────────────────────────────────────────
	cfgDefaults := localconfig.Default()

	confirmRadio := widget.NewRadioGroup(
		[]string{"Always (every job)", "Risky operations only (recommended)", "Never"},
		nil,
	)
	confirmRadio.SetSelected("Risky operations only (recommended)")

	// Confirm Timeout
	confirmTimeoutEntry := widget.NewEntry()
	confirmTimeoutEntry.SetText(strconv.Itoa(cfgDefaults.ConfirmTimeout))
	confirmTimeoutHint := canvas.NewText(
		"How long the approval dialog waits for you before the job is auto-denied (default: 120 s).",
		colorMuted,
	)
	confirmTimeoutHint.TextSize = 10

	// Max Timeout
	maxTimeoutEntry := widget.NewEntry()
	maxTimeoutEntry.SetText(strconv.Itoa(cfgDefaults.MaxTimeout))
	maxTimeoutHint := canvas.NewText(
		"Upper limit on how long any single job may run before it is stopped (default: 180 s).",
		colorMuted,
	)
	maxTimeoutHint.TextSize = 10

	// Sound on approval
	soundCheck := widget.NewCheck("Play sound on approval request", nil)
	soundCheck.SetChecked(cfgDefaults.SoundOnApproval)

	// Allowed paths
	pathsEntry := widget.NewMultiLineEntry()
	pathsEntry.SetPlaceHolder("One path per line, e.g.\n~/projects\n~/Documents:ro")
	pathsEntry.SetText("~/")
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

	networkRadio := widget.NewRadioGroup(
		[]string{"Allow all (recommended)", "Local only", "Block all"},
		nil,
	)
	networkRadio.SetSelected("Allow all (recommended)")

	// ── Advanced Options (accordion, collapsed by default) ────────────────────
	sandboxRadio := widget.NewRadioGroup(
		[]string{"None (disabled)", "Kernel (Landlock, Linux 5.13+)"},
		nil,
	)
	sandboxRadio.SetSelected("None (disabled)")

	auditLogEntry := widget.NewEntry()
	auditLogEntry.SetText(cfgDefaults.AuditLog)
	auditLogEntry.SetPlaceHolder("/path/to/audit.log (leave blank to disable)")
	auditLogHint := canvas.NewText("Append a line to this file for every script that runs.", colorMuted)
	auditLogHint.TextSize = 10

	blockedEntry := widget.NewMultiLineEntry()
	blockedEntry.SetPlaceHolder("One regex pattern per line, e.g.  rm\\s+-rf\\s+/")
	blockedEntry.SetText(strings.Join(cfgDefaults.BlockedPatterns, "\n"))
	blockedEntry.SetMinRowsVisible(4)
	blockedHint := canvas.NewText("Scripts matching these patterns will be flagged in the approval dialog.", colorMuted)
	blockedHint.TextSize = 10

	alwaysAllowedEntry := widget.NewMultiLineEntry()
	alwaysAllowedEntry.SetPlaceHolder("Patterns auto-approved without a prompt (one regex per line)")
	alwaysAllowedEntry.SetMinRowsVisible(3)
	alwaysAllowedHint := canvas.NewText("Scripts matching these patterns are never shown in the approval dialog.", colorMuted)
	alwaysAllowedHint.TextSize = 10

	environmentIDEntry := widget.NewEntry()
	environmentIDEntry.SetPlaceHolder("Leave blank — a new environment is auto-created on registration")
	envIDHint := canvas.NewText("UUID of an existing environment to attach this runner to.", colorMuted)
	envIDHint.TextSize = 10

	advancedContent := container.NewVBox(
		makeSectionRow("Sandbox", win,
			"Sandbox Mode",
			"Kernel mode (Landlock) restricts filesystem access at the OS level, even if a script tries to bypass the allowed-paths list.\n\nRequires Linux kernel 5.13 or later. Has no effect on Windows or macOS."),
		sandboxRadio,
		widget.NewSeparator(),
		makeLabeledEntryInfo("Audit Log Path", auditLogEntry, auditLogHint, win,
			"Audit Log",
			"Every script execution is appended to this file so you have a complete record of what ran on this machine.\n\nLeave blank to disable audit logging."),
		widget.NewSeparator(),
		makeSectionRow("Blocked Patterns", win,
			"Blocked Patterns",
			"Regular expressions that flag scripts in the approval dialog.\n\nScripts matching these patterns are highlighted as potentially dangerous, but you still decide whether to allow them. They are not automatically blocked."),
		blockedEntry,
		container.NewWithoutLayout(blockedHint),
		widget.NewSeparator(),
		makeSectionRow("Always-Approved Patterns", win,
			"Always-Approved Patterns",
			"Regular expressions for scripts that are automatically approved without showing an approval dialog.\n\nUse these for safe, frequently-run commands you never want to be interrupted by."),
		alwaysAllowedEntry,
		container.NewWithoutLayout(alwaysAllowedHint),
		widget.NewSeparator(),
		makeSectionRow("Environment ID (override)", win,
			"Environment ID",
			"UUID of an existing WorkflowFiesta environment to attach this runner to.\n\nLeave blank — a new environment is created automatically during registration."),
		environmentIDEntry,
		container.NewWithoutLayout(envIDHint),
	)
	advancedAccordion := widget.NewAccordion(
		widget.NewAccordionItem("Advanced Options", advancedContent),
	)
	// Collapsed by default — do not call advancedAccordion.Open(0)

	steps[2] = container.NewVBox(
		makeStepHeading("Step 3: Local Permissions", "Control what scripts can access and whether you're prompted for approval."),
		makeSectionRow("Folder Access", win,
			"Folder Access",
			"Directories this runner can read from and write to.\n\nScripts that access paths outside this list will be blocked or require approval, depending on your Approval Prompt setting.\n\nAppend :ro to a path for read-only access (e.g. ~/Documents:ro)."),
		pathsEntry,
		container.NewHBox(browseBtn, container.NewWithoutLayout(pathHint)),
		widget.NewSeparator(),
		makeSectionRow("Approval Prompt", win,
			"Approval Prompt",
			"Controls when a dialog appears asking you to approve a script before it runs.\n\n• Always — you approve every single job.\n• Risky operations only — jobs that match blocked patterns or access sensitive paths require approval (recommended).\n• Never — all jobs run without asking."),
		confirmRadio,
		makeLabeledEntryInfo("Confirm timeout (s)", confirmTimeoutEntry, confirmTimeoutHint, win,
			"Confirm Timeout",
			"How long (in seconds) the runner waits for you to approve a job.\n\nIf you don't respond within this time the job is automatically cancelled. Default: 120 s."),
		makeLabeledEntryInfo("Max job timeout (s)", maxTimeoutEntry, maxTimeoutHint, win,
			"Max Job Timeout",
			"The maximum time (in seconds) a script is allowed to run before it is forcibly stopped.\n\nUse this to prevent runaway jobs from consuming resources indefinitely. Default: 180 s."),
		container.NewHBox(soundCheck, makeInfoBtn(win,
			"Sound on Approval",
			"Plays a system notification sound when an approval dialog appears.\n\nUseful when the runner window is minimised or in the background.")),
		widget.NewSeparator(),
		makeSectionRow("Network Access", win,
			"Network Access",
			"Controls outbound network access for scripts running on this runner.\n\n• Allow all — no network restrictions (recommended for most use cases).\n• Local only — scripts can only connect to localhost / 127.0.0.1.\n• Block all — no network connections permitted."),
		networkRadio,
		widget.NewSeparator(),
		advancedAccordion,
	)

	// ── Step 4 of 4: Review & Save ────────────────────────────────────────────
	summaryLabel := widget.NewLabel("")
	summaryLabel.Wrapping = fyne.TextWrapWord
	summaryBg := canvas.NewRectangle(colorTermBg)
	summaryBg.CornerRadius = 4
	summaryBg.StrokeColor = colorBorder
	summaryBg.StrokeWidth = 1
	summaryBlock := container.NewStack(summaryBg, container.NewPadded(summaryLabel))

	savedText := canvas.NewText("", colorSuccess)
	savedText.TextSize = 11
	savedBox := container.NewWithoutLayout(savedText)

	closeBtnInner := newButton("Close", func() { win.Close() })
	closeBtnInner.Hidden = true

	steps[3] = container.NewVBox(
		makeStepHeading("Step 4: Review & Confirm", "Check your settings, then click Save & Start."),
		summaryBlock,
		savedBox,
		container.NewPadded(closeBtnInner),
	)

	// ── navigation wiring ─────────────────────────────────────────────────────
	nextBtn.OnTapped = func() {
		if currentStep == 1 && regResult != nil {
			tokenDisplay.SetText(regResult.Token)
			runnerIDDisplay.Text = "Runner UID: " + regResult.RunnerUID
			runnerIDDisplay.Refresh()
		}
		if currentStep == 2 {
			// Populate the review summary before showing step 3
			soundStr := "off"
			if soundCheck.Checked {
				soundStr = "on"
			}
			// Advanced fields summary (only show non-default values)
			advancedLines := ""
			if sandboxRadio.Selected == "Kernel (Landlock, Linux 5.13+)" {
				advancedLines += "\nSandbox:           kernel"
			}
			if v := strings.TrimSpace(auditLogEntry.Text); v != "" && v != cfgDefaults.AuditLog {
				advancedLines += "\nAudit log:         " + v
			}
			blockedCount := 0
			for _, line := range strings.Split(blockedEntry.Text, "\n") {
				if strings.TrimSpace(line) != "" {
					blockedCount++
				}
			}
			if blockedCount > 0 {
				advancedLines += fmt.Sprintf("\nBlocked patterns:  %d", blockedCount)
			}
			alwaysCount := 0
			for _, line := range strings.Split(alwaysAllowedEntry.Text, "\n") {
				if strings.TrimSpace(line) != "" {
					alwaysCount++
				}
			}
			if alwaysCount > 0 {
				advancedLines += fmt.Sprintf("\nAuto-approved:     %d patterns", alwaysCount)
			}
			if v := strings.TrimSpace(environmentIDEntry.Text); v != "" {
				advancedLines += "\nEnvironment ID:    " + v
			}
			summaryLabel.SetText(fmt.Sprintf(
				"Allowed paths:\n%s\n\nApproval:          %s\nConfirm timeout:   %s s\nMax timeout:       %s s\nSound on approval: %s\nNetwork:           %s%s",
				pathsEntry.Text,
				confirmRadio.Selected,
				confirmTimeoutEntry.Text,
				maxTimeoutEntry.Text,
				soundStr,
				networkRadio.Selected,
				advancedLines,
			))
		}
		if currentStep < numSteps-1 {
			show(currentStep + 1)
		}
	}
	backBtn.OnTapped = func() {
		if currentStep > 0 {
			show(currentStep - 1)
		}
	}

	saveBtn.OnTapped = func() {
		if regResult == nil {
			dialog.ShowError(fmt.Errorf("registration not completed — go back to Step 1 first"), win)
			return
		}

		cfg := localconfig.Default()
		switch confirmRadio.Selected {
		case "Always (every job)":
			cfg.Confirm = "always"
		case "Never":
			cfg.Confirm = "never"
		default:
			cfg.Confirm = "destructive"
		}

		// Parse confirm timeout
		if v, err := strconv.Atoi(confirmTimeoutEntry.Text); err == nil && v > 0 {
			cfg.ConfirmTimeout = v
		}
		// Parse max timeout
		if v, err := strconv.Atoi(maxTimeoutEntry.Text); err == nil && v > 0 {
			cfg.MaxTimeout = v
		}

		cfg.SoundOnApproval = soundCheck.Checked

		// Parse allowed paths
		var paths []string
		for _, line := range strings.Split(pathsEntry.Text, "\n") {
			if strings.TrimSpace(line) != "" {
				paths = append(paths, strings.TrimSpace(line))
			}
		}
		if len(paths) > 0 {
			cfg.AllowedPaths = paths
		}

		switch networkRadio.Selected {
		case "Local only":
			cfg.Network = "localhost"
		case "Block all":
			cfg.Network = "none"
		default:
			cfg.Network = "all"
		}

		// Advanced options
		if sandboxRadio.Selected == "Kernel (Landlock, Linux 5.13+)" {
			cfg.Sandbox = "kernel"
		} else {
			cfg.Sandbox = "none"
		}
		if v := strings.TrimSpace(auditLogEntry.Text); v != "" {
			cfg.AuditLog = v
		}
		var blocked []string
		for _, line := range strings.Split(blockedEntry.Text, "\n") {
			if strings.TrimSpace(line) != "" {
				blocked = append(blocked, strings.TrimSpace(line))
			}
		}
		cfg.BlockedPatterns = blocked
		var alwaysAllowed []string
		for _, line := range strings.Split(alwaysAllowedEntry.Text, "\n") {
			if strings.TrimSpace(line) != "" {
				alwaysAllowed = append(alwaysAllowed, strings.TrimSpace(line))
			}
		}
		cfg.AlwaysAllowedPatterns = alwaysAllowed
		if v := strings.TrimSpace(environmentIDEntry.Text); v != "" {
			cfg.EnvironmentID = v
		}

		if err := localconfig.Save(cfg, configPath); err != nil {
			saveErr = err
			dialog.ShowError(err, win)
			return
		}

		credPath := filepath.Join(filepath.Dir(configPath), "credentials.env")
		if err := writeCredentials(credPath, regResult); err != nil {
			dialog.ShowError(fmt.Errorf("save credentials: %w", err), win)
			return
		}

		// Update review screen in-place to show success state
		savedText.Text = "✓ Saved to " + credPath + " — double-click the app any time to start."
		savedText.Refresh()
		saveBtn.Hide()
		closeBtnInner.Show()
	}

	// ── layout ────────────────────────────────────────────────────────────────
	bodyHolder := container.NewStack()
	bodyHolder.Objects = []fyne.CanvasObject{steps[0]}

	show0 := func(n int) {
		currentStep = n
		stepCountLabel.Text = fmt.Sprintf("Step %d of %d", n+1, numSteps)
		stepCountLabel.Refresh()
		refreshDots(n)
		bodyHolder.Objects = []fyne.CanvasObject{steps[n]}
		bodyHolder.Refresh()
		if n == 0 || n == numSteps-1 {
			backBtn.Hide()
			nextBtn.Hide()
		} else {
			backBtn.Show()
			nextBtn.Show()
		}
		if n == numSteps-1 {
			saveBtn.Show()
		} else {
			saveBtn.Hide()
		}
	}
	// Override show to also update bodyHolder
	show = show0

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

	show(0)
	win.ShowAndRun()
	return regResult, saveErr
}

// ── helpers ───────────────────────────────────────────────────────────────────

func makeStepHeading(title, desc string) fyne.CanvasObject {
	t := canvas.NewText(title, colorText)
	t.TextSize = 16
	t.TextStyle = fyne.TextStyle{Bold: true}
	d := canvas.NewText(desc, colorMuted)
	d.TextSize = 11
	return container.NewVBox(
		container.NewWithoutLayout(t),
		container.NewWithoutLayout(d),
		widget.NewSeparator(),
	)
}

func makeFieldItem(label string, entry *widget.Entry) fyne.CanvasObject {
	lbl := canvas.NewText(label, colorMuted)
	lbl.TextSize = 11
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewVBox(container.NewWithoutLayout(lbl), entry)
}

func makeSectionLabel(text string) fyne.CanvasObject {
	lbl := canvas.NewText(strings.ToUpper(text), colorLabel)
	lbl.TextSize = 10
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewPadded(container.NewWithoutLayout(lbl))
}

// makeLabeledEntryWithHint2 creates a labeled entry with a small hint text below.
func makeLabeledEntryWithHint2(label string, entry *widget.Entry, hint *canvas.Text) fyne.CanvasObject {
	lbl := canvas.NewText(strings.ToUpper(label), colorLabel)
	lbl.TextSize = 10
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewVBox(
		container.NewWithoutLayout(lbl),
		entry,
		container.NewWithoutLayout(hint),
	)
}

// makeInfoBtn creates a small icon button that shows an information dialog when tapped.
func makeInfoBtn(win fyne.Window, title, explanation string) *widget.Button {
	btn := widget.NewButtonWithIcon("", theme.InfoIcon(), func() {
		dialog.ShowInformation(title, explanation, win)
	})
	btn.Importance = widget.LowImportance
	return btn
}

// makeSectionRow creates an uppercase section label with an info icon button on the right.
func makeSectionRow(text string, win fyne.Window, infoTitle, infoText string) fyne.CanvasObject {
	lbl := canvas.NewText(strings.ToUpper(text), colorLabel)
	lbl.TextSize = 10
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewHBox(
		container.NewWithoutLayout(lbl),
		makeInfoBtn(win, infoTitle, infoText),
	)
}

// makeLabeledEntryInfo creates a labeled entry with a hint below and an info icon next to the label.
func makeLabeledEntryInfo(label string, entry *widget.Entry, hint *canvas.Text, win fyne.Window, infoTitle, infoText string) fyne.CanvasObject {
	lbl := canvas.NewText(strings.ToUpper(label), colorLabel)
	lbl.TextSize = 10
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewVBox(
		container.NewHBox(container.NewWithoutLayout(lbl), makeInfoBtn(win, infoTitle, infoText)),
		entry,
		container.NewWithoutLayout(hint),
	)
}

// callRegisterAPI posts to /api/runner/register (UNAUTHENTICATED, code-based).
// The code embeds the orgUid AND the runner identity (name, environment, etc.)
// — they were chosen at code-issuance time on the server side. The runner only
// needs to send the code; the server returns the bearer token (= the code) and
// the runner's name/uid/environment for the runner to persist locally.
func callRegisterAPI(apiURL, code string) (*RegistrationResult, error) {
	apiURL = strings.TrimRight(apiURL, "/")
	reqBody := map[string]string{
		"code":          code,
		"documentsPath": platform.DocumentsPath(),
		"shell":         platform.Shell(),
	}
	bodyBytes, _ := json.Marshal(reqBody)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(apiURL+"/api/runner/register", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, friendlyNetworkError(err, apiURL)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return nil, friendlyHTTPError(resp.StatusCode, data)
	}

	var payload struct {
		UID            string `json:"uid"`
		Token          string `json:"token"`
		OrgUID         string `json:"orgUid"`
		Name           string `json:"name"`
		EnvironmentUID string `json:"environmentUid"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("unexpected response from server — is this a WorkflowFiesta instance?")
	}
	if payload.UID == "" || payload.Token == "" {
		return nil, fmt.Errorf("server returned an incomplete response (missing uid or token)")
	}
	return &RegistrationResult{
		RunnerUID:      payload.UID,
		Token:          payload.Token,
		RunnerName:     payload.Name,
		APIURL:         apiURL,
		OrgUID:         payload.OrgUID,
		EnvironmentUID: payload.EnvironmentUID,
	}, nil
}

// friendlyNetworkError converts a raw http/net error into a readable message.
func friendlyNetworkError(err error, apiURL string) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("connection refused — is the server running at %s?", apiURL)
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "unknown host"):
		return fmt.Errorf("host not found — check the API URL (%s)", apiURL)
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "i/o timeout"):
		return fmt.Errorf("connection timed out — the server at %s is not responding", apiURL)
	case strings.Contains(msg, "certificate"), strings.Contains(msg, "tls"):
		return fmt.Errorf("TLS/certificate error — try http:// instead of https:// for local instances")
	default:
		return fmt.Errorf("could not reach server: %s", msg)
	}
}

// friendlyHTTPError converts an HTTP error status + body into a readable message.
func friendlyHTTPError(status int, body []byte) error {
	// Try to extract a message field from a JSON error body.
	var errBody struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(body, &errBody)
	serverMsg := strings.TrimSpace(errBody.Message)
	if serverMsg == "" {
		serverMsg = strings.TrimSpace(errBody.Error)
	}

	switch status {
	case 400:
		if serverMsg != "" {
			return fmt.Errorf("%s", serverMsg)
		}
		return fmt.Errorf("invalid registration code — paste the full code (or click 'Get one →' for a fresh code)")
	case 401, 403:
		return fmt.Errorf("access denied (HTTP %d) — this server may require authentication", status)
	case 404:
		return fmt.Errorf("endpoint not found — is the API URL correct? (%s)", "/api/runner/register")
	case 409:
		return fmt.Errorf("a runner with this name already exists — choose a different runner name")
	case 500:
		if serverMsg != "" {
			return fmt.Errorf("server error: %s", serverMsg)
		}
		return fmt.Errorf("server error (500) — try again, or get a fresh registration code")
	default:
		if serverMsg != "" {
			return fmt.Errorf("error %d: %s", status, serverMsg)
		}
		return fmt.Errorf("unexpected response from server (HTTP %d)", status)
	}
}

// writeCredentials saves shell export lines to credPath (mode 0600).
func writeCredentials(credPath string, r *RegistrationResult) error {
	dir := filepath.Dir(credPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if home, err := os.UserHomeDir(); err == nil {
		_ = platform.SetHidden(filepath.Join(home, ".workflowfiesta"))
	}
	content := fmt.Sprintf(
		"export WORKFLOWFIESTA_API_URL=%s\nexport WORKFLOWFIESTA_TOKEN=%s\nexport WORKFLOWFIESTA_RUNNER_ID=%s\nexport WORKFLOWFIESTA_RUNNER_NAME=%s\n",
		r.APIURL, r.Token, r.RunnerUID, r.RunnerName,
	)
	return os.WriteFile(credPath, []byte(content), 0o600)
}

// frontendURLFrom maps an API URL to its frontend counterpart:
//
//	api.workflowfiesta.com         → app.workflowfiesta.com
//	staging.api.workflowfiesta.com → staging.app.workflowfiesta.com
//	app.workflowfiesta.com         → app.workflowfiesta.com  (unchanged)
//	localhost:5000                 → localhost:3000
func frontendURLFrom(apiURL string) string {
	s := strings.TrimRight(apiURL, "/")
	parsed, err := url.Parse(s)
	if err != nil {
		return s
	}
	host := parsed.Hostname()
	if strings.Contains(host, "api.") && !strings.Contains(host, "app.") {
		newHost := strings.Replace(host, "api.", "app.", 1)
		if parsed.Port() != "" {
			parsed.Host = newHost + ":" + parsed.Port()
		} else {
			parsed.Host = newHost
		}
		return parsed.String()
	}
	if (host == "localhost" || host == "127.0.0.1") && parsed.Port() == "5000" {
		parsed.Host = host + ":3000"
		return parsed.String()
	}
	return s
}
