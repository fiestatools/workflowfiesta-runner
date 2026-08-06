//go:build windows

package installer

import (
	"context"
	"strings"
	"testing"
)
.
func TestInstallGitBash_FallsBackWhenWingetMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	var lines []string
	emit := func(s string) { lines = append(lines, s) }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := InstallGitBash(ctx, emit)
	if err == nil {
		t.Fatal("expected error since the canceled context prevents the download from completing")
	}

	combined := strings.Join(lines, " | ")
	if !strings.Contains(combined, "winget not available") {
		t.Errorf("expected fallback message about winget, got: %q", combined)
	}
	if !strings.Contains(combined, "Downloading Git for Windows from") {
		t.Errorf("expected to reach the download step, got: %q", combined)
	}
}
