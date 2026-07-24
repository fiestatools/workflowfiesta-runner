package interpreter

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// probeTimeout is the per-interpreter deadline for the version probe.
// Long enough to cover a cold disk read; short enough not to hang the wizard.
// Declared as a var so tests can override it.
var probeTimeout = 3 * time.Second

// known is the ordered list of interpreter names to discover.
var known = []string{"bash", "python3", "python", "node", "ruby", "go"}

// versionArgs overrides the arguments used to query the version for specific
// interpreters. Defaults to []string{"--version"} if not listed here.
var versionArgs = map[string][]string{
	"go": {"version"},
}

func discover(ctx context.Context) []Info {
	results := make([]Info, len(known))
	var wg sync.WaitGroup
	for i, name := range known {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			results[i] = probe(ctx, name)
		}(i, name)
	}
	wg.Wait()
	return results
}

func probe(ctx context.Context, name string) Info {
	path, status := resolve(name)
	if status != StatusFound {
		return Info{Name: name, Status: status}
	}
	version := queryVersion(ctx, path, versionArgsFor(name))
	return Info{Name: name, Path: path, Version: version, Status: StatusFound}
}

func versionArgsFor(name string) []string {
	if args, ok := versionArgs[name]; ok {
		return args
	}
	return []string{"--version"}
}

func queryVersion(ctx context.Context, path string, args []string) string {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}
