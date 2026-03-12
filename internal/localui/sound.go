//go:build !nolocalui

package localui

import (
	"os/exec"
	"runtime"
)

// playApprovalSound plays a notification sound to alert the user an approval is needed.
func playApprovalSound() {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("afplay", "/System/Library/Sounds/Funk.aiff").Start()
	case "windows":
		exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"[System.Media.SystemSounds]::Exclamation.Play()").Start()
	default:
		if err := exec.Command("paplay", "/usr/share/sounds/freedesktop/stereo/bell.oga").Start(); err != nil {
			exec.Command("printf", "\a").Start()
		}
	}
}
