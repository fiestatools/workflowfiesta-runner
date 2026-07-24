//go:build !windows

package installer

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

func installPython(ctx context.Context, emit func(string)) error {
	switch runtime.GOOS {
	case "darwin":
		brew, err := exec.LookPath("brew")
		if err != nil {
			emit("Homebrew not found — install Python manually: https://www.python.org/downloads/")
			return fmt.Errorf("brew not found")
		}
		return runAndStream(ctx, emit, brew, "install", "python3")
	default:
		if apt, err := exec.LookPath("apt-get"); err == nil {
			return runAndStream(ctx, emit, apt, "install", "-y", "python3")
		}
		if dnf, err := exec.LookPath("dnf"); err == nil {
			return runAndStream(ctx, emit, dnf, "install", "-y", "python3")
		}
		if yum, err := exec.LookPath("yum"); err == nil {
			return runAndStream(ctx, emit, yum, "install", "-y", "python3")
		}
		emit("No supported package manager found — install Python manually: https://www.python.org/downloads/")
		return fmt.Errorf("no supported package manager found")
	}
}
