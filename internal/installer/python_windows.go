//go:build windows

package installer

import (
	"context"
	"fmt"
	"os/exec"
)

func installPython(ctx context.Context, emit func(string)) error {
	winget, err := exec.LookPath("winget")
	if err != nil {
		emit("winget not available — install Python manually: https://www.python.org/downloads/")
		return fmt.Errorf("winget not found")
	}
	return runAndStream(ctx, emit, winget,
		"install", "--id", "Python.Python.3",
		"--scope", "user",
		"--silent",
		"--accept-package-agreements",
		"--accept-source-agreements",
	)
}
