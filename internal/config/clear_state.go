package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClearLocalState removes persisted runner credentials and configuration.
// When deleteAuditLogs is false, files matching preserveAuditPaths (and the
// default ~/.workflowfiesta/audit.log) are kept; other files under
// ~/.workflowfiesta are removed.
func ClearLocalState(configPath string, deleteAuditLogs bool, preserveAuditPaths []string) error {
	homeStateDir := filepath.Dir(CredentialsFilePath())
	preserve := auditPreserveSet(preserveAuditPaths)

	if deleteAuditLogs {
		if err := os.RemoveAll(homeStateDir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove local state dir: %w", err)
		}
		for p := range preserve {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove audit log %s: %w", p, err)
			}
		}
	} else if err := clearHomeStateExceptAudit(homeStateDir, preserve); err != nil {
		return err
	}

	if configPath != "" && !strings.HasPrefix(configPath, homeStateDir+string(os.PathSeparator)) {
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove custom config: %w", err)
		}
	}
	return nil
}

func auditPreserveSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{})
	add := func(p string) {
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		set[filepath.Clean(abs)] = struct{}{}
	}
	for _, p := range paths {
		add(p)
	}
	home, _ := os.UserHomeDir()
	add(filepath.Join(home, ".workflowfiesta", "audit.log"))
	return set
}

func clearHomeStateExceptAudit(homeStateDir string, preserve map[string]struct{}) error {
	entries, err := os.ReadDir(homeStateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read local state dir: %w", err)
	}
	for _, ent := range entries {
		path := filepath.Join(homeStateDir, ent.Name())
		if isPreservedAuditPath(path, preserve) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}

func isPreservedAuditPath(path string, preserve map[string]struct{}) bool {
	clean := filepath.Clean(path)
	if _, ok := preserve[clean]; ok {
		return true
	}
	// Symlink or equivalent path to a preserved audit file.
	for p := range preserve {
		if sameFile(path, p) {
			return true
		}
	}
	return false
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

// AuditLogPathsFromConfig returns audit log file paths that may exist on disk.
func AuditLogPathsFromConfig(auditLog string) []string {
	var paths []string
	if auditLog != "" {
		paths = append(paths, auditLog)
	}
	home, _ := os.UserHomeDir()
	paths = append(paths, filepath.Join(home, ".workflowfiesta", "audit.log"))
	return paths
}
