//go:build !nolocalui

package localui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"workflowfiesta-runner/internal/localconfig"
)

// RegistrationResult holds the credentials returned from a successful registration.
type RegistrationResult struct {
	RunnerID   string
	Token      string
	RunnerName string
	APIURL     string
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
	win.Resize(fyne.NewSize(520, 460))
	win.CenterOnScreen()

	var regResult *RegistrationResult
	var saveErr error

	// Steps: 0=Connect, 1=Token, 2=Config, 3=Done
	const numSteps = 4
	stepLabel := widget.NewLabel("Step 1 of 4")
	backBtn := widget.NewButton("← Back", nil)
	nextBtn := widget.NewButton("Next →", nil)
	saveBtn := widget.NewButton("Save & Start", nil)
	saveBtn.Importance = widget.HighImportance

	body := container.NewStack()
	navRow := container.NewHBox(backBtn, layout.NewSpacer(), stepLabel, layout.NewSpacer(), nextBtn, saveBtn)
	win.SetContent(container.NewBorder(nil, navRow, nil, nil, body))

	steps := make([]fyne.CanvasObject, numSteps)
	var currentStep int

	show := func(n int) {
		currentStep = n
		stepLabel.SetText(fmt.Sprintf("Step %d of %d", n+1, numSteps))
		body.Objects = []fyne.CanvasObject{steps[n]}
		body.Refresh()

		backBtn.Hidden = (n == 0 || n == 3)
		nextBtn.Hidden = (n != 1) // only step 1 has a "Next" button
		saveBtn.Hidden = (n != 2) // only step 2 has "Save & Start"
		backBtn.Refresh()
		nextBtn.Refresh()
		saveBtn.Refresh()
	}

	// ── Step 0: Connect & Register ──────────────────────────────────────────
	apiURLEntry := widget.NewEntry()
	apiURLEntry.SetText("http://localhost:3001")
	apiURLEntry.SetPlaceHolder("https://your-instance.workflowfiesta.com")

	orgIDEntry := widget.NewEntry()
	orgIDEntry.SetPlaceHolder("your-organization-id")

	nameEntry := widget.NewEntry()
	nameEntry.SetText(defaultRunnerName())
	nameEntry.SetPlaceHolder("my-laptop")

	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapWord

	registerBtn := widget.NewButton("Register Runner", nil)
	registerBtn.Importance = widget.HighImportance

	registerBtn.OnTapped = func() {
		apiURL := strings.TrimRight(strings.TrimSpace(apiURLEntry.Text), "/")
		orgID := strings.TrimSpace(orgIDEntry.Text)
		name := strings.TrimSpace(nameEntry.Text)
		if apiURL == "" || orgID == "" || name == "" {
			statusLabel.SetText("⚠  All fields are required.")
			return
		}
		registerBtn.Disable()
		statusLabel.SetText("Registering…")
		go func() {
			r, err := callRegisterAPI(apiURL, name, orgID)
			if err != nil {
				registerBtn.Enable()
				statusLabel.SetText("✗  " + err.Error())
				return
			}
			regResult = r
			statusLabel.SetText("✓  Registered!")
			show(1)
		}()
	}

	steps[0] = container.NewVBox(
		widget.NewLabelWithStyle("Connect to WorkflowFiesta", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Enter your WorkflowFiesta API URL and register this machine as a runner."),
		widget.NewSeparator(),
		widget.NewForm(
			widget.NewFormItem("API URL", apiURLEntry),
			widget.NewFormItem("Organization ID", orgIDEntry),
			widget.NewFormItem("Runner name", nameEntry),
		),
		registerBtn,
		statusLabel,
	)

	// ── Step 1: Token display ───────────────────────────────────────────────
	tokenDisplay := widget.NewLabel("")
	tokenDisplay.TextStyle = fyne.TextStyle{Monospace: true}
	tokenDisplay.Wrapping = fyne.TextWrapWord
	runnerIDDisplay := widget.NewLabel("")
	copyTokenBtn := widget.NewButton("Copy token", func() {
		if regResult != nil {
			win.Clipboard().SetContent(regResult.Token)
		}
	})

	steps[1] = container.NewVBox(
		widget.NewLabelWithStyle("Registered!", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Save your token — it won't be shown again."),
		widget.NewSeparator(),
		runnerIDDisplay,
		tokenDisplay,
		copyTokenBtn,
		widget.NewSeparator(),
		widget.NewLabel("Click Next to configure local permissions."),
	)

	// ── Step 2: Local config ────────────────────────────────────────────────
	pathsEntry := widget.NewMultiLineEntry()
	pathsEntry.SetPlaceHolder("One path per line — append :ro for read-only\n~/projects\n~/Documents:ro")
	pathsEntry.SetText("~/")
	pathsEntry.SetMinRowsVisible(3)

	browseBtn := widget.NewButton("Browse…", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			cur := pathsEntry.Text
			if cur != "" && !strings.HasSuffix(cur, "\n") {
				cur += "\n"
			}
			pathsEntry.SetText(cur + uri.Path())
		}, win)
	})

	confirmRadio := widget.NewRadioGroup(
		[]string{"Always (every job)", "Risky operations only (default)", "Never"},
		nil,
	)
	confirmRadio.SetSelected("Risky operations only (default)")

	networkRadio := widget.NewRadioGroup(
		[]string{"Allow all (default)", "Local only", "Block all"},
		nil,
	)
	networkRadio.SetSelected("Allow all (default)")

	steps[2] = container.NewVBox(
		widget.NewLabelWithStyle("Local permissions", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Which folders can scripts access?"),
		pathsEntry,
		browseBtn,
		widget.NewSeparator(),
		widget.NewLabel("Approval prompt:"),
		confirmRadio,
		widget.NewLabel("Network:"),
		networkRadio,
	)

	// ── Step 3: Done ────────────────────────────────────────────────────────
	doneLabel := widget.NewLabel("")
	doneLabel.TextStyle = fyne.TextStyle{Monospace: true}
	doneLabel.Wrapping = fyne.TextWrapWord

	closeBtn := widget.NewButton("Close", func() { win.Close() })

	steps[3] = container.NewVBox(
		widget.NewLabelWithStyle("All set! 🎉", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Your runner is configured. Start it with:"),
		widget.NewSeparator(),
		doneLabel,
		widget.NewSeparator(),
		closeBtn,
	)

	// ── Navigation wiring ───────────────────────────────────────────────────
	nextBtn.OnTapped = func() {
		if currentStep == 1 && regResult != nil {
			tokenDisplay.SetText(regResult.Token)
			runnerIDDisplay.SetText("Runner ID: " + regResult.RunnerID)
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
			dialog.ShowError(fmt.Errorf("registration not completed — go back and register first"), win)
			return
		}

		cfg := localconfig.Default()
		var paths []string
		for _, line := range splitLines(pathsEntry.Text) {
			if line != "" {
				paths = append(paths, line)
			}
		}
		if len(paths) > 0 {
			cfg.AllowedPaths = paths
		}
		switch confirmRadio.Selected {
		case "Always (every job)":
			cfg.Confirm = "always"
		case "Never":
			cfg.Confirm = "never"
		default:
			cfg.Confirm = "destructive"
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

		doneLabel.SetText(fmt.Sprintf("source %s\nworkflowfiesta-runner run-local", credPath))
		show(3)
	}

	show(0)
	win.ShowAndRun()
	return regResult, saveErr
}

// callRegisterAPI posts to /api/runners/register and returns the result.
func callRegisterAPI(apiURL, name, orgID string) (*RegistrationResult, error) {
	apiURL = strings.TrimRight(apiURL, "/")
	body, _ := json.Marshal(map[string]string{"name": name, "org_id": orgID})
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(apiURL+"/api/runners/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var payload struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &RegistrationResult{
		RunnerID:   payload.ID,
		Token:      payload.Token,
		RunnerName: name,
		APIURL:     apiURL,
	}, nil
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
