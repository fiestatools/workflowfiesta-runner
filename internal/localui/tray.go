//go:build !nolocalui

package localui

import (
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
)

// approveGreen is the colour used for the Allow button background.
var approveGreen = color.NRGBA{R: 52, G: 168, B: 83, A: 255}

var (
	fyneOnce   sync.Once
	fyneApp    fyne.App
	appFactory = func() fyne.App { return app.New() } // overridden by tests
)

// getApp returns the process-wide fyne.App, creating it on first call.
func getApp() fyne.App {
	fyneOnce.Do(func() { fyneApp = appFactory() })
	return fyneApp
}

// QuitApp stops the Fyne event loop.
func QuitApp() {
	if fyneApp != nil {
		fyneApp.Quit()
	}
}

// StartTray sets up the system tray icon and starts the Fyne event loop.
// Blocks until the event loop exits. onStop is called on "Stop Runner".
func StartTray(runnerName string, onStop func()) {
	a := getApp()
	if desk, ok := a.(desktop.App); ok {
		menu := fyne.NewMenu("WorkflowFiesta",
			fyne.NewMenuItem(runnerName+" · running", nil),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Stop Runner", func() {
				if onStop != nil {
					onStop()
				}
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Quit", func() { a.Quit() }),
		)
		desk.SetSystemTrayMenu(menu)
		desk.SetSystemTrayIcon(runnerIcon())
	}
	a.Run()
}

func runnerIcon() fyne.Resource {
	return fyne.NewStaticResource("runner-icon.png", defaultIconPNG)
}

// defaultIconPNG is a minimal 16×16 green square PNG.
var defaultIconPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x10,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x91, 0x68,
	0x36, 0x00, 0x00, 0x00, 0x18, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x62, 0x34, 0xa8, 0x53, 0xfc,
	0xff, 0xff, 0x3f, 0x03, 0x03, 0x03, 0x00, 0x00,
	0x00, 0xff, 0xff, 0x03, 0x00, 0x03, 0x6d, 0x01,
	0x2e, 0xb4, 0x3f, 0xf7, 0xa2, 0x00, 0x00, 0x00,
	0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60,
	0x82,
}
