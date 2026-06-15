//go:build !nolocalui

package localui

import (
	"fmt"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"

	"workflowfiesta-runner/internal/localconfig"
)

var (
	fyneOnce   sync.Once
	fyneApp    fyne.App
	appFactory = func() fyne.App { return app.NewWithID("com.workflowfiesta.runner") } // overridden by tests
)

// getApp returns the process-wide fyne.App, creating it on first call.
func getApp() fyne.App {
	fyneOnce.Do(func() {
		fyneApp = appFactory()
		fyneApp.Settings().SetTheme(wfTheme{base: theme.DarkTheme()})
		fyneApp.SetIcon(LogoResource())
	})
	return fyneApp
}

// QuitApp stops the Fyne event loop.
func QuitApp() {
	if fyneApp != nil {
		fyne.Do(func() { fyneApp.Quit() })
	}
}

// SetupTray configures the system tray icon and menu without starting the event
// loop. Call this before a.Run() when you need to manage the loop yourself.
// cfg and onConfigSaved are used for the Settings menu item; both may be nil.
func SetupTray(runnerName string, onStop func(), onClearConfig func(deleteAuditLogs, deleteScripts bool) error, sw *StatusWindow, cfg *localconfig.LocalConfig, onConfigSaved func(*localconfig.LocalConfig)) {
	a := getApp()
	if desk, ok := a.(desktop.App); ok {
		var showItem *fyne.MenuItem
		if sw != nil {
			showItem = fyne.NewMenuItem("Open Status Window", func() { sw.Show() })
		}

		settingsItem := fyne.NewMenuItem("Settings…", func() {
			if cfg != nil {
				OpenSettingsWindow(cfg, localconfig.DefaultPath(), onConfigSaved, onClearConfig)
			}
		})
		clearCfgItem := fyne.NewMenuItem("Clear Configuration…", func() {
			if onClearConfig == nil {
				return
			}
			runClear := func(deleteAuditLogs, deleteScripts bool) {
				if err := onClearConfig(deleteAuditLogs, deleteScripts); err != nil {
					if sw != nil {
						dialog.ShowError(fmt.Errorf("reset runner: %w", err), sw.win)
					}
				}
			}
			if sw == nil {
				runClear(false, false)
				return
			}
			ShowResetRunnerConfirm(sw.win, runClear)
		})

		items := []*fyne.MenuItem{
			fyne.NewMenuItem("WorkflowFiesta Runner · running", nil),
			fyne.NewMenuItemSeparator(),
			settingsItem,
			clearCfgItem,
		}
		if showItem != nil {
			items = append(items, showItem)
		}
		items = append(items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Stop Runner", func() {
				if onStop != nil {
					onStop()
				}
			}),
			fyne.NewMenuItem("Quit WorkflowFiesta Runner", func() { a.Quit() }),
		)
		menu := fyne.NewMenu("WorkflowFiesta", items...)
		desk.SetSystemTrayMenu(menu)
		desk.SetSystemTrayIcon(runnerIcon())
	}
}

// StartTray sets up the system tray icon and starts the Fyne event loop.
// Blocks until the event loop exits. onStop is called on "Stop Runner".
// sw is the StatusWindow to show/re-open from the tray menu; may be nil.
// cfg and onConfigSaved are passed to the Settings menu item.
func StartTray(runnerName string, onStop func(), onClearConfig func(deleteAuditLogs, deleteScripts bool) error, sw *StatusWindow, cfg *localconfig.LocalConfig, onConfigSaved func(*localconfig.LocalConfig)) {
	SetupTray(runnerName, onStop, onClearConfig, sw, cfg, onConfigSaved)
	getApp().Run()
}

func runnerIcon() fyne.Resource {
	return LogoResource()
}
