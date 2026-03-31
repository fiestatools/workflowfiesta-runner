//go:build windows

package platform

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// SetHidden marks path with FILE_ATTRIBUTE_HIDDEN so it does not appear in
// normal File Explorer views. Errors are non-fatal — the directory still works.
func SetHidden(path string) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_HIDDEN)
}
