//go:build windows

package installer

import (
	"context"
	"strings"
	"testing"
)

// TestInstallPython_FallsBackWhenWingetMissing verifies that InstallPython
// takes the direct-download path when winget cannot be found on PATH.
//
// It hides winget by pointing PATH at an empty directory, and passes an
// already-canceled context so runAndStream's exec.CommandContext returns
// ctx.Err() immediately in Start(), without ever spawning powershell.exe or
// touching the network. This confirms routing into installPythonViaDownload
// without performing a real download or install.
func TestInstallPython_FallsBackWhenWingetMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir — winget.LookPath fails

	var lines []string
	emit := func(s string) { lines = append(lines, s) }

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled: cmd.Start() short-circuits, no process runs

	err := InstallPython(ctx, emit)
	if err == nil {
		t.Fatal("expected error since the canceled context prevents the download from completing")
	}

	combined := strings.Join(lines, " | ")
	if !strings.Contains(combined, "winget not available") {
		t.Errorf("expected fallback message about winget, got: %q", combined)
	}
	if !strings.Contains(combined, "Downloading Python from") {
		t.Errorf("expected to reach the download step, got: %q", combined)
	}
}

func TestVersionDirSuffix(t *testing.T) {
	cases := map[string]string{
		"3.13.1": "313",
		"3.9.0":  "39",
		"3.10.4": "310",
	}
	for version, want := range cases {
		if got := versionDirSuffix(version); got != want {
			t.Errorf("versionDirSuffix(%q) = %q, want %q", version, got, want)
		}
	}
}
