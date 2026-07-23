package installer

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"sync"
)

// InstallPython installs Python using the platform-appropriate package manager.
// Each line of output is forwarded to emit as it arrives.
// Returns nil on success, an error if the install failed or no package manager was found.
func InstallPython(ctx context.Context, emit func(string)) error {
	return installPython(ctx, emit)
}

// runAndStream runs name with args, forwarding each line of combined stdout+stderr to emit.
func runAndStream(ctx context.Context, emit func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	pipe := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			emit(sc.Text())
		}
	}
	go pipe(stdout)
	go pipe(stderr)
	wg.Wait()
	return cmd.Wait()
}
