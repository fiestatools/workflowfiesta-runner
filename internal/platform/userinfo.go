package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// DocumentsPath returns a best-effort path to the user's Documents directory.
// This is used for UX hints and optional server-side telemetry; it should not
// be treated as a guaranteed existing path.
func DocumentsPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "Documents")
}

// Shell returns the user's default shell path, if available.
func Shell() string {
	if runtime.GOOS == "windows" {
		if v := os.Getenv("COMSPEC"); v != "" {
			return v
		}
	}
	return os.Getenv("SHELL")
}
