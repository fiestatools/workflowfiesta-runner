//go:build windows

package interpreter

import (
	"io"
	"os"
	"path/filepath"
)

func shimDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".workflowfiesta", "shims")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ensureShim makes `name.exe` resolve to targetExe by hardlinking (or, if that's not possible, copying) targetExe into the shim directory under a different name.
func ensureShim(name, targetExe string) (string, error) {
	dir, err := shimDir()
	if err != nil {
		return "", err
	}
	shimPath := filepath.Join(dir, name+filepath.Ext(targetExe))

	if shimInfo, statErr := os.Stat(shimPath); statErr == nil {
		if targetInfo, targetErr := os.Stat(targetExe); targetErr == nil && os.SameFile(shimInfo, targetInfo) {
			return shimPath, nil // already correct
		}
		os.Remove(shimPath)
	}

	if err := os.Link(targetExe, shimPath); err == nil {
		return shimPath, nil
	}
	// Cross-volume, or hardlinks unsupported on this filesystem, fall back to a copy.
	if err := copyFile(targetExe, shimPath); err != nil {
		return "", err
	}
	return shimPath, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// EnsurePython3Shim resolves a real Python interpreter and ensures a python3 shim exists for it. Returns the shim path and true on success.
func EnsurePython3Shim() (string, bool) {
	path, status := resolve("python3")
	return path, status == StatusFound
}
