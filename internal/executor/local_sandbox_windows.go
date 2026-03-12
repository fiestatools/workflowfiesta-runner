//go:build windows

package executor

import (
	"context"
	"os/exec"
	"syscall"

	"workflowfiesta-runner/internal/localconfig"
)

const createNoWindow = 0x08000000

func runWithSandbox(ctx context.Context, cfg *localconfig.LocalConfig, script string, env []string, dir string, outputChan chan<- string) (int, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = env
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return execAndStream(ctx, cmd, outputChan)
}
