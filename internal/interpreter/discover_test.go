package interpreter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ── versionArgsFor ────────────────────────────────────────────────────────────

func TestVersionArgsFor_DefaultsToDoubleDash(t *testing.T) {
	for _, name := range []string{"python3", "node", "ruby", "bash"} {
		args := versionArgsFor(name)
		if len(args) != 1 || args[0] != "--version" {
			t.Errorf("versionArgsFor(%q) = %v, want [--version]", name, args)
		}
	}
}

func TestVersionArgsFor_GoUsesVersionSubcommand(t *testing.T) {
	args := versionArgsFor("go")
	if len(args) != 1 || args[0] != "version" {
		t.Errorf("versionArgsFor(go) = %v, want [version]", args)
	}
}

// ── queryVersion ─────────────────────────────────────────────────────────────

func TestQueryVersion_ReturnsFirstLineOnly(t *testing.T) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go binary not in PATH")
	}
	ctx := context.Background()
	v := queryVersion(ctx, goPath, []string{"version"})
	if v == "" {
		t.Error("expected non-empty version string from 'go version'")
	}
	if strings.Contains(v, "\n") {
		t.Errorf("version should be a single line; got %q", v)
	}
}

func TestQueryVersion_RespectsTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep not available as standalone binary on Windows")
	}
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not in PATH")
	}
	ctx := context.Background()
	// probeTimeout is 3 s; force a timeout by sleeping longer.
	// We patch probeTimeout locally for this test.
	orig := probeTimeout
	probeTimeout = 100 * time.Millisecond
	defer func() { probeTimeout = orig }()

	start := time.Now()
	v := queryVersion(ctx, sleepPath, []string{"10"})
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("queryVersion did not respect timeout; took %v", elapsed)
	}
	if v != "" {
		t.Errorf("timed-out probe should return empty version; got %q", v)
	}
}

func TestQueryVersion_InvalidBinary_ReturnsEmpty(t *testing.T) {
	v := queryVersion(context.Background(), "/no/such/binary", []string{"--version"})
	if v != "" {
		t.Errorf("missing binary should return empty version; got %q", v)
	}
}

// ── Discover (integration) ────────────────────────────────────────────────────

func TestDiscover_ReturnsOneEntryPerKnown(t *testing.T) {
	infos := Discover(context.Background())
	if len(infos) != len(known) {
		t.Errorf("Discover returned %d entries, want %d (one per known interpreter)", len(infos), len(known))
	}
}

func TestDiscover_AllEntriesHaveNames(t *testing.T) {
	for _, info := range Discover(context.Background()) {
		if info.Name == "" {
			t.Error("every Info should have a non-empty Name")
		}
	}
}

func TestDiscover_StatusIsValidVariant(t *testing.T) {
	for _, info := range Discover(context.Background()) {
		switch info.Status {
		case StatusFound, StatusMissing, StatusStub:
			// ok
		default:
			t.Errorf("Info{Name:%q} has unknown Status %d", info.Name, info.Status)
		}
	}
}

func TestDiscover_FoundEntryHasPath(t *testing.T) {
	for _, info := range Discover(context.Background()) {
		if info.Status == StatusFound && info.Path == "" {
			t.Errorf("Info{Name:%q, Status:Found} should have a non-empty Path", info.Name)
		}
		if info.Status != StatusFound && info.Path != "" {
			t.Errorf("Info{Name:%q, Status:%d} should have empty Path", info.Name, info.Status)
		}
	}
}

func TestDiscover_FakeInterpreterFoundViaPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell-script interpreter not usable on Windows")
	}

	dir := t.TempDir()
	fakeRuby := filepath.Join(dir, "ruby")
	script := "#!/bin/sh\necho 'ruby 9.9.9 (fake)'\n"
	if err := os.WriteFile(fakeRuby, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// Prepend our temp dir so LookPath finds the fake binary first.
	origPATH := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(filepath.ListSeparator)+origPATH)

	infos := Discover(context.Background())
	var ruby *Info
	for i := range infos {
		if infos[i].Name == "ruby" {
			ruby = &infos[i]
			break
		}
	}
	if ruby == nil {
		t.Fatal("ruby not found in Discover results")
	}
	if ruby.Status != StatusFound {
		t.Errorf("Status = %d, want StatusFound", ruby.Status)
	}
	if ruby.Path != fakeRuby {
		t.Errorf("Path = %q, want %q", ruby.Path, fakeRuby)
	}
	if !strings.Contains(ruby.Version, "9.9.9") {
		t.Errorf("Version = %q, want to contain '9.9.9'", ruby.Version)
	}
}

func TestDiscover_MissingInterpreterStatus(t *testing.T) {
	// Remove all real entries from PATH so nothing can be found.
	t.Setenv("PATH", t.TempDir()) // empty dir — nothing resolves

	infos := Discover(context.Background())
	for _, info := range infos {
		if info.Status == StatusFound {
			// Only acceptable if the interpreter was found via hardcoded candidate paths
			// (Windows only). On Unix this is always a LookPath failure.
			if runtime.GOOS != "windows" {
				t.Errorf("Info{Name:%q} reported StatusFound with empty PATH — unexpected", info.Name)
			}
		}
	}
}
