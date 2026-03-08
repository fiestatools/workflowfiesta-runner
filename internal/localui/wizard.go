//go:build !nolocalui

package localui

import (
	"fmt"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"workflowfiesta-runner/internal/localconfig"
)

// RunWizard opens the first-run setup wizard.  It blocks until the user
// completes or cancels the wizard, writing the resulting config to configPath.
func RunWizard(configPath string) error {
	if !hasDisplay() {
		return fmt.Errorf("no display available; run init-local on a machine with a GUI or create %s manually", configPath)
	}

	a := getApp()
	win := a.NewWindow("WorkflowFiesta · Setup")
	win.Resize(fyne.NewSize(500, 400))
	win.CenterOnScreen()

	cfg := localconfig.Default()
	var saveErr error

	// --- Step state ---
	var currentStep int
	var stepContainers [4]fyne.CanvasObject

	// Back/Next/Finish buttons.
	backBtn := widget.NewButton("← Back", nil)
	nextBtn := widget.NewButton("Next →", nil)
	finishBtn := widget.NewButton("Save & Start", nil)
	backBtn.Hide()
	finishBtn.Hide()

	stepIndicator := widget.NewLabel("Step 1 of 4")

	contentHolder := container.NewStack()

	navRow := container.NewHBox(backBtn, layout.NewSpacer(), stepIndicator, layout.NewSpacer(), nextBtn, finishBtn)
	root := container.NewBorder(nil, navRow, nil, nil, contentHolder)
	win.SetContent(root)

	showStep := func(n int) {
		currentStep = n
		stepIndicator.SetText(fmt.Sprintf("Step %d of 4", n+1))
		contentHolder.Objects = []fyne.CanvasObject{stepContainers[n]}
		contentHolder.Refresh()
		backBtn.Hidden = (n == 0)
		nextBtn.Hidden = (n == 3)
		finishBtn.Hidden = (n != 3)
		backBtn.Refresh()
		nextBtn.Refresh()
		finishBtn.Refresh()
	}

	// ---- Step 1: Folder picker ----
	pathsEntry := widget.NewMultiLineEntry()
	pathsEntry.SetPlaceHolder("One path per line, e.g.\n~/projects\n~/Documents:ro")
	pathsEntry.SetText("~/")
	pathsEntry.SetMinRowsVisible(4)

	browseBtn := widget.NewButton("Browse…", func() {
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

	stepContainers[0] = container.NewVBox(
		widget.NewLabelWithStyle("Which folders can the agent access?", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("The agent will only be able to read and write files in these folders.\nAppend :ro for read-only access."),
		pathsEntry,
		browseBtn,
	)

	// ---- Step 2: Confirm mode ----
	confirmRadio := widget.NewRadioGroup(
		[]string{"Always (prompt for every job)", "Risky operations only (recommended)", "Never (auto-approve all jobs)"},
		nil,
	)
	confirmRadio.SetSelected("Risky operations only (recommended)")

	stepContainers[1] = container.NewVBox(
		widget.NewLabelWithStyle("When should you be asked to approve a job?", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		confirmRadio,
	)

	// ---- Step 3: Network ----
	networkRadio := widget.NewRadioGroup(
		[]string{"Allow all (recommended for most users)", "Local only (localhost / LAN)", "Block all outbound network"},
		nil,
	)
	networkRadio.SetSelected("Allow all (recommended for most users)")

	stepContainers[2] = container.NewVBox(
		widget.NewLabelWithStyle("Network access for scripts", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Controls whether scripts run by the agent can make network requests."),
		networkRadio,
	)

	// ---- Step 4: Summary ----
	summaryLabel := widget.NewLabel("")
	summaryLabel.Wrapping = fyne.TextWrapWord
	stepContainers[3] = container.NewVBox(
		widget.NewLabelWithStyle("Review your settings", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		summaryLabel,
		widget.NewLabel("Click \"Save & Start\" to write the config and launch the runner."),
	)

	updateSummary := func() {
		summaryLabel.SetText(fmt.Sprintf(
			"Allowed paths:\n%s\n\nApproval: %s\nNetwork: %s",
			pathsEntry.Text, confirmRadio.Selected, networkRadio.Selected,
		))
	}

	// Wire navigation.
	nextBtn.OnTapped = func() {
		if currentStep == 2 {
			updateSummary()
		}
		showStep(currentStep + 1)
	}
	backBtn.OnTapped = func() {
		showStep(currentStep - 1)
	}

	finishBtn.OnTapped = func() {
		// Parse paths.
		var paths []string
		for _, line := range splitLines(pathsEntry.Text) {
			if line != "" {
				paths = append(paths, line)
			}
		}
		if len(paths) == 0 {
			paths = []string{"~/"}
		}
		cfg.AllowedPaths = paths

		switch confirmRadio.Selected {
		case "Always (prompt for every job)":
			cfg.Confirm = "always"
		case "Never (auto-approve all jobs)":
			cfg.Confirm = "never"
		default:
			cfg.Confirm = "destructive"
		}

		switch networkRadio.Selected {
		case "Local only (localhost / LAN)":
			cfg.Network = "localhost"
		case "Block all outbound network":
			cfg.Network = "none"
		default:
			cfg.Network = "all"
		}

		if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
			dialog.ShowError(err, win)
			return
		}
		if err := localconfig.Save(cfg, configPath); err != nil {
			saveErr = err
			dialog.ShowError(err, win)
			return
		}
		win.Close()
	}

	showStep(0)
	win.ShowAndRun() // Starts the Fyne event loop; quits when the window closes.

	return saveErr
}

// hasDisplay returns false when running on Linux without a display server.
func hasDisplay() bool {
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" || isNonLinux()
}
