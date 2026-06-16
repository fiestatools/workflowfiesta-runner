//go:build !nolocalui

package localui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// ShowResetRunnerConfirm asks the user to confirm logout/reset and which
// optional data to delete. onConfirm receives (deleteAuditLogs, deleteScripts).
func ShowResetRunnerConfirm(parent fyne.Window, onConfirm func(deleteAuditLogs, deleteScripts bool)) {
	if onConfirm == nil {
		return
	}

	body := widget.NewLabel(
		"This will unregister this runner from the server and clear local runner configuration.\n\n" +
			"You will need to register again to continue.",
	)
	body.Wrapping = fyne.TextWrapWord

	deleteAuditCheck := widget.NewCheck("Delete audit logs", nil)
	deleteAuditCheck.SetChecked(false)

	deleteScriptsCheck := widget.NewCheck("Delete local script cache", nil)
	deleteScriptsCheck.SetChecked(false)

	content := container.NewVBox(
		body,
		deleteAuditCheck,
		deleteScriptsCheck,
	)

	dialog.ShowCustomConfirm(
		"Reset Runner",
		"Reset",
		"Cancel",
		content,
		func(ok bool) {
			if ok {
				onConfirm(deleteAuditCheck.Checked, deleteScriptsCheck.Checked)
			}
		},
		parent,
	)
}
