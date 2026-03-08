//go:build !linux && !darwin

package executor

import (
	"context"
	"os/exec"
	"runtime"

	log "github.com/sirupsen/logrus"

	"workflowfiesta-runner/internal/localconfig"
)

// runWithSandbox on unsupported platforms applies CWD restriction and a minimal
// environment, but cannot enforce path or network isolation at the OS level.
func runWithSandbox(ctx context.Context, cfg *localconfig.LocalConfig, script string, env []string, dir string, outputChan chan<- string) (int, error) {
	if cfg.Sandbox == "kernel" {
		log.Warnf("[local] kernel-level sandboxing is not supported on %s; running with process-level controls only", runtime.GOOS)
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// On Windows, use PowerShell for script execution.
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", script)
	}
	cmd.Env = env
	cmd.Dir = dir
	return execAndStream(ctx, cmd, outputChan)
}
