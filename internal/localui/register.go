//go:build !nolocalui

package localui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"net/http"
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

	"workflowfiesta-runner/internal/localconfig"
)

// RegistrationResult holds the credentials returned from a successful registration.
type RegistrationResult struct {
	RunnerID      string
	Token         string
	RunnerName    string
	APIURL        string
	EnvironmentID string // auto-created or chosen environment
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

		backBtn.Hidden = (n == 0 || n == 3)
		nextBtn.Hidden = (n != 1)
		saveBtn.Hidden = (n != 2)
		backBtn.Refresh()
		nextBtn.Refresh()
		saveBtn.Refresh()
	}

	// ── Step 0: Connect & Register ───────────────────────────────────────────
	apiURLEntry := widget.NewEntry()
	apiURLEntry.SetText("http://localhost:3001")
	apiURLEntry.SetPlaceHolder("https://your-instance.workflowfiesta.com")

	orgIDEntry := widget.NewEntry()
	orgIDEntry.SetPlaceHolder("your-organization-id")

	nameEntry := widget.NewEntry()
	nameEntry.SetText(defaultRunnerName())
	nameEntry.SetPlaceHolder("my-laptop")

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
		orgID := strings.TrimSpace(orgIDEntry.Text)
		name := strings.TrimSpace(nameEntry.Text)
		if apiURL == "" || orgID == "" || name == "" {
			setRegStatus("All fields are required.", colorAmber)
			return
		}
		registerBtn.Disable()
		setRegStatus("Connecting…", colorMuted)
		go func() {
			r, err := callRegisterAPI(apiURL, name, orgID, "")
			if err != nil {
				fyne.Do(func() {
					registerBtn.Enable()
					setRegStatus(err.Error(), colorDanger)
				})
				return
			}
			regResult = r
			fyne.Do(func() {
				setRegStatus("Connected! Proceeding to next step…", colorSuccess)
				show(1)
			})
		}()
	}

	steps[0] = container.NewVBox(
		makeStepHeading("Connect to WorkflowFiesta", "Enter your API URL and register this machine as a self-hosted runner."),
		makeFieldItem("API URL", apiURLEntry),
		makeFieldItem("Organization ID", orgIDEntry),
		makeFieldItem("Runner Name", nameEntry),
		registerBtn,
		container.NewWithoutLayout(regStatusText),
	)

	// ── Step 1: Token display ─────────────────────────────────────────────────
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
		makeStepHeading("Runner Registered!", "Save this token — it will not be shown again."),
		container.NewPadded(container.NewWithoutLayout(runnerIDDisplay)),
		tokenBlock,
		container.NewHBox(copyBtn, container.NewWithoutLayout(savedNote)),
		widget.NewLabel("Click Next to configure local permissions."),
	)

		// ── Step 2: Local config ──────────────────────────────────────────────────
	confirmRadio := widget.NewRadioGroup(
		[]string{"Always (every job)", "Risky operations only (recommended)", "Never"},
		nil,
	)
	confirmRadio.SetSelected("Risky operations only (recommended)")

	confirmTimeoutEntry := widget.NewEntry()
	confirmTimeoutEntry.SetText("120")
	confirmTimeoutHint := makeHintText(
		"Seconds to wait for your approval before the job is cancelled (default: 120).",
	)

	maxTimeoutEntry := widget.NewEntry()
	maxTimeoutEntry.SetText("180")
	maxTimeoutHint := makeHintText(
		"Maximum seconds a single job may run before it is forcibly terminated (default: 180).",
	)

	soundCheck := widget.NewCheck("Play a sound when an approval request arrives", nil)

	pathsEntry := widget.NewMultiLineEntry()
	pathsEntry.SetPlaceHolder("One path per line, e.g.\n~/projects\n~/Documents:ro")
	pathsEntry.SetText("~/")
	pathsEntry.SetMinRowsVisible(3)
	pathHint := makeHintText("Append :ro for read-only access.")

	networkRadio := widget.NewRadioGroup(
		[]string{"Allow all (recommended)", "Local only", "Block all"},
		nil,
	)
	networkRadio.SetSelected("Allow all (recommended)")

	steps[2] = container.NewVBox(
		makeStepHeading("Local Permissions", "Control what scripts can access and whether you\'re prompted for approval."),
		makeSectionLabel("Folder Access"),
		pathsEntry,
		container.NewWithoutLayout(pathHint),
		widget.NewSeparator(),
		makeSectionLabel("Approval Prompt"),
		confirmRadio,
		makeLabeledEntryWithHint("Confirm timeout (seconds)", confirmTimeoutEntry, confirmTimeoutHint),
		makeLabeledEntryWithHint("Max timeout (seconds)", maxTimeoutEntry, maxTimeoutHint),
		widget.NewSeparator(),
		soundCheck,
		widget.NewSeparator(),
		makeSectionLabel("Network Access"),
		networkRadio,
	)

