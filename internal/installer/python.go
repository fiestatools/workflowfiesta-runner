package installer

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"sync"
)

// InstallPython installs Python using the platform-appropriate package manager.
func InstallPython(ctx context.Context, emit func(string)) error {
	return installPython(ctx, emit)
}

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
