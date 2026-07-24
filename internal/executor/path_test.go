package executor

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"workflowfiesta-runner/internal/localconfig"
)

// ── sanitizeWindowsPATH (platform-agnostic behaviour) ────────────────────────

func TestSanitizeWindowsPATH_NoOp_OnNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows behaviour tested here; Windows-specific tests are in path_windows_test.go")
	}
	input := "/usr/local/bin:/usr/bin:/bin"
	if got := sanitizeWindowsPATH(input); got != input {
		t.Errorf("sanitizeWindowsPATH should be a no-op on non-Windows; got %q", got)
	}
}

func TestSanitizeWindowsPATH_EmptyString_NoOp(t *testing.T) {
	if got := sanitizeWindowsPATH(""); got != "" {
		t.Errorf("empty PATH should return empty, got %q", got)
	}
}

// ── prependDiscoveredDirs ─────────────────────────────────────────────────────

func TestPrependDiscoveredDirs_EmptyMap_ReturnsBase(t *testing.T) {
	e := testExecutor()
	base := "/usr/bin:/bin"
	if got := e.prependDiscoveredDirs(base); got != base {
		t.Errorf("empty Interpreters should return base unchanged; got %q", got)
	}
}

func TestPrependDiscoveredDirs_NilMap_ReturnsBase(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.Interpreters = nil
	})
	base := "/usr/bin:/bin"
	if got := e.prependDiscoveredDirs(base); got != base {
		t.Errorf("nil Interpreters should return base unchanged; got %q", got)
	}
}

func TestPrependDiscoveredDirs_PrependsSingleDir(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.Interpreters = map[string]string{
			"python3": "/opt/python/bin/python3",
		}
	})
	base := "/usr/bin:/bin"
	got := e.prependDiscoveredDirs(base)

	wantPrefix := filepath.FromSlash("/opt/python/bin")
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("expected PATH to start with %q, got %q", wantPrefix, got)
	}
	if !strings.Contains(got, base) {
		t.Errorf("base PATH should still be present in %q", got)
	}
}

func TestPrependDiscoveredDirs_DeduplicatesDirs(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.Interpreters = map[string]string{
			"python3": "/opt/python/bin/python3",
			"pip3":    "/opt/python/bin/pip3", // same dir as python3
		}
	})
	got := e.prependDiscoveredDirs("/usr/bin")
	dir := filepath.FromSlash("/opt/python/bin")
	count := strings.Count(got, dir)
	if count != 1 {
		t.Errorf("duplicate dirs should be collapsed to one; found %d occurrences of %q in %q", count, dir, got)
	}
}

func TestPrependDiscoveredDirs_SkipsEmptyPaths(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.Interpreters = map[string]string{
			"python3": "",
			"node":    "/usr/local/bin/node",
		}
	})
	base := "/usr/bin"
	got := e.prependDiscoveredDirs(base)
	// filepath.Dir("") returns "." which we don't want prepended
	if strings.Contains(got, string(filepath.ListSeparator)+"."+string(filepath.ListSeparator)) ||
		strings.HasPrefix(got, "."+string(filepath.ListSeparator)) {
		t.Errorf("empty path should not add '.' to PATH; got %q", got)
	}
	if !strings.Contains(got, base) {
		t.Errorf("base PATH should still be present in %q", got)
	}
}

func TestPrependDiscoveredDirs_BaseIsAlwaysLast(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.Interpreters = map[string]string{
			"node": "/opt/node/bin/node",
		}
	})
	base := "/usr/bin:/bin"
	got := e.prependDiscoveredDirs(base)
	if !strings.HasSuffix(got, base) {
		t.Errorf("base PATH should be at the end; got %q", got)
	}
}
