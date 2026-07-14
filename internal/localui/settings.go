//go:build !nolocalui

package localui

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"workflowfiesta-runner/internal/localconfig"
)

// OpenSettingsWindow opens the settings window and returns it.
// onSave is called with the updated config when the user clicks "Save & Close".
// onResetRunner is called when the user confirms "Reset Runner"; deleteAuditLogs
// and deleteScripts reflect the checkboxes the user ticked.
func OpenSettingsWindow(cfg *localconfig.LocalConfig, configPath string, onSave func(*localconfig.LocalConfig), onResetRunner func(deleteAuditLogs, deleteScripts bool) error) fyne.Window {
	a := getApp()
	win := a.NewWindow("WorkflowFiesta Runner · Settings")
	win.Resize(fyne.NewSize(600, 520))
	win.CenterOnScreen()

	// ── Execution section ─────────────────────────────────────────────────────
	confirmOptions := []string{"Always ask", "Risky operations only", "Never ask"}
	confirmMap := map[string]string{
		"Always ask":            "always",
		"Risky operations only": "destructive",
		"Never ask":             "never",
	}
	confirmRevMap := map[string]string{
		"always":      "Always ask",
		"destructive": "Risky operations only",
		"never":       "Never ask",
	}
	confirmSelected := confirmRevMap[cfg.Confirm]
	if confirmSelected == "" {
		confirmSelected = "Risky operations only"
	}
	confirmRadio := widget.NewRadioGroup(confirmOptions, nil)
	confirmRadio.SetSelected(confirmSelected)

	confirmTimeoutEntry := widget.NewEntry()
	confirmTimeoutEntry.SetText(strconv.Itoa(cfg.ConfirmTimeout))

	neverTimeoutCheck := widget.NewCheck("Never timeout (wait indefinitely)", func(checked bool) {
		if checked {
			confirmTimeoutEntry.Disable()
		} else {
			confirmTimeoutEntry.Enable()
		}
	})
	neverTimeoutCheck.SetChecked(cfg.ConfirmNeverTimeout)
	if cfg.ConfirmNeverTimeout {
		confirmTimeoutEntry.Disable()
	}

	maxTimeoutEntry := widget.NewEntry()
	maxTimeoutEntry.SetText(strconv.Itoa(cfg.MaxTimeout))

	executionSection := container.NewVBox(
		makeSectionLabel("Execution"),
		widget.NewLabel("Confirm mode:"),
		confirmRadio,
		makeLabeledEntry("Confirm timeout (seconds)", confirmTimeoutEntry),
		neverTimeoutCheck,
		makeLabeledEntry("Max timeout (seconds)", maxTimeoutEntry),
	)

	// ── Sound section ─────────────────────────────────────────────────────────
	soundCheck := widget.NewCheck("Play sound on approval request", nil)
	soundCheck.SetChecked(cfg.SoundOnApproval)

	soundSection := container.NewVBox(
		makeSectionLabel("Sound"),
		soundCheck,
	)

	// ── Security section ──────────────────────────────────────────────────────
	allowedPathsEntry := widget.NewMultiLineEntry()
	allowedPathsEntry.SetPlaceHolder("One path per line, e.g. ~/projects")
	allowedPathsEntry.SetText(strings.Join(cfg.AllowedPaths, "\n"))
	allowedPathsEntry.SetMinRowsVisible(3)

	networkOptions := []string{"Allow all", "Localhost only", "Block all"}
	networkMap := map[string]string{
		"Allow all":      "all",
		"Localhost only": "localhost",
		"Block all":      "none",
	}
	networkRevMap := map[string]string{
		"all":       "Allow all",
		"localhost": "Localhost only",
		"none":      "Block all",
	}
	networkSelected := networkRevMap[cfg.Network]
	if networkSelected == "" {
		networkSelected = "Allow all"
	}
	networkRadio := widget.NewRadioGroup(networkOptions, nil)
	networkRadio.SetSelected(networkSelected)

	securitySection := container.NewVBox(
		makeSectionLabel("Security"),
		widget.NewLabel("Allowed paths (one per line):"),
		allowedPathsEntry,
		widget.NewLabel("Network mode:"),
		networkRadio,
	)

	// ── Permissions (advanced) section ────────────────────────────────────────
	// Always-allowed patterns list with remove buttons
	alwaysAllowedPatterns := make([]string, len(cfg.AlwaysAllowedPatterns))
	copy(alwaysAllowedPatterns, cfg.AlwaysAllowedPatterns)

	var patternList *widget.List
	patternList = widget.NewList(
		func() int { return len(alwaysAllowedPatterns) },
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewLabel("fingerprint"),
				layout.NewSpacer(),
				widget.NewButton("Remove", nil),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			row := obj.(*fyne.Container)
			lbl := row.Objects[0].(*widget.Label)
			btn := row.Objects[2].(*widget.Button)
			fp := alwaysAllowedPatterns[id]
			if len(fp) > 16 {
				lbl.SetText(fp[:16] + "…")
			} else {
				lbl.SetText(fp)
			}
			btn.OnTapped = func() {
				alwaysAllowedPatterns = append(alwaysAllowedPatterns[:id], alwaysAllowedPatterns[id+1:]...)
				patternList.Refresh()
			}
		},
	)
	patternScroll := container.NewScroll(patternList)
	patternScroll.SetMinSize(fyne.NewSize(550, 100))

	permissionsSection := container.NewVBox(
		makeSectionLabel("Permissions (Advanced)"),
		widget.NewLabel("Script fingerprints approved with 'Always allow':"),
		patternScroll,
		widget.NewLabel("Session allowlist clears when runner restarts."),
	)

	// ── Approval popup position section ──────────────────────────────────────
	prefs := a.Preferences()
	approvalXEntry := widget.NewEntry()
	approvalXEntry.SetText(strconv.FormatFloat(prefs.FloatWithFallback("approval.window.x", -1), 'f', 0, 64))

	approvalYEntry := widget.NewEntry()
	approvalYEntry.SetText(strconv.FormatFloat(prefs.FloatWithFallback("approval.window.y", -1), 'f', 0, 64))

	positionSection := container.NewVBox(
		makeSectionLabel("Approval Popup Position"),
		makeLabeledEntry("X position (pixels, -1 = center)", approvalXEntry),
		makeLabeledEntry("Y position (pixels, -1 = center)", approvalYEntry),
	)

	// ── Actions section ───────────────────────────────────────────────────────
	openAuditBtn := newButton("Open Audit Log", func() {
		auditPath := cfg.AuditLog
		if auditPath == "" {
			return
		}
		u, err := url.Parse("file://" + auditPath)
		if err == nil {
			a.OpenURL(u)
		}
	})
	openAuditBtn.Importance = widget.LowImportance

	resetRunnerBtn := newButton("Reset Runner", func() {
		if onResetRunner == nil {
			return
		}
		ShowResetRunnerConfirm(win, func(deleteAuditLogs, deleteScripts bool) {
			go func() {
				if err := onResetRunner(deleteAuditLogs, deleteScripts); err != nil {
					fyne.Do(func() {
						dialog.ShowError(fmt.Errorf("reset runner: %w", err), win)
					})
				}
			}()
		})
	})
	resetRunnerBtn.Importance = widget.DangerImportance

	actionsSection := container.NewVBox(
		makeSectionLabel("Actions"),
		openAuditBtn,
		resetRunnerBtn,
	)

	// ── Scrollable body ───────────────────────────────────────────────────────
	body := container.NewVBox(
		executionSection,
		widget.NewSeparator(),
		soundSection,
		widget.NewSeparator(),
		securitySection,
		widget.NewSeparator(),
		permissionsSection,
		widget.NewSeparator(),
		positionSection,
		widget.NewSeparator(),
		actionsSection,
	)
	scroll := container.NewVScroll(body)

	// ── Footer buttons ────────────────────────────────────────────────────────
	cancelBtn := newButton("Cancel", func() { win.Close() })
	cancelBtn.Importance = widget.LowImportance

	saveBtn := newButton("Save & Close", func() {
		// Build updated config
		updated := *cfg

		updated.Confirm = confirmMap[confirmRadio.Selected]
		updated.ConfirmNeverTimeout = neverTimeoutCheck.Checked
		if ct, err := strconv.Atoi(strings.TrimSpace(confirmTimeoutEntry.Text)); err == nil && ct > 0 {
			updated.ConfirmTimeout = ct
		}
		if mt, err := strconv.Atoi(strings.TrimSpace(maxTimeoutEntry.Text)); err == nil && mt > 0 {
			updated.MaxTimeout = mt
		}
		updated.SoundOnApproval = soundCheck.Checked
		updated.Network = networkMap[networkRadio.Selected]
		updated.AlwaysAllowedPatterns = alwaysAllowedPatterns

		// Parse allowed paths
		lines := strings.Split(allowedPathsEntry.Text, "\n")
		var paths []string
		for _, l := range lines {
			if t := strings.TrimSpace(l); t != "" {
				paths = append(paths, t)
			}
		}
		if len(paths) > 0 {
			updated.AllowedPaths = paths
		}

		// Save approval window position to preferences
		if x, err := strconv.ParseFloat(strings.TrimSpace(approvalXEntry.Text), 64); err == nil {
			prefs.SetFloat("approval.window.x", x)
		}
		if y, err := strconv.ParseFloat(strings.TrimSpace(approvalYEntry.Text), 64); err == nil {
			prefs.SetFloat("approval.window.y", y)
		}

		// Persist to disk
		_ = localconfig.Save(&updated, configPath)

		if onSave != nil {
			onSave(&updated)
		}
		win.Close()
	})
	saveBtn.Importance = widget.HighImportance

	footer := container.NewPadded(container.NewHBox(layout.NewSpacer(), cancelBtn, saveBtn))

	win.SetContent(container.NewBorder(nil, footer, nil, nil, scroll))
	win.Show()
	return win
}

// makeLabeledEntry returns a VBox with a label and an entry widget.
func makeLabeledEntry(label string, entry *widget.Entry) fyne.CanvasObject {
	return container.NewVBox(
		widget.NewLabel(label),
		entry,
	)
}
