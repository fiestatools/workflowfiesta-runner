package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"workflowfiesta-runner/internal/config"
)

func setupStateDir(t *testing.T) (home, stateDir string) {
	t.Helper()
	dir := t.TempDir()
	home = filepath.Join(dir, "home")
	stateDir = filepath.Join(home, ".workflowfiesta")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home, stateDir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClearLocalState_PreservesAuditLog(t *testing.T) {
	_, stateDir := setupStateDir(t)

	auditPath := filepath.Join(stateDir, "audit.log")
	credPath := filepath.Join(stateDir, "credentials.env")
	runnerYAML := filepath.Join(stateDir, "runner.yaml")
	writeFile(t, auditPath, "line\n")
	writeFile(t, credPath, "token=x\n")
	writeFile(t, runnerYAML, "confirm: never\n")

	if err := config.ClearLocalState(runnerYAML, false, false, []string{auditPath}); err != nil {
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
	_, stateDir := setupStateDir(t)

	auditPath := filepath.Join(stateDir, "audit.log")
	credPath := filepath.Join(stateDir, "credentials.env")
	writeFile(t, auditPath, "line\n")
	writeFile(t, credPath, "token=x\n")

	if err := config.ClearLocalState("", true, true, []string{auditPath}); err != nil {
		t.Fatalf("ClearLocalState: %v", err)
	}
	if _, err := os.Stat(auditPath); !os.IsNotExist(err) {
		t.Fatal("audit.log should be removed")
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Fatal("credentials.env should be removed")
	}
}

func TestClearLocalState_PreservesScripts(t *testing.T) {
	_, stateDir := setupStateDir(t)

	scriptsDir := filepath.Join(stateDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptFile := filepath.Join(scriptsDir, "deploy.sh")
	writeFile(t, scriptFile, "#!/bin/bash\necho hi\n")

	credPath := filepath.Join(stateDir, "credentials.env")
	auditPath := filepath.Join(stateDir, "audit.log")
	writeFile(t, credPath, "token=x\n")
	writeFile(t, auditPath, "line\n")

	// deleteAuditLogs=true, deleteScripts=false — scripts survive, audit goes
	if err := config.ClearLocalState("", true, false, []string{auditPath}); err != nil {
		t.Fatalf("ClearLocalState: %v", err)
	}
	if _, err := os.Stat(scriptFile); err != nil {
		t.Fatalf("scripts should remain: %v", err)
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Fatal("credentials.env should be removed")
	}
	if _, err := os.Stat(auditPath); !os.IsNotExist(err) {
		t.Fatal("audit.log should be removed")
	}
}

func TestClearLocalState_PreservesOtherRunnerAuditLogs(t *testing.T) {
	_, stateDir := setupStateDir(t)

	// This runner's named audit log
	thisLog := filepath.Join(stateDir, "audit-my-runner-abc123.log")
	writeFile(t, thisLog, "this runner\n")

	// Another runner's named audit log on the same machine
	otherLog := filepath.Join(stateDir, "audit-other-runner-xyz789.log")
	writeFile(t, otherLog, "other runner\n")

	credPath := filepath.Join(stateDir, "credentials.env")
	writeFile(t, credPath, "token=x\n")

	if err := config.ClearLocalState("", true, false, []string{thisLog}); err != nil {
		t.Fatalf("ClearLocalState: %v", err)
	}
	if _, err := os.Stat(thisLog); !os.IsNotExist(err) {
		t.Fatal("this runner's audit log should be removed")
	}
	if _, err := os.Stat(otherLog); err != nil {
		t.Fatalf("other runner's audit log should be preserved: %v", err)
	}
	if _, err := os.Stat(credPath); !os.IsNotExist(err) {
		t.Fatal("credentials.env should be removed")
	}
}

func TestClearLocalState_DeletesScriptsWhenRequested(t *testing.T) {
	_, stateDir := setupStateDir(t)

	scriptsDir := filepath.Join(stateDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(scriptsDir, "deploy.sh"), "#!/bin/bash\n")

	auditPath := filepath.Join(stateDir, "audit.log")
	writeFile(t, auditPath, "line\n")

	// deleteAuditLogs=false, deleteScripts=true — audit survives, scripts go
	if err := config.ClearLocalState("", false, true, []string{auditPath}); err != nil {
		t.Fatalf("ClearLocalState: %v", err)
	}
	if _, err := os.Stat(scriptsDir); !os.IsNotExist(err) {
		t.Fatal("scripts dir should be removed")
	}
	if _, err := os.Stat(auditPath); err != nil {
		t.Fatalf("audit.log should remain: %v", err)
	}
}
