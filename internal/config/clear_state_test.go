package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"workflowfiesta-runner/internal/config"
)

// setupState creates a ~/.workflowfiesta state dir with credentials, runner.yaml,
// and an audit/ directory containing two runners' logs. Returns the paths.
func setupState(t *testing.T) (home, stateDir, auditDir, ownAudit, otherAudit, credPath, runnerYAML string) {
	t.Helper()
	dir := t.TempDir()
	home = filepath.Join(dir, "home")
	stateDir = filepath.Join(home, ".workflowfiesta")
	auditDir = filepath.Join(stateDir, "audit")
	if err := os.MkdirAll(auditDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ownAudit = filepath.Join(auditDir, "runner-a.log")
	otherAudit = filepath.Join(auditDir, "runner-b.log")
	for _, p := range []string{ownAudit, otherAudit} {
		if err := os.WriteFile(p, []byte("line\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	credPath = filepath.Join(stateDir, "credentials.env")
	if err := os.WriteFile(credPath, []byte("token=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runnerYAML = filepath.Join(stateDir, "runner.yaml")
	if err := os.WriteFile(runnerYAML, []byte("confirm: never\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home, stateDir, auditDir, ownAudit, otherAudit, credPath, runnerYAML
}

// When deleteOwnAudit is false, all audit logs are preserved and credentials/config removed.
func TestClearLocalState_PreservesAuditLogs(t *testing.T) {
	_, _, auditDir, ownAudit, otherAudit, credPath, runnerYAML := setupState(t)

	if err := config.ClearLocalState(runnerYAML, auditDir, ownAudit, false); err != nil {
		t.Fatalf("ClearLocalState: %v", err)
	}
	if _, err := os.Stat(ownAudit); err != nil {
		t.Fatalf("own audit log should remain: %v", err)
	}
	if _, err := os.Stat(otherAudit); err != nil {
		t.Fatalf("other runner's audit log should remain: %v", err)
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Fatal("credentials.env should be removed")
	}
	if _, err := os.Stat(runnerYAML); !os.IsNotExist(err) {
		t.Fatal("runner.yaml should be removed")
	}
}

// When deleteOwnAudit is true, only this runner's audit log is removed; other
// runners' logs are kept.
func TestClearLocalState_DeletesOnlyOwnAuditLog(t *testing.T) {
	_, _, auditDir, ownAudit, otherAudit, credPath, runnerYAML := setupState(t)

	if err := config.ClearLocalState(runnerYAML, auditDir, ownAudit, true); err != nil {
		t.Fatalf("ClearLocalState: %v", err)
	}
	if _, err := os.Stat(ownAudit); !os.IsNotExist(err) {
		t.Fatal("own audit log should be removed")
	}
	if _, err := os.Stat(otherAudit); err != nil {
		t.Fatalf("other runner's audit log should remain: %v", err)
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Fatal("credentials.env should be removed")
	}
}

// Deleting the last remaining audit log cleans up the empty audit directory.
func TestClearLocalState_RemovesEmptyAuditDir(t *testing.T) {
	_, _, auditDir, ownAudit, otherAudit, _, runnerYAML := setupState(t)
	// Remove the other runner's log so only ours remains.
	if err := os.Remove(otherAudit); err != nil {
		t.Fatal(err)
	}

	if err := config.ClearLocalState(runnerYAML, auditDir, ownAudit, true); err != nil {
		t.Fatalf("ClearLocalState: %v", err)
	}
	if _, err := os.Stat(auditDir); !os.IsNotExist(err) {
		t.Fatal("empty audit dir should be removed")
	}
}
