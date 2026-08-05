//go:build windows

package executor

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"

	log "github.com/sirupsen/logrus"

	"workflowfiesta-runner/internal/interpreter"
	"workflowfiesta-runner/internal/localconfig"
)

const createNoWindow = 0x08000000
const bashNotFoundMsg = "bash not found — install Git for Windows to run scripts: https://git-scm.com/download/win"

func runWithSandbox(ctx context.Context, cfg *localconfig.LocalConfig, script string, env []string, dir string, outputChan chan<- string) (int, error) {
	bash, ok := interpreter.ResolveBash()
	if !ok {
		log.Warn("[local] " + bashNotFoundMsg)
		if outputChan != nil {
			outputChan <- bashNotFoundMsg + "\n"
		}
		return -1, fmt.Errorf("%s", bashNotFoundMsg)
	}

	log.Infof("[local] using bash shell: %s", bash)
	scriptPath, cleanup, err := writeScriptTempFile(script)
	if err != nil {
		return -1, err
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", scriptPath)
	cmd.Env = env
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return execAndStream(ctx, cmd, outputChan)
}
