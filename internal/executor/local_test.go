package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.AuditLog = dir
		c.RunnerID = "test-runner"
	})

	e.writeAudit(auditEntry{
		Time:     "2026-03-07T00:00:00Z",
		JobID:    "test-001",
		Script:   "ls ~/",
		Decision: "approved",
	})

	data, err := os.ReadFile(e.localCfg.AuditFilePath())
	if err != nil {
		t.Fatalf("audit log not created: %v", err)
	}
	if !strings.Contains(string(data), "test-001") {
		t.Errorf("audit log should contain job_id, got: %s", data)
	}
}

func TestWriteAudit_IsValidJSON(t *testing.T) {
	dir := t.TempDir()
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.AuditLog = dir
		c.RunnerID = "test-runner"
	})

	e.writeAudit(auditEntry{
		Time:     "2026-03-07T00:00:00Z",
		JobID:    "test-002",
		Decision: "blocked",
		Reason:   `blocked_pattern:rm\s+-rf\s+/`,
	})

	data, _ := os.ReadFile(e.localCfg.AuditFilePath())
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
		t.Errorf("audit entry is not valid JSON object: %s", line)
	}
}

func TestWriteAudit_AppendMultiple(t *testing.T) {
	dir := t.TempDir()
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.AuditLog = dir
		c.RunnerID = "test-runner"
	})

	for i := 0; i < 3; i++ {
		e.writeAudit(auditEntry{
			Time:     "2026-03-07T00:00:00Z",
			JobID:    fmt.Sprintf("job-%d", i),
			Decision: "approved",
		})
	}

	data, _ := os.ReadFile(e.localCfg.AuditFilePath())
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
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.AuditLog = dir
		c.RunnerID = "test-runner"
	})
	e.writeAudit(auditEntry{JobID: "x", Decision: "approved"})

	info, err := os.Stat(e.localCfg.AuditFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("audit log mode = %o, want 0600", info.Mode().Perm())
	}
}

// ── Execute integration ───────────────────────────────────────────────────────

func TestExecute_BlockedScript_ReturnsNonZeroWithMessage(t *testing.T) {
	e := testExecutor()
	outputChan := make(chan string, 10)
	exitCode, err := e.Execute(context.Background(), Input{
		JobID:      "blocked",
		Script:     "rm -rf /",
		OutputChan: outputChan,
	})
	if err != nil {
		t.Fatalf("expected nil error for blocked script (message goes to output), got: %v", err)
	}
	if exitCode == 0 {
		t.Fatal("expected non-zero exit code for blocked script")
	}
	close(outputChan)
	var out strings.Builder
	for chunk := range outputChan {
		out.WriteString(chunk)
	}
	if !strings.Contains(out.String(), "blocked") {
		t.Errorf("output should mention 'blocked', got: %q", out.String())
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
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.AuditLog = dir
		c.RunnerID = "test-runner"
		c.Confirm = "never"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	e.Execute(ctx, Input{
		JobID:   "audit-test",
		Script:  "echo ok",
		Timeout: 5 * time.Second,
	})

	data, err := os.ReadFile(e.localCfg.AuditFilePath())
	if err != nil {
		t.Fatalf("audit log not written: %v", err)
	}
	if !strings.Contains(string(data), "audit-test") {
		t.Errorf("audit log should contain job_id: %s", data)
	}
}

func TestWriteScriptTempFile_CreatesExecutableFile(t *testing.T) {
	script := "echo hello"
	path, cleanup, err := writeScriptTempFile(script)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not readable: %v", err)
	}
	if string(data) != script {
		t.Errorf("expected %q, got %q", script, string(data))
	}
	info, _ := os.Stat(path)
	if info.Mode()&0o100 == 0 {
		t.Error("file should be executable")
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("cleanup should remove the file")
	}
}

