package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"workflowfiesta-runner/internal/config"
)

func TestClearLocalState_PreservesAuditLog(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	stateDir := filepath.Join(home, ".workflowfiesta")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(stateDir, "audit.log")
	if err := os.WriteFile(auditPath, []byte("line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credPath := filepath.Join(stateDir, "credentials.env")
	if err := os.WriteFile(credPath, []byte("token=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runnerYAML := filepath.Join(stateDir, "runner.yaml")
	if err := os.WriteFile(runnerYAML, []byte("confirm: never\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := config.ClearLocalState(runnerYAML, false, []string{auditPath}); err != nil {
		t.Fatalf("ClearLocalState: %v", err)
	}
	if _, err := os.Stat(auditPath); err != nil {
		t.Fatalf("audit.log should remain: %v", err)
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Fatal("credentials.env should be removed")
	}
	if _, err := os.Stat(runnerYAML); !os.IsNotExist(err) {
		t.Fatal("runner.yaml should be removed")
	}
}

func TestClearLocalState_DeletesAuditLogWhenRequested(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	stateDir := filepath.Join(home, ".workflowfiesta")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(stateDir, "audit.log")
	if err := os.WriteFile(auditPath, []byte("line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := config.ClearLocalState("", true, []string{auditPath}); err != nil {
		t.Fatalf("ClearLocalState: %v", err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatal("state dir should be removed")
	}
}
