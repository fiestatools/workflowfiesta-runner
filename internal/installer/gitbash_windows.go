//go:build windows

package installer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const (
	gitForWindowsVersion = "2.55.0.3"
	gitForWindowsTag     = "v2.55.0.windows.3"
)

func installGitBash(ctx context.Context, emit func(string)) error {
	if winget, err := exec.LookPath("winget"); err == nil {
		return runAndStream(ctx, emit, winget,
			"install", "--id", "Git.Git",
			"--source", "winget",
			"--scope", "user",
			"--silent",
			"--accept-package-agreements",
			"--accept-source-agreements",
		)
	}

	emit("winget not available — downloading Git for Windows installer directly")
	return installGitBashViaDownload(ctx, emit)
}

// installGitBashViaDownload is the fallback used when winget is missing.
func installGitBashViaDownload(ctx context.Context, emit func(string)) error {
	arch := "64-bit"
	if runtime.GOARCH == "386" {
		arch = "32-bit"
	}
	url := fmt.Sprintf(
		"https://github.com/git-for-windows/git/releases/download/%s/Git-%s-%s.exe",
		gitForWindowsTag, gitForWindowsVersion, arch,
	)

	installerPath := filepath.Join(os.TempDir(), fmt.Sprintf("git-for-windows-%s-%s.exe", gitForWindowsVersion, arch))
	defer os.Remove(installerPath)

	emit("Downloading Git for Windows from " + url)
	dlScript := fmt.Sprintf(
		"$ProgressPreference = 'SilentlyContinue'; Invoke-WebRequest -Uri '%s' -OutFile '%s'",
		url, installerPath,
	)
	if err := runAndStream(ctx, emit, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", dlScript); err != nil {
		emit("download failed — install Git for Windows manually: https://git-scm.com/download/win")
		return fmt.Errorf("iwr download failed: %w", err)
	}

	emit("Installing Git for Windows (silent, current user only)")
	if err := runAndStream(ctx, emit, installerPath,
		"/VERYSILENT",
		"/NORESTART",
		"/NOCANCEL",
		"/SP-",
		"/CURRENTUSER",
	); err != nil {
		return fmt.Errorf("installer failed: %w", err)
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return nil
	}
	binDir := filepath.Join(localAppData, "Programs", "Git", "bin")

	emit("Updating PATH for this session")
	newPath := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	if err := os.Setenv("PATH", newPath); err != nil {
		emit(fmt.Sprintf("warning: failed to update PATH: %v", err))
	}

	return nil
}
