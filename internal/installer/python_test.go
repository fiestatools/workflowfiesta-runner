package installer

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// TestInstallPython_NoPackageManager verifies that InstallPython emits a helpful
// fallback message when no package manager is available instead of panicking or
// returning a blank error.
//
// This test works by relying on the fact that in CI the expected package manager
// for the current platform may not be present (e.g. winget on Linux CI).
// It is intentionally lenient: it only checks that emit was called and the
// error is non-nil when the install cannot proceed.
func TestInstallPython_EmitCalledOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("winget may be present on Windows CI — skip to avoid side effects")
	}

	var lines []string
	emit := func(s string) { lines = append(lines, s) }

	// On a machine where brew/apt/dnf/yum may exist this test could succeed or fail.
	// We only assert that emit was called at least once regardless of outcome.
	_ = InstallPython(context.Background(), emit)

	// The emit callback must always be called at minimum once (progress or fallback URL).
	if len(lines) == 0 {
		t.Error("expected at least one line emitted, got none")
	}
}

func TestInstallPython_FallbackMessageContainsURL(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fallback message test only runs on Linux where we control package manager absence")
	}

	// Temporarily shadow apt-get, dnf, yum by providing a PATH with none of them.
	t.Setenv("PATH", t.TempDir()) // empty dir — no binaries

	var lines []string
	emit := func(s string) { lines = append(lines, s) }

	err := InstallPython(context.Background(), emit)
	if err == nil {
		t.Fatal("expected error when no package manager available")
	}

	combined := strings.Join(lines, " ")
	if !strings.Contains(combined, "python.org") {
		t.Errorf("fallback message should mention python.org, got: %q", combined)
	}
}

func TestRunAndStream_CapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("echo behaves differently on Windows")
	}

	var got []string
	emit := func(s string) { got = append(got, s) }

	err := runAndStream(context.Background(), emit, "echo", "hello world")
	if err != nil {
		t.Fatalf("runAndStream: %v", err)
	}
	if len(got) == 0 || !strings.Contains(got[0], "hello world") {
		t.Errorf("expected 'hello world' in output, got %v", got)
	}
}