func TestExecute_HeredocScript(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	e := testExecutor(func(c *localconfig.LocalConfig) {
		c.Confirm = "never"
	})
	out := make(chan string, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	code, err := e.Execute(ctx, Input{
		JobID:      "heredoc-test",
		Script:     "python3 << 'EOF'\nprint('heredoc works')\nEOF\n",
		Timeout:    10 * time.Second,
		OutputChan: out,
	})
	close(out)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	var combined string
	for line := range out {
		combined += line
	}
	if !strings.Contains(combined, "heredoc works") {
		t.Errorf("expected output to contain 'heredoc works', got: %q", combined)
	}
}

// ── scriptFingerprint ─────────────────────────────────────────────────────────

func TestScriptFingerprint_DeterministicAndUnique(t *testing.T) {
	// Same script → same fingerprint
	fp1 := scriptFingerprint("ls -la")
	fp2 := scriptFingerprint("ls -la")
	if fp1 != fp2 {
		t.Error("fingerprint should be deterministic")
	}
	// Different script → different fingerprint
	fp3 := scriptFingerprint("rm -rf /tmp/test")
	if fp1 == fp3 {
		t.Error("different scripts should produce different fingerprints")
	}
	// Whitespace not trimmed by scriptFingerprint itself (it hashes raw bytes)
	fp4 := scriptFingerprint("ls -la")
	if fp1 != fp4 {
		t.Error("same content should produce same fingerprint")
	}
}

func TestScriptFingerprint_EmptyString(t *testing.T) {
	fp := scriptFingerprint("")
	if fp == "" {
		t.Error("fingerprint of empty string should not be empty")
	}
	// Should be a valid hex SHA-256 (64 chars)
	if len(fp) != 64 {
		t.Errorf("expected 64-char hex fingerprint, got %d chars: %q", len(fp), fp)
	}
}

func TestScriptFingerprint_DifferentWhitespace(t *testing.T) {
	fp1 := scriptFingerprint("ls -la")
	fp2 := scriptFingerprint("  ls -la  \n")
	// scriptFingerprint hashes raw input, so whitespace differences produce different hashes
	if fp1 == fp2 {
		t.Log("note: fingerprint does NOT trim whitespace (hashes raw input)")
	}
}

// ── isSessionAllowed / isAlwaysAllowed ────────────────────────────────────────

func TestIsSessionAllowed_AfterAddingFingerprint(t *testing.T) {
	e := testExecutor()
	script := "ls ~/Documents"
	if e.isSessionAllowed(script) {
		t.Fatal("should not be session-allowed before adding")
	}
	fp := scriptFingerprint(script)
	e.mu.Lock()
	e.sessionAllows = append(e.sessionAllows, fp)
	e.mu.Unlock()
	if !e.isSessionAllowed(script) {
		t.Error("should be session-allowed after adding fingerprint")
	}
}

func TestIsAlwaysAllowed_AfterAddingToConfig(t *testing.T) {
	e := testExecutor()
	script := "cat README.md"
	if e.isAlwaysAllowed(script) {
		t.Fatal("should not be always-allowed before adding")
	}
	fp := scriptFingerprint(script)
	e.localCfg.AlwaysAllowedPatterns = append(e.localCfg.AlwaysAllowedPatterns, fp)
	if !e.isAlwaysAllowed(script) {
		t.Error("should be always-allowed after adding fingerprint to config")
	}
}

func TestIsSessionAllowed_DoesNotMatchDifferentScript(t *testing.T) {
	e := testExecutor()
	fp := scriptFingerprint("ls ~/Documents")
	e.mu.Lock()
	e.sessionAllows = append(e.sessionAllows, fp)
	e.mu.Unlock()
	if e.isSessionAllowed("ls ~/Desktop") {
		t.Error("different script should not be session-allowed")
	}
}

// ── Session allow via needsConfirmation ───────────────────────────────────────

func TestNeedsConfirmation_SkippedWhenSessionAllowed(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) { c.Confirm = "always" })
	script := "echo session-allowed"
	// Before session-allow, confirm=always requires confirmation
	if !e.needsConfirmation(script) {
		t.Fatal("expected confirmation before session-allow")
	}
	// Add to session allows
	fp := scriptFingerprint(script)
	e.mu.Lock()
	e.sessionAllows = append(e.sessionAllows, fp)
	e.mu.Unlock()
	// After session-allow, no confirmation needed
	if e.needsConfirmation(script) {
		t.Error("should not need confirmation for session-allowed script")
	}
}

func TestNeedsConfirmation_SkippedWhenAlwaysAllowed(t *testing.T) {
	e := testExecutor(func(c *localconfig.LocalConfig) { c.Confirm = "always" })
	script := "echo always-allowed"
	fp := scriptFingerprint(script)
	e.localCfg.AlwaysAllowedPatterns = append(e.localCfg.AlwaysAllowedPatterns, fp)
	if e.needsConfirmation(script) {
		t.Error("should not need confirmation for always-allowed script")
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
