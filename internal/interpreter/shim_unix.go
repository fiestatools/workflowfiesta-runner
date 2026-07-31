//go:build !windows

package interpreter

// EnsurePython3Shim is a no-op on non-Windows platforms: python3 is expected
// to exist directly wherever Python is installed.
func EnsurePython3Shim() (string, bool) {
	return "", false
}
