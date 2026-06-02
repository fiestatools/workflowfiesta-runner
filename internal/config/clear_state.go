package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClearLocalState removes persisted runner credentials and configuration while
// preserving the per-runner audit directory (which may hold logs from other
// runners on this machine).
//
// auditDir is the directory holding per-runner audit logs; it is always kept.
// ownAuditFile is this runner's own audit log file. When deleteOwnAudit is true,
// only that single file is removed — other runners' audit logs are left intact.
func ClearLocalState(configPath, auditDir, ownAuditFile string, deleteOwnAudit bool) error {
	homeStateDir := filepath.Dir(CredentialsFilePath())
	preserveDir := cleanAbs(auditDir)

	// Remove everything under the state dir except the audit directory.
	if err := clearHomeStateExceptDir(homeStateDir, preserveDir); err != nil {
		return err
	}

	// Remove a custom config file that lives outside the state dir.
	if configPath != "" && !strings.HasPrefix(configPath, homeStateDir+string(os.PathSeparator)) {
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove custom config: %w", err)
		}
	}

	// Optionally delete just this runner's own audit file.
	if deleteOwnAudit && ownAuditFile != "" {
		if err := os.Remove(ownAuditFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove audit log %s: %w", ownAuditFile, err)
		}
		removeDirIfEmpty(preserveDir)
	}

	return nil
}

// cleanAbs returns the cleaned, absolute form of p (best-effort).
func cleanAbs(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(p)
}

// clearHomeStateExceptDir removes every entry under homeStateDir except the
// preserveDir (the audit directory). preserveDir may be empty (preserve nothing).
func clearHomeStateExceptDir(homeStateDir, preserveDir string) error {
	entries, err := os.ReadDir(homeStateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read local state dir: %w", err)
	}
	for _, ent := range entries {
		path := filepath.Join(homeStateDir, ent.Name())
		if preserveDir != "" && (cleanAbs(path) == preserveDir || sameFile(path, preserveDir)) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}

// removeDirIfEmpty removes dir only when it contains no entries.
func removeDirIfEmpty(dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
}

func sameFile(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == b {
		return true
	}
	sa, errA := filepath.EvalSymlinks(a)
	sb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil && sa == sb {
		return true
	}
	return false
}
