package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClearLocalState removes persisted runner credentials and configuration.
//
// deleteAuditLogs: when true, only THIS runner's audit log is deleted
// (audit-{name}-{id}.log). Other runners' named audit logs in the same
// directory are always preserved.
// deleteScripts: when true, the ~/.workflowfiesta/scripts/ org cache is removed.
//
// preserveAuditPaths must contain this runner's audit log path(s) so the
// function knows which files belong to it vs. other runners.
func ClearLocalState(configPath string, deleteAuditLogs, deleteScripts bool, preserveAuditPaths []string) error {
	homeStateDir := filepath.Dir(CredentialsFilePath())
	preserve := buildPreserveSet(homeStateDir, deleteAuditLogs, deleteScripts, preserveAuditPaths)

	if err := clearHomeStateExcept(homeStateDir, preserve); err != nil {
		return err
	}

	// Remove this runner's audit log at external paths (outside homeStateDir) when requested.
	if deleteAuditLogs {
		for _, p := range preserveAuditPaths {
			if abs, err := filepath.Abs(p); err == nil {
				clean := filepath.Clean(abs)
				if !strings.HasPrefix(clean, homeStateDir+string(os.PathSeparator)) {
					if err := os.Remove(clean); err != nil && !os.IsNotExist(err) {
						return fmt.Errorf("remove audit log %s: %w", clean, err)
					}
				}
			}
		}
	}

	if configPath != "" && !strings.HasPrefix(configPath, homeStateDir+string(os.PathSeparator)) {
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove custom config: %w", err)
		}
	}
	return nil
}

// buildPreserveSet constructs the set of absolute paths inside homeStateDir to keep.
// Other runners' named audit logs (audit-*-*.log) are always preserved regardless
// of deleteAuditLogs — only this runner's own log is subject to deletion.
func buildPreserveSet(homeStateDir string, deleteAuditLogs, deleteScripts bool, preserveAuditPaths []string) map[string]struct{} {
	preserve := make(map[string]struct{})

	// Always keep other runners' audit logs.
	for p := range otherRunnerAuditLogs(homeStateDir, preserveAuditPaths) {
		preserve[p] = struct{}{}
	}
	if !deleteAuditLogs {
		for p, v := range auditPreserveSet(preserveAuditPaths) {
			preserve[p] = v
		}
	}
	if !deleteScripts {
		preserve[filepath.Clean(filepath.Join(homeStateDir, "scripts"))] = struct{}{}
	}
	return preserve
}

// otherRunnerAuditLogs returns the set of audit-*-*.log files in homeStateDir
// that do NOT belong to this runner (i.e. not in thisRunnerPaths).
func otherRunnerAuditLogs(homeStateDir string, thisRunnerPaths []string) map[string]struct{} {
	thisRunner := auditPreserveSet(thisRunnerPaths)
	result := make(map[string]struct{})
	entries, err := os.ReadDir(homeStateDir)
	if err != nil {
		return result
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if strings.HasPrefix(name, "audit-") && strings.HasSuffix(name, ".log") {
			abs := filepath.Clean(filepath.Join(homeStateDir, name))
			if _, isThis := thisRunner[abs]; !isThis {
				result[abs] = struct{}{}
			}
		}
	}
	return result
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

func clearHomeStateExcept(homeStateDir string, preserve map[string]struct{}) error {
	entries, err := os.ReadDir(homeStateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read local state dir: %w", err)
	}
	for _, ent := range entries {
		path := filepath.Join(homeStateDir, ent.Name())
		if isPreservedPath(path, preserve) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}

func isPreservedPath(path string, preserve map[string]struct{}) bool {
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
