//go:build windows

package interpreter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// candidatePaths lists well-known installation locations per interpreter name,
// ordered by preference. The hardcoded list is checked before PATH to avoid
// touching Windows Store stubs entirely.
var candidatePaths = map[string][]string{
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

func resolve(name string) (string, Status) {
	for _, p := range candidatePaths[name] {
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
