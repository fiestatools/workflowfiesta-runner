//go:build windows

package interpreter

import (
	"path/filepath"
	"strings"
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

func TestStaticCandidates_NonEmptyAndNoStubs(t *testing.T) {
	for name, paths := range staticCandidates {
		if len(paths) == 0 {
			t.Errorf("staticCandidates[%q] is empty — every interpreter needs at least one candidate", name)
		}
		for _, p := range paths {
			if isWindowsStub(p) {
				t.Errorf("staticCandidates[%q] contains a stub path %q", name, p)
			}
		}
	}
}

// ── dynamicCandidates ─────────────────────────────────────────────────────────

func TestDynamicCandidates_Python_LocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\testuser\AppData\Local`)
	t.Setenv("PYENV_ROOT", "") // isolate — don't mix pyenv paths into this check

	paths := dynamicCandidates("python3")

	wantPrefix := `C:\Users\testuser\AppData\Local\Programs\Python`
	found := false
	for _, p := range paths {
		if strings.HasPrefix(p, wantPrefix) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("dynamicCandidates(python3) should include paths under %q; got %v", wantPrefix, paths)
	}
}

func TestDynamicCandidates_Python_PyenvRoot(t *testing.T) {
	t.Setenv("PYENV_ROOT", `C:\pyenv`)

	paths := dynamicCandidates("python3")

	want := filepath.Join(`C:\pyenv`, "shims", "python.exe")
	for _, p := range paths {
		if p == want {
			return
		}
	}
	t.Errorf("dynamicCandidates(python3) should include pyenv shims path %q; got %v", want, paths)
}

func TestDynamicCandidates_Node_NvmHome(t *testing.T) {
	t.Setenv("NVM_HOME", `C:\Users\testuser\AppData\Roaming\nvm`)
	t.Setenv("NVM_SYMLINK", "")

	paths := dynamicCandidates("node")

	want := filepath.Join(`C:\Users\testuser\AppData\Roaming\nvm`, "node.exe")
	for _, p := range paths {
		if p == want {
			return
		}
	}
	t.Errorf("dynamicCandidates(node) should include NVM_HOME node path %q; got %v", want, paths)
}

func TestDynamicCandidates_Node_NvmSymlink(t *testing.T) {
	t.Setenv("NVM_HOME", "")
	t.Setenv("NVM_SYMLINK", `C:\Program Files\nodejs`)

	paths := dynamicCandidates("node")

	want := filepath.Join(`C:\Program Files\nodejs`, "node.exe")
	for _, p := range paths {
		if p == want {
			return
		}
	}
	t.Errorf("dynamicCandidates(node) should include NVM_SYMLINK node path %q; got %v", want, paths)
}

func TestDynamicCandidates_UnknownInterpreter_ReturnsEmpty(t *testing.T) {
	for _, name := range []string{"bash", "ruby", "go"} {
		if paths := dynamicCandidates(name); len(paths) != 0 {
			t.Errorf("dynamicCandidates(%q) should return empty — no dynamic paths defined; got %v", name, paths)
		}
	}
}

func TestDynamicCandidates_NoStubPaths(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\testuser\AppData\Local`)
	t.Setenv("PYENV_ROOT", `C:\pyenv`)
	t.Setenv("NVM_HOME", `C:\nvm`)
	t.Setenv("NVM_SYMLINK", `C:\nodejs`)

	for _, name := range []string{"python3", "python", "node"} {
		for _, p := range dynamicCandidates(name) {
			if isWindowsStub(p) {
				t.Errorf("dynamicCandidates(%q) returned a stub path %q", name, p)
			}
		}
	}
}
