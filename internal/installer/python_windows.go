//go:build windows

package installer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// pythonInstallerVersion is the version downloaded directly from python.org
// when winget is unavailable. Kept in sync with the "Python.Python.3.13"
// winget package id used in the primary install path.
const pythonInstallerVersion = "3.13.1"

func installPython(ctx context.Context, emit func(string)) error {
	if winget, err := exec.LookPath("winget"); err == nil {
		return runAndStream(ctx, emit, winget,
			"install", "--id", "Python.Python.3.13",
			"--scope", "user",
			"--silent",
			"--accept-package-agreements",
			"--accept-source-agreements",
		)
	}

	emit("winget not available — downloading Python installer directly")
	return installPythonViaDownload(ctx, emit)
}

// installPythonViaDownload is the fallback used when winget is missing. It
// downloads the official python.org installer with PowerShell's
// Invoke-WebRequest, runs it silently for the current user, and prepends the
// resulting install directories to PATH so the interpreter is usable in this
// process without requiring a shell restart.
func installPythonViaDownload(ctx context.Context, emit func(string)) error {
	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	url := fmt.Sprintf("https://www.python.org/ftp/python/%s/python-%s-%s.exe",
		pythonInstallerVersion, pythonInstallerVersion, arch)

	installerPath := filepath.Join(os.TempDir(), fmt.Sprintf("python-%s-%s.exe", pythonInstallerVersion, arch))
	defer os.Remove(installerPath)

	emit("Downloading Python from " + url)
	dlScript := fmt.Sprintf(
		"$ProgressPreference = 'SilentlyContinue'; Invoke-WebRequest -Uri '%s' -OutFile '%s'",
		url, installerPath,
	)
	if err := runAndStream(ctx, emit, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", dlScript); err != nil {
		emit("download failed — install Python manually: https://www.python.org/downloads/")
		return fmt.Errorf("iwr download failed: %w", err)
	}

	emit("Installing Python (silent)")
	if err := runAndStream(ctx, emit, installerPath,
		"/quiet",
		"InstallAllUsers=0",
		"PrependPath=1",
		"Include_launcher=0",
	); err != nil {
		return fmt.Errorf("installer failed: %w", err)
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return nil
	}
	installDir := filepath.Join(localAppData, "Programs", "Python", "Python"+versionDirSuffix(pythonInstallerVersion))
	scriptsDir := filepath.Join(installDir, "Scripts")

	emit("Updating PATH for this session")
	newPath := installDir + string(os.PathListSeparator) + scriptsDir + string(os.PathListSeparator) + os.Getenv("PATH")
	if err := os.Setenv("PATH", newPath); err != nil {
		emit(fmt.Sprintf("warning: failed to update PATH: %v", err))
	}

	return nil
}

// versionDirSuffix converts "3.13.1" to "313", matching python.org's
// installer directory naming (e.g. LOCALAPPDATA\Programs\Python\Python313).
func versionDirSuffix(version string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return strings.ReplaceAll(version, ".", "")
	}
	return parts[0] + parts[1]
}