// ── Step 3: Done ──────────────────────────────────────────────────────────
	doneLabel := widget.NewLabel("")
	doneLabel.TextStyle = fyne.TextStyle{Monospace: true}
	doneLabel.Wrapping = fyne.TextWrapWord
	doneBg := canvas.NewRectangle(colorTermBg)
	doneBg.CornerRadius = 4
	doneBg.StrokeColor = colorBorder
	doneBg.StrokeWidth = 1
	doneBlock := container.NewStack(doneBg, container.NewPadded(doneLabel))

	alertLabel := canvas.NewText("Keep credentials.env private — it contains your runner token.", colorAmber)
	alertLabel.TextSize = 11

	closeBtn := newButton("Close", func() { win.Close() })

	steps[3] = container.NewVBox(
		makeStepHeading("All Set! 🎉", "Your runner is configured and ready to start."),
		makeSectionLabel("Start with:"),
		doneBlock,
		container.NewWithoutLayout(alertLabel),
		container.NewPadded(closeBtn),
	)

	// ── navigation wiring ─────────────────────────────────────────────────────
	nextBtn.OnTapped = func() {
		if currentStep == 1 && regResult != nil {
			tokenDisplay.SetText(regResult.Token)
			runnerIDDisplay.Text = "Runner ID: " + regResult.RunnerID
			runnerIDDisplay.Refresh()
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
		if v, err := strconv.Atoi(confirmTimeoutEntry.Text); err == nil && v > 0 {
			cfg.ConfirmTimeout = v
		}
		if v, err := strconv.Atoi(maxTimeoutEntry.Text); err == nil && v > 0 {
			cfg.MaxTimeout = v
		}
		cfg.SoundOnApproval = soundCheck.Checked
		var regPaths []string
		for _, line := range splitLines(pathsEntry.Text) {
			if line != "" {
				regPaths = append(regPaths, line)
			}
		}
		if len(regPaths) > 0 {
			cfg.AllowedPaths = regPaths
		}
		switch networkRadio.Selected {
		case "Local only":
			cfg.Network = "localhost"
		case "Block all":
			cfg.Network = "none"
		default:
			cfg.Network = "all"
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

		doneLabel.SetText(fmt.Sprintf("Credentials saved to:\n%s\n\nDouble-click the app any time to start the runner.", credPath))
		show(3)
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
		backBtn.Hidden = (n == 0 || n == 3)
		nextBtn.Hidden = (n != 1)
		saveBtn.Hidden = (n != 2)
		backBtn.Refresh()
		nextBtn.Refresh()
		saveBtn.Refresh()
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
		container.NewPadded(bodyHolder),
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

// callRegisterAPI posts to /api/runners/register and returns the result.
// environmentID is optional; if empty the server auto-creates a new environment.
func callRegisterAPI(apiURL, name, orgID, environmentID string) (*RegistrationResult, error) {
	apiURL = strings.TrimRight(apiURL, "/")
	reqBody := map[string]string{"name": name, "org_id": orgID}
	if environmentID != "" {
		reqBody["environment_id"] = environmentID
	}
	bodyBytes, _ := json.Marshal(reqBody)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(apiURL+"/api/runners/register", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, friendlyNetworkError(err, apiURL)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return nil, friendlyHTTPError(resp.StatusCode, data)
	}

	var payload struct {
		ID            string `json:"id"`
		Token         string `json:"token"`
		EnvironmentID string `json:"environment_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("unexpected response from server — is this a WorkflowFiesta instance?")
	}
	if payload.ID == "" || payload.Token == "" {
		return nil, fmt.Errorf("server returned an incomplete response (missing id or token)")
	}
	return &RegistrationResult{
		RunnerID:      payload.ID,
		Token:         payload.Token,
		RunnerName:    name,
		APIURL:        apiURL,
		EnvironmentID: payload.EnvironmentID,
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
			return fmt.Errorf("bad request: %s", serverMsg)
		}
		return fmt.Errorf("bad request — check your API URL and Organization ID")
	case 401, 403:
		return fmt.Errorf("access denied (HTTP %d) — this server may require authentication", status)
	case 404:
		return fmt.Errorf("endpoint not found — is the API URL correct? (%s)", "/api/runners/register")
	case 409:
		return fmt.Errorf("a runner with this name already exists — choose a different runner name")
	case 500:
		// Try to surface the most useful detail from the server error.
		raw := strings.TrimSpace(string(body))
		if strings.Contains(raw, "org_id") || strings.Contains(raw, "organizations") {
			return fmt.Errorf("organization not found — check your Organization ID in Settings → Organization on the web app")
		}
		if serverMsg != "" {
			return fmt.Errorf("server error: %s", serverMsg)
		}
		return fmt.Errorf("server error (500) — check the API URL and Organization ID, then try again")
	default:
		if serverMsg != "" {
			return fmt.Errorf("error %d: %s", status, serverMsg)
		}
		return fmt.Errorf("unexpected response from server (HTTP %d)", status)
	}
}

// writeCredentials saves shell export lines to credPath (mode 0600).
func writeCredentials(credPath string, r *RegistrationResult) error {
	if err := os.MkdirAll(filepath.Dir(credPath), 0o700); err != nil {
		return err
	}
	content := fmt.Sprintf(
		"export WORKFLOWFIESTA_API_URL=%s\nexport WORKFLOWFIESTA_TOKEN=%s\nexport WORKFLOWFIESTA_RUNNER_ID=%s\nexport WORKFLOWFIESTA_RUNNER_NAME=%s\n",
		r.APIURL, r.Token, r.RunnerID, r.RunnerName,
	)
	return os.WriteFile(credPath, []byte(content), 0o600)
}

func defaultRunnerName() string {
	host, err := os.Hostname()
	if err != nil {
		return "my-runner"
	}
	return host
}
