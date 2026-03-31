//go:build !windows

package platform

// SetHidden is a no-op on non-Windows platforms; dot-prefixed directories are
// conventionally hidden by the shell and file managers on Unix/macOS.
func SetHidden(path string) error { return nil }
