//go:build !windows

package executor

// sanitizeWindowsPATH is a no-op on non-Windows platforms.
func sanitizeWindowsPATH(raw string) string { return raw }
