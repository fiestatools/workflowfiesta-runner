//go:build windows

package interpreter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// staticCandidates lists well-known system-wide installation locations per
// interpreter name, ordered by preference. Checked before PATH to avoid
// touching Windows Store stubs entirely.
var staticCandidates = map[string][]string{
	"bash": {
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
		`C:\msys64\usr\bin\bash.exe`,
		`C:\msys32\usr\bin\bash.exe`,
		`C:\cygwin64\bin\bash.exe`,
		`C:\cygwin\bin\bash.exe`,
	},
	"python3": {
		`C:\Python313\python.exe`,
		`C:\Python312\python.exe`,
		`C:\Python311\python.exe`,
		`C:\Python310\python.exe`,
		`C:\Python39\python.exe`,
	},
	"python": {
		`C:\Python313\python.exe`,
		`C:\Python312\python.exe`,
		`C:\Python311\python.exe`,
		`C:\Python310\python.exe`,
		`C:\Python39\python.exe`,
	},
	"node": {
		`C:\Program Files\nodejs\node.exe`,
		`C:\Program Files (x86)\nodejs\node.exe`,
	},
	"ruby": {
		`C:\Ruby33-x64\bin\ruby.exe`,
		`C:\Ruby32-x64\bin\ruby.exe`,
		`C:\Ruby31-x64\bin\ruby.exe`,
		`C:\Ruby30-x64\bin\ruby.exe`,
	},
	"go": {
		`C:\Program Files\Go\bin\go.exe`,
		`C:\Go\bin\go.exe`,
	},
}

// dynamicCandidates builds additional interpreter paths derived from
// environment variables, covering user-scoped installs that have no fixed
// system-wide location (python.org user installs, pyenv-win, nvm-windows).
func dynamicCandidates(name string) []string {
	switch name {
	case "python3", "python":
		var paths []string
		// python.org installer default user location (no admin rights required).
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			for _, ver := range []string{"313", "312", "311", "310", "39"} {
				paths = append(paths,
					filepath.Join(localAppData, "Programs", "Python", "Python"+ver, "python.exe"),
				)
			}
		}
		// pyenv-win: shims directory under PYENV_ROOT or default ~/.pyenv.
		pyenvRoot := os.Getenv("PYENV_ROOT")
		if pyenvRoot == "" {
			if home, err := os.UserHomeDir(); err == nil {
				pyenvRoot = filepath.Join(home, ".pyenv", "pyenv-win")
			}
		}
		if pyenvRoot != "" {
			paths = append(paths, filepath.Join(pyenvRoot, "shims", "python.exe"))
		}
		return paths

	case "node":
		var paths []string
		// nvm-windows: NVM_HOME points directly to the active node version dir.
		if nvmHome := os.Getenv("NVM_HOME"); nvmHome != "" {
			paths = append(paths, filepath.Join(nvmHome, "node.exe"))
		}
		// nvm-windows symlink: NVM_SYMLINK points to the active version.
		if nvmSymlink := os.Getenv("NVM_SYMLINK"); nvmSymlink != "" {
			paths = append(paths, filepath.Join(nvmSymlink, "node.exe"))
		}
		return paths
	}
	return nil
}

func resolve(name string) (string, Status) {
	path, status := resolveDirect(name)
	if status != StatusFound {
		return path, status
	}
	// Windows has no python3.exe, so alias the discovered python.exe to that name.
	if name == "python3" && !strings.EqualFold(filepath.Base(path), "python3.exe") {
		if shimPath, err := ensureShim("python3", path); err == nil {
			return shimPath, StatusFound
		}
	}
	return path, status
}

func resolveDirect(name string) (string, Status) {
	// Check static system-wide paths first.
	for _, p := range staticCandidates[name] {
		if _, err := os.Stat(p); err == nil {
			return p, StatusFound
		}
	}
	// Check dynamic env-var-derived paths (user installs, pyenv, nvm).
	for _, p := range dynamicCandidates(name) {
		if _, err := os.Stat(p); err == nil {
			return p, StatusFound
		}
	}
	// Fall back to PATH lookup, but reject stubs.
	if p, err := exec.LookPath(name); err == nil {
		if isWindowsStub(p) {
			return "", StatusStub
		}
		return p, StatusFound
	}
	return "", StatusMissing
}

func isWindowsStub(path string) bool {
	lower := strings.ToLower(filepath.Clean(path))
	return strings.Contains(lower, `microsoft\windowsapps`)
}
