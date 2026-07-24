//go:build windows

package executor

import (
	"strings"
	"testing"
)

func TestIsWindowsAppsDir_DetectsStubDir(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{`C:\Users\user\AppData\Local\Microsoft\WindowsApps`, true},
		{`C:\Users\user\AppData\Local\Microsoft\WindowsApps\`, true},
		// Case-insensitive
		{`C:\users\user\appdata\local\microsoft\windowsapps`, true},
		{`C:\USERS\USER\APPDATA\LOCAL\MICROSOFT\WINDOWSAPPS`, true},
		// Real interpreter dirs — must not be stripped
		{`C:\Python312`, false},
		{`C:\Program Files\Git\bin`, false},
		{`C:\Program Files\nodejs`, false},
		{`C:\Windows\System32`, false},
		{`C:\Go\bin`, false},
	}
	for _, tc := range cases {
		if got := isWindowsAppsDir(tc.dir); got != tc.want {
			t.Errorf("isWindowsAppsDir(%q) = %v, want %v", tc.dir, got, tc.want)
		}
	}
}

func TestSanitizeWindowsPATH_StripsStubDirs(t *testing.T) {
	stub := `C:\Users\user\AppData\Local\Microsoft\WindowsApps`
	real := `C:\Python312;C:\Windows\System32`
	raw := real[:10] + ";" + stub + ";" + real[10:] // interleave stub

	got := sanitizeWindowsPATH(stub + ";" + real)

	if strings.Contains(got, stub) {
		t.Errorf("WindowsApps stub dir should have been stripped; got %q", got)
	}
	if !strings.Contains(got, `C:\Python312`) {
		t.Errorf("real Python dir should be preserved; got %q", got)
	}
	_ = raw
}

func TestSanitizeWindowsPATH_PreservesNonStubEntries(t *testing.T) {
	input := `C:\Program Files\Git\bin;C:\Program Files\nodejs;C:\Windows\System32`
	got := sanitizeWindowsPATH(input)
	if got != input {
		t.Errorf("PATH with no stubs should be unchanged; got %q", got)
	}
}

func TestSanitizeWindowsPATH_AllStubs_ReturnsEmpty(t *testing.T) {
	stub := `C:\Users\user\AppData\Local\Microsoft\WindowsApps`
	got := sanitizeWindowsPATH(stub)
	if got != "" {
		t.Errorf("PATH containing only stub should result in empty string; got %q", got)
	}
}

func TestSanitizeWindowsPATH_MultipleStubs_AllStripped(t *testing.T) {
	// Different users' stub dirs (rare but possible on shared machines)
	stubs := `C:\Users\alice\AppData\Local\Microsoft\WindowsApps;C:\Users\bob\AppData\Local\Microsoft\WindowsApps`
	real := `C:\Windows\System32`
	got := sanitizeWindowsPATH(stubs + ";" + real)

	if strings.Contains(got, "WindowsApps") {
		t.Errorf("all stub dirs should be stripped; got %q", got)
	}
	if !strings.Contains(got, real) {
		t.Errorf("real dir should remain; got %q", got)
	}
}
