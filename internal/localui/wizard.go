//go:build !nolocalui

package localui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"workflowfiesta-runner/internal/localconfig"
)

// RunWizard opens the first-run setup wizard. It blocks until the user
// completes or cancels the wizard, writing the resulting config to configPath.
func RunWizard(configPath string) error {
	if !hasDisplay() {
		return fmt.Errorf("no display available; run init-local on a machine with a GUI or create %s manually", configPath)
	}

	a := getApp()
	win := a.NewWindow("WorkflowFiesta · Setup")
	win.Resize(fyne.NewSize(520, 520))
	win.CenterOnScreen()

	cfg := localconfig.Default()
	var saveErr error
	var currentStep int

	const numSteps = 4

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
				d.FillColor = color.NRGBA{R: 0xF3, G: 0x61, B: 0x0C, A: 0xFF}
			default:
				d.FillColor = colorBorder
			}
			d.Refresh()
		}
	}

	dotCells := make([]fyne.CanvasObject, numSteps)
	makeDotCells := func(active int) {
		for i, d := range dots {
			sz := fyne.NewSize(8, 8)
			if i == active {
				sz = fyne.NewSize(24, 8)
			}
			dotCells[i] = container.New(layout.NewGridWrapLayout(sz), d)
		}
	}
	makeDotCells(0)
	dotsRow := container.NewHBox(dotCells...)

	// ── nav ──────────────────────────────────────────────────────────────────
	stepLabel := canvas.NewText("Step 1 of 4", colorLabel)
	stepLabel.TextSize = 11

	backBtn := newButton("← Back", nil)
	nextBtn := newButton("Next →", nil)
	finishBtn := newButton("Save & Start", nil)
	finishBtn.Importance = widget.HighImportance

	navRow := container.NewHBox(
		container.NewWithoutLayout(stepLabel),
		layout.NewSpacer(),
		backBtn, nextBtn, finishBtn,
	)

	bodyHolder := container.NewStack()
	var stepContainers [numSteps]fyne.CanvasObject

	showStep := func(n int) {
		currentStep = n
		stepLabel.Text = fmt.Sprintf("Step %d of 4", n+1)
		stepLabel.Refresh()
		refreshDots(n)
		makeDotCells(n)
		objs := make([]fyne.CanvasObject, numSteps)
		copy(objs, dotCells)
		dotsRow.Objects = objs
		dotsRow.Refresh()
		bodyHolder.Objects = []fyne.CanvasObject{stepContainers[n]}
		bodyHolder.Refresh()
		backBtn.Hidden = (n == 0)
		nextBtn.Hidden = (n == 3)
		finishBtn.Hidden = (n != 3)
		backBtn.Refresh()
		nextBtn.Refresh()
		finishBtn.Refresh()
	}

	// ── Step 1: Folder access ─────────────────────────────────────────────────
	pathsEntry := widget.NewMultiLineEntry()
	pathsEntry.SetPlaceHolder("One path per line, e.g.\n~/projects\n~/Documents:ro")
	pathsEntry.SetText("~/")
	pathsEntry.SetMinRowsVisible(4)

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

	pathHint := canvas.NewText("Append  :ro  for read-only access", colorLabel)
	pathHint.TextSize = 11

	stepContainers[0] = container.NewVBox(
		makeStepHeading("Folder Access", "Which folders can scripts read and write?"),
		pathsEntry,
		container.NewHBox(browseBtn, container.NewWithoutLayout(pathHint)),
	)

	// ── Step 2: Approval mode ─────────────────────────────────────────────────
	confirmRadio := widget.NewRadioGroup(
		[]string{"Always (prompt for every job)", "Risky operations only (recommended)", "Never (auto-approve all jobs)"},
		nil,
	)
	confirmRadio.SetSelected("Risky operations only (recommended)")

	// Confirm Timeout
	confirmTimeoutEntry := widget.NewEntry()
	confirmTimeoutEntry.SetText(strconv.Itoa(cfg.ConfirmTimeout))
	confirmTimeoutHint := makeHintText(
		"How long (in seconds) to wait for your approval before cancelling the job. Default: 120 s.",
	)

	// Max Timeout
	maxTimeoutEntry := widget.NewEntry()
	maxTimeoutEntry.SetText(strconv.Itoa(cfg.MaxTimeout))
	maxTimeoutHint := makeHintText(
		"Maximum time (in seconds) a single job is allowed to run. Jobs exceeding this limit are terminated. Default: 180 s.",
	)

	// Sound on approval
	soundCheck := widget.NewCheck("Play a sound when an approval request arrives", nil)
	soundCheck.SetChecked(cfg.SoundOnApproval)

	stepContainers[1] = container.NewVBox(
		makeStepHeading("Approval & Timeouts", "When should you be asked to review a job, and how long should it wait?"),
		confirmRadio,
		widget.NewSeparator(),
		makeLabeledEntryWithHint("Confirm timeout (seconds)", confirmTimeoutEntry, confirmTimeoutHint),
		makeLabeledEntryWithHint("Max timeout (seconds)", maxTimeoutEntry, maxTimeoutHint),
		widget.NewSeparator(),
		soundCheck,
	)

	// ── Step 3: Network ───────────────────────────────────────────────────────
	networkRadio := widget.NewRadioGroup(
		[]string{"Allow all (recommended)", "Local only (localhost / LAN)", "Block all outbound network"},
		nil,
	)
	networkRadio.SetSelected("Allow all (recommended)")

	stepContainers[2] = container.NewVBox(
		makeStepHeading("Network Access", "Controls whether scripts can make outbound network requests."),
		networkRadio,
	)

	// ── Step 4: Summary ───────────────────────────────────────────────────────
	summaryLabel := widget.NewLabel("")
	summaryLabel.Wrapping = fyne.TextWrapWord
	summaryBg := canvas.NewRectangle(colorCard)
	summaryBg.CornerRadius = 4
	summaryBg.StrokeColor = colorBorder
	summaryBg.StrokeWidth = 1
	summaryBlock := container.NewStack(summaryBg, container.NewPadded(summaryLabel))

	stepContainers[3] = container.NewVBox(
		makeStepHeading("Review Settings", "Click Save & Start to write the config and launch the runner."),
		summaryBlock,
	)

	updateSummary := func() {
		confirmTO := confirmTimeoutEntry.Text
		if confirmTO == "" {
			confirmTO = strconv.Itoa(cfg.ConfirmTimeout)
		}
		maxTO := maxTimeoutEntry.Text
		if maxTO == "" {
			maxTO = strconv.Itoa(cfg.MaxTimeout)
		}
		soundStr := "off"
		if soundCheck.Checked {
			soundStr = "on"
		}
		summaryLabel.SetText(fmt.Sprintf(
			"Allowed paths:\n%s\n\nApproval:         %s\nConfirm timeout:  %s s\nMax timeout:      %s s\nSound on approval: %s\nNetwork:          %s",
			pathsEntry.Text, confirmRadio.Selected, confirmTO, maxTO, soundStr, networkRadio.Selected,
		))
	}

	// ── wiring ────────────────────────────────────────────────────────────────
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

		// Parse confirm timeout
		if v, err := strconv.Atoi(confirmTimeoutEntry.Text); err == nil && v > 0 {
			cfg.ConfirmTimeout = v
		}
		// Parse max timeout
		if v, err := strconv.Atoi(maxTimeoutEntry.Text); err == nil && v > 0 {
			cfg.MaxTimeout = v
		}

		cfg.SoundOnApproval = soundCheck.Checked

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

	// ── chrome ────────────────────────────────────────────────────────────────
	headerBg := canvas.NewRectangle(colorCard)
	headerBg.StrokeColor = colorBorder
	headerBg.StrokeWidth = 1
	dotsHeader := container.NewStack(headerBg, container.NewPadded(
		container.NewHBox(dotsRow, layout.NewSpacer(), container.NewWithoutLayout(stepLabel)),
	))

	navBg := canvas.NewRectangle(colorCard)
	navBg.StrokeColor = colorBorder
	navBg.StrokeWidth = 1
	navArea := container.NewStack(navBg, container.NewPadded(navRow))

	win.SetContent(container.NewBorder(dotsHeader, navArea, nil, nil,
		container.NewPadded(bodyHolder),
	))

	showStep(0)
	win.ShowAndRun()
	return saveErr
}

// makeHintText creates a small muted hint label.
func makeHintText(text string) *canvas.Text {
	t := canvas.NewText(text, colorLabel)
	t.TextSize = 11
	return t
}

// makeLabeledEntryWithHint creates a labeled entry with a hint below it.
func makeLabeledEntryWithHint(label string, entry *widget.Entry, hint *canvas.Text) fyne.CanvasObject {
	lbl := widget.NewLabel(label)
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	return container.NewVBox(
		lbl,
		entry,
		container.NewWithoutLayout(hint),
	)
}

// hasDisplay returns false when running on Linux without a display server.
func hasDisplay() bool {
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" || isNonLinux()
}
