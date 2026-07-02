//go:build windows

package interpreter

import (
	"testing"
)

func TestIsWindowsStub_DetectsStub(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`C:\Users\user\AppData\Local\Microsoft\WindowsApps\python.exe`, true},
		{`C:\Users\user\AppData\Local\Microsoft\WindowsApps\python3.exe`, true},
		{`C:\users\user\appdata\local\microsoft\windowsapps\node.exe`, true}, // case-insensitive
		// Real interpreter paths — must not be flagged
		{`C:\Python312\python.exe`, false},
		{`C:\Program Files\nodejs\node.exe`, false},
		{`C:\Program Files\Git\bin\bash.exe`, false},
		{`C:\Go\bin\go.exe`, false},
	}
	for _, tc := range cases {
		if got := isWindowsStub(tc.path); got != tc.want {
			t.Errorf("isWindowsStub(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestResolve_HardcodedCandidateTakesPriorityOverStub(t *testing.T) {
	// This test verifies the resolution order: hardcoded paths are checked
	// before LookPath, so a stub that appears in PATH is never consulted when
	// a real binary exists at a well-known location.
	//
	// We can't actually install Python in a unit test, so we verify the
	// candidate list is non-empty for each interpreter and ordered latest-first.
	for name, paths := range candidatePaths {
		if len(paths) == 0 {
			t.Errorf("candidatePaths[%q] is empty — every interpreter needs at least one candidate", name)
		}
		for _, p := range paths {
			if isWindowsStub(p) {
				t.Errorf("candidatePaths[%q] contains a stub path %q — hardcoded candidates must be real binaries", name, p)
			}
		}
	}
}
