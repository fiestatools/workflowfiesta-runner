package executor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"workflowfiesta-runner/internal/localconfig"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func testExecutor(overrides ...func(*localconfig.LocalConfig)) *localExecutor {
	cfg := localconfig.Default()
	cfg.AuditLog = "" // disable audit writes in unit tests
	for _, fn := range overrides {
		fn(cfg)
	}
	return newLocalExecutor(cfg)
}

// ── blockedPatternCheck ───────────────────────────────────────────────────────

func TestBlockedPatternCheck_RmRF_Root(t *testing.T) {
	e := testExecutor()
	_, blocked := e.blockedPatternCheck("rm -rf /")
	if !blocked {
		t.Fatal("expected rm -rf / to be blocked")
	}
}

func TestBlockedPatternCheck_RmRF_Home(t *testing.T) {
	e := testExecutor()
	_, blocked := e.blockedPatternCheck("rm -rf ~")
	if !blocked {
		t.Fatal("expected rm -rf ~ to be blocked")
	}
}

func TestBlockedPatternCheck_Mkfs(t *testing.T) {
	e := testExecutor()
	_, blocked := e.blockedPatternCheck("mkfs.ext4 /dev/sdb")
	if !blocked {
		t.Fatal("expected mkfs. to be blocked")
	}
}

func TestBlockedPatternCheck_SafeScript(t *testing.T) {
	e := testExecutor()
	_, blocked := e.blockedPatternCheck("ls ~/Documents && echo done")
	if blocked {
		t.Error("ls ~/Documents should not be blocked")
	}
}

func TestBlockedPatternCheck_CustomPattern(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.BlockedPatterns = append(c.BlockedPatterns, `sudo`)
	})
	_, blocked := e.blockedPatternCheck("sudo apt-get update")
	if !blocked {
		t.Fatal("expected sudo to be blocked by custom pattern")
	}
}

func TestBlockedPatternCheck_InvalidRegex_NoPanic(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.BlockedPatterns = []string{`[invalid`}
	})
	_, blocked := e.blockedPatternCheck("ls ~/")
	if blocked {
		t.Error("invalid regex should not match anything")
	}
}

func TestBlockedPatternCheck_EmptyPatterns(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.BlockedPatterns = nil
	})
	_, blocked := e.blockedPatternCheck("rm -rf /")
	if blocked {
		t.Error("no patterns configured, nothing should be blocked")
	}
}

func TestBlockedPatternCheck_ReturnsMatchedPattern(t *testing.T) {
	e := testExecutor()
	pat, blocked := e.blockedPatternCheck("mkfs.ext4 /dev/sdb")
	if !blocked {
		t.Fatal("expected block")
	}
	if !strings.Contains(pat, "mkfs") {
		t.Errorf("returned pattern %q should contain 'mkfs'", pat)
	}
}

// ── needsConfirmation ─────────────────────────────────────────────────────────

func TestNeedsConfirmation_Always(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) { c.Confirm = "always" })
	if !e.needsConfirmation("ls ~/") {
		t.Error("confirm=always should require confirmation for safe scripts")
	}
}

func TestNeedsConfirmation_Never(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) { c.Confirm = "never" })
	if e.needsConfirmation("rm -rf ~/important-files") {
		t.Error("confirm=never should never require confirmation")
	}
}

func TestNeedsConfirmation_Destructive_rm(t *testing.T) {
	e := testExecutor()
	tests := []struct {
		script string
		expect bool
	}{
		{"rm -f old.log", true},
		{"mv file.txt ~/trash/", true},
		{"rmdir empty-dir", true},
		{"truncate -s 0 logfile", true},
		{"cat data > output.txt", true},
		{"ls ~/projects", false},
		{"find ~/Documents -name '*.pdf'", false},
		{"cat README.md", false},
		{"grep -r 'TODO' .", false},
		{"echo hello world", false},
	}
	for _, tt := range tests {
		got := e.needsConfirmation(tt.script)
		if got != tt.expect {
			t.Errorf("needsConfirmation(%q) = %v, want %v", tt.script, got, tt.expect)
		}
	}
}

// ── buildEnv ─────────────────────────────────────────────────────────────────

func TestBuildEnv_ContainsMinimalPath(t *testing.T) {
	e := testExecutor()
	env := e.buildEnv(nil)
	hasPATH, hasHOME := false, false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			hasPATH = true
		}
		if strings.HasPrefix(kv, "HOME=") {
			hasHOME = true
		}
	}
	if !hasPATH {
		t.Error("env should contain PATH")
	}
	if !hasHOME {
		t.Error("env should contain HOME")
	}
}

func TestBuildEnv_MergesJobVars(t *testing.T) {
	e := testExecutor()
	env := e.buildEnv(map[string]string{"MY_VAR": "hello", "API_KEY": "secret"})
	envMap := envToMap(env)
	if envMap["MY_VAR"] != "hello" {
		t.Errorf("MY_VAR = %q, want %q", envMap["MY_VAR"], "hello")
	}
	if envMap["API_KEY"] != "secret" {
		t.Errorf("API_KEY = %q, want %q", envMap["API_KEY"], "secret")
	}
}

func TestBuildEnv_NoLDPreload(t *testing.T) {
	e := testExecutor()
	env := e.buildEnv(nil)
	for _, kv := range env {
		if strings.HasPrefix(kv, "LD_PRELOAD=") {
			t.Error("LD_PRELOAD should not be in the minimal env")
		}
	}
}

