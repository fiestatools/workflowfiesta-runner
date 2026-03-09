package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"workflowfiesta-runner/internal/localconfig"
	"workflowfiesta-runner/internal/localui"
)


type localExecutor struct {
	localCfg *localconfig.LocalConfig
}

func newLocalExecutor(cfg *localconfig.LocalConfig) *localExecutor {
	if cfg == nil {
		cfg = localconfig.Default()
	}
	return &localExecutor{localCfg: cfg}
}

// auditEntry is a single line in the JSON audit log.
type auditEntry struct {
	Time       string `json:"time"`
	JobID      string `json:"job_id"`
	Script     string `json:"script"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// Execute runs the script on the local host, applying the configured security layers.
func (e *localExecutor) Execute(ctx context.Context, input Input) (int, error) {
	start := time.Now()

	// Layer 1: Blocked pattern check.
	if pattern, blocked := e.blockedPatternCheck(input.Script); blocked {
		e.writeAudit(auditEntry{
			Time:     start.UTC().Format(time.RFC3339),
			JobID:    input.JobID,
			Script:   input.Script,
			Decision: "blocked",
			Reason:   "blocked_pattern:" + pattern,
		})
		return -1, fmt.Errorf("script blocked by pattern %q", pattern)
	}

	// Layer 2: Confirmation gate.
	if e.needsConfirmation(input.Script) {
		timeout := time.Duration(e.localCfg.ConfirmTimeout) * time.Second
		approved := localui.RequestApproval(localui.ApprovalRequest{
			JobID:      input.JobID,
			Script:     input.Script,
			RunnerName: e.localCfg.RunnerName,
			Timeout:    timeout,
		})
		if !approved {
			e.writeAudit(auditEntry{
				Time:     start.UTC().Format(time.RFC3339),
				JobID:    input.JobID,
				Script:   input.Script,
				Decision: "denied",
				Reason:   "user_denied",
			})
			return -1, fmt.Errorf("job denied")
		}
	}

	// Clamp timeout to local max.
	maxTimeout := time.Duration(e.localCfg.MaxTimeout) * time.Second
	timeout := input.Timeout
	if timeout == 0 || timeout > maxTimeout {
		timeout = maxTimeout
	}

	// Layer 3: Build minimal environment.
	env := e.buildEnv(input.EnvVars)

	// CWD = first writable allowed path.
	cwd := e.localCfg.WorkingDir()

	// Wrap script with ulimit resource caps.
	script := e.wrapWithLimits(input.Script)

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()

	log.Infof("[local] running job %s (timeout=%v, sandbox=%s)", input.JobID, timeout, e.localCfg.Sandbox)

	exitCode, runErr := runWithSandbox(timeoutCtx, e.localCfg, script, env, cwd, input.OutputChan)

	duration := time.Since(start).Milliseconds()
	decision := "approved"
	if runErr != nil {
		decision = "failed"
	}
	ec := exitCode
	e.writeAudit(auditEntry{
		Time:       start.UTC().Format(time.RFC3339),
		JobID:      input.JobID,
		Script:     input.Script,
		Decision:   decision,
		ExitCode:   &ec,
		DurationMs: duration,
	})

	return exitCode, runErr
}

// blockedPatternCheck returns the matching pattern and true if the script is blocked.
func (e *localExecutor) blockedPatternCheck(script string) (string, bool) {
	for _, pat := range e.localCfg.BlockedPatterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			log.Warnf("[local] invalid blocked_pattern %q: %v", pat, err)
			continue
		}
		if re.MatchString(script) {
			return pat, true
		}
	}
	return "", false
}

var destructiveRe = []*regexp.Regexp{
	regexp.MustCompile(`\brm\b`),
	regexp.MustCompile(`\bmv\b`),
	regexp.MustCompile(`\brmdir\b`),
	regexp.MustCompile(`\btruncate\b`),
	regexp.MustCompile(`>\s*\S`), // output redirect
}

func (e *localExecutor) needsConfirmation(script string) bool {
	switch e.localCfg.Confirm {
	case "always":
		return true
	case "never":
		return false
	default: // "destructive"
		for _, re := range destructiveRe {
			if re.MatchString(script) {
				return true
			}
		}
		return false
	}
}

// buildEnv constructs a minimal, safe environment for the subprocess.
// Dangerous loader variables are stripped; job-provided vars are merged on top.
func (e *localExecutor) buildEnv(jobEnvVars map[string]string) []string {
	home := e.localCfg.WorkingDir()

	env := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + home,
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
	}

	// Merge job-provided env vars (may override defaults).
	for k, v := range jobEnvVars {
		env = append(env, k+"="+v)
	}
	return env
}

// wrapWithLimits wraps the script with ulimit resource caps.
func (e *localExecutor) wrapWithLimits(script string) string {
	cpu := e.localCfg.MaxTimeout
	// 1 GB in KB for ulimit -v
	const memKB = 1048576
	return fmt.Sprintf("ulimit -t %d -v %d 2>/dev/null\n%s", cpu, memKB, script)
}

// writeAudit appends a JSON audit entry to the configured audit log.
func (e *localExecutor) writeAudit(entry auditEntry) {
	logPath := e.localCfg.AuditLog
	if logPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		log.Warnf("[local] audit log mkdir: %v", err)
		return
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Warnf("[local] audit log open: %v", err)
		return
	}
	defer f.Close()
	data, _ := json.Marshal(entry)
	f.Write(append(data, '\n'))
}

// writeScriptTempFile writes script to a temporary file and returns its path.
// The caller is responsible for removing it with the returned cleanup function.
func writeScriptTempFile(script string) (string, func(), error) {
	f, err := os.CreateTemp("", "wf-script-*.sh")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp script: %w", err)
	}
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("write temp script: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("close temp script: %w", err)
	}
	if err := os.Chmod(f.Name(), 0o700); err != nil {
		os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("chmod temp script: %w", err)
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// execAndStream starts cmd, streams its stdout+stderr to outputChan, waits for
// completion, and returns the exit code.
func execAndStream(ctx context.Context, cmd *exec.Cmd, outputChan chan<- string) (int, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("start: %w", err)
	}

	var wg sync.WaitGroup
	stream := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			if outputChan != nil {
				outputChan <- scanner.Text() + "\n"
			}
		}
	}
	wg.Add(2)
	go stream(stdout)
	go stream(stderr)
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return -1, fmt.Errorf("wait: %w", err)
	}
	return 0, nil
}