func TestBuildEnv_PathIsMinimal(t *testing.T) {
	e := testExecutor()
	envMap := envToMap(e.buildEnv(nil))
	path := envMap["PATH"]
	// Should not contain unusual paths like /snap or /home/user/.local
	if strings.Contains(path, ".local") || strings.Contains(path, "snap") {
		t.Errorf("PATH should be minimal, got %q", path)
	}
}

// ── wrapWithLimits ────────────────────────────────────────────────────────────

func TestWrapWithLimits_ContainsUlimit(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) { c.MaxTimeout = 45 })
	wrapped := e.wrapWithLimits("echo hello")
	if !strings.Contains(wrapped, "ulimit") {
		t.Error("wrapped script should contain ulimit")
	}
	if !strings.Contains(wrapped, "45") {
		t.Errorf("wrapped script should contain timeout value 45:\n%s", wrapped)
	}
	if !strings.Contains(wrapped, "echo hello") {
		t.Error("original script should be present in wrapped version")
	}
}

func TestWrapWithLimits_MemoryLimit(t *testing.T) {
	e := testExecutor()
	wrapped := e.wrapWithLimits("ls")
	// 1GB in KB = 1048576
	if !strings.Contains(wrapped, "1048576") {
		t.Errorf("expected 1GB memory limit in wrapped script:\n%s", wrapped)
	}
}

// ── writeAudit ────────────────────────────────────────────────────────────────

func TestWriteAudit_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/audit.log"
	e := testExecutor(func(c *localconfig.LocalConfig) { c.AuditLog = logPath })

	e.writeAudit(auditEntry{
		Time:     "2026-03-07T00:00:00Z",
		JobID:    "test-001",
		Script:   "ls ~/",
		Decision: "approved",
	})

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("audit log not created: %v", err)
	}
	if !strings.Contains(string(data), "test-001") {
		t.Errorf("audit log should contain job_id, got: %s", data)
	}
}

func TestWriteAudit_IsValidJSON(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/audit.log"
	e := testExecutor(func(c *localconfig.LocalConfig) { c.AuditLog = logPath })

	e.writeAudit(auditEntry{
		Time:     "2026-03-07T00:00:00Z",
		JobID:    "test-002",
		Decision: "blocked",
		Reason:   `blocked_pattern:rm\s+-rf\s+/`,
	})

	data, _ := os.ReadFile(logPath)
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
		t.Errorf("audit entry is not valid JSON object: %s", line)
	}
}

func TestWriteAudit_AppendMultiple(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/audit.log"
	e := testExecutor(func(c *localconfig.LocalConfig) { c.AuditLog = logPath })

	for i := 0; i < 3; i++ {
		e.writeAudit(auditEntry{
			Time:     "2026-03-07T00:00:00Z",
			JobID:    fmt.Sprintf("job-%d", i),
			Decision: "approved",
		})
	}

	data, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 audit lines, got %d:\n%s", len(lines), data)
	}
}

func TestWriteAudit_EmptyPath_NoError(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) { c.AuditLog = "" })
	// Should not panic.
	e.writeAudit(auditEntry{JobID: "x", Decision: "approved"})
}

func TestWriteAudit_FileMode(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/audit.log"
	e := testExecutor(func(c *localconfig.LocalConfig) { c.AuditLog = logPath })
	e.writeAudit(auditEntry{JobID: "x", Decision: "approved"})

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("audit log mode = %o, want 0600", info.Mode().Perm())
	}
}

// ── Execute integration ───────────────────────────────────────────────────────

func TestExecute_BlockedScript_ReturnsError(t *testing.T) {
	e := testExecutor()
	_, err := e.Execute(context.Background(), Input{
		JobID:  "blocked",
		Script: "rm -rf /",
	})
	if err == nil {
		t.Fatal("expected error for blocked script")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error should mention 'blocked', got: %v", err)
	}
}

func TestExecute_ScriptOutput(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) { c.Confirm = "never" })
	outputChan := make(chan string, 100)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exitCode, err := e.Execute(ctx, Input{
		JobID:      "output-test",
		Script:     "echo 'hello from local runner'",
		Timeout:    10 * time.Second,
		OutputChan: outputChan,
	})
	close(outputChan)

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}

	var output strings.Builder
	for chunk := range outputChan {
		output.WriteString(chunk)
	}
	if !strings.Contains(output.String(), "hello from local runner") {
		t.Errorf("expected output to contain marker, got: %q", output.String())
	}
}

func TestExecute_NonZeroExitCode(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) { c.Confirm = "never" })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exitCode, err := e.Execute(ctx, Input{
		JobID:   "exit-test",
		Script:  "exit 42",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if exitCode != 42 {
		t.Errorf("exit code = %d, want 42", exitCode)
	}
}

func TestExecute_ClampTimeout(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.MaxTimeout = 5
		c.Confirm = "never"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exitCode, err := e.Execute(ctx, Input{
		JobID:   "clamp-test",
		Script:  "exit 0",
		Timeout: 600 * time.Second, // way over the max
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}
}

func TestExecute_AuditLog_Written(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/audit.log"
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.AuditLog = logPath
		c.Confirm = "never"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	e.Execute(ctx, Input{
		JobID:   "audit-test",
		Script:  "echo ok",
		Timeout: 5 * time.Second,
	})

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("audit log not written: %v", err)
	}
	if !strings.Contains(string(data), "audit-test") {
		t.Errorf("audit log should contain job_id: %s", data)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func envToMap(env []string) map[string]string {
	m := make(map[string]string)
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}
	return m
}
