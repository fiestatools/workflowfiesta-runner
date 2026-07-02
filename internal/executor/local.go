package executor

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"workflowfiesta-runner/internal/auditlog"
	"workflowfiesta-runner/internal/localconfig"
	"workflowfiesta-runner/internal/localui"
)

// ApprovalReporter is an optional interface for reporting approval state to the API.
type ApprovalReporter interface {
	ReportApprovalPending(jobID, runnerName string) error
	ReportApprovalResolved(jobID string, approved bool) error
}

type localExecutor struct {
	localCfg      *localconfig.LocalConfig
	apiClient     ApprovalReporter
	sessionAllows []string
	mu            sync.Mutex
	basePATH      string // sanitized once at construction, reused per job
}

func newLocalExecutor(cfg *localconfig.LocalConfig) *localExecutor {
	if cfg == nil {
		cfg = localconfig.Default()
	}
	return &localExecutor{localCfg: cfg, basePATH: buildBasePATH()}
}

// NewLocal creates a localExecutor with an optional ApprovalReporter client.
func NewLocal(cfg *localconfig.LocalConfig, client ApprovalReporter) *localExecutor {
	if cfg == nil {
		cfg = localconfig.Default()
	}
	return &localExecutor{localCfg: cfg, apiClient: client, basePATH: buildBasePATH()}
}

// scriptFingerprint returns a hex SHA-256 fingerprint of the script.
func scriptFingerprint(script string) string {
	h := sha256.Sum256([]byte(script))
	return fmt.Sprintf("%x", h)
}

func (e *localExecutor) isSessionAllowed(script string) bool {
	fp := scriptFingerprint(script)
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.sessionAllows {
		if s == fp {
			return true
		}
	}
	return false
}

func (e *localExecutor) isAlwaysAllowed(script string) bool {
	fp := scriptFingerprint(script)
	for _, s := range e.localCfg.AlwaysAllowedPatterns {
		if s == fp {
			return true
		}
	}
	return false
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

	// emit sends msg to the output channel so it appears in the step's output log.
	emit := func(msg string) {
		if input.OutputChan != nil {
			input.OutputChan <- msg
		}
	}

	// Layer 1: Blocked pattern check.
	if pattern, blocked := e.blockedPatternCheck(input.Script); blocked {
		e.writeAudit(auditEntry{
			Time:     start.UTC().Format(time.RFC3339),
			JobID:    input.JobID,
			Script:   input.Script,
			Decision: "blocked",
			Reason:   "blocked_pattern:" + pattern,
		})
		emit(fmt.Sprintf("[runner] script blocked by pattern %q — update your runner's blocked_patterns config to allow this script\n", pattern))
		return 1, nil
	}

	// Layer 2: Confirmation gate.
	if e.needsConfirmation(input.Script) {
		log.Infof("[local] job %s requires approval (confirm=%s)", input.JobID, e.localCfg.Confirm)
		if e.apiClient != nil {
			_ = e.apiClient.ReportApprovalPending(input.JobID, e.localCfg.RunnerName)
		}
		timeout := time.Duration(e.localCfg.ConfirmTimeout) * time.Second
		if e.localCfg.ConfirmNeverTimeout {
			timeout = 0 // signal to approval UI: wait indefinitely
		}
		result := localui.RequestApproval(localui.ApprovalRequest{
			JobID:      input.JobID,
			Script:     input.Script,
			RunnerName: e.localCfg.RunnerName,
			Timeout:    timeout,
			PlaySound:  e.localCfg.SoundOnApproval,
			Callbacks: localui.ApprovalCallbacks{
				OnResolved: func(approved bool) {
					if e.apiClient != nil {
						_ = e.apiClient.ReportApprovalResolved(input.JobID, approved)
					}
				},
			},
		})
		log.Infof("[local] job %s approval result: %v", input.JobID, result)
		switch result {
		case localui.ApprovalDeny:
			log.Warnf("[local] job %s denied by local operator", input.JobID)
			e.writeAudit(auditEntry{
				Time:     start.UTC().Format(time.RFC3339),
				JobID:    input.JobID,
				Script:   input.Script,
				Decision: "denied",
				Reason:   "user_denied",
			})
			emit("[runner] job denied by local operator — approve the job in the runner's approval dialog to allow execution\n")
			return 1, nil
		case localui.ApprovalAllowSession:
			fp := scriptFingerprint(input.Script)
			e.mu.Lock()
			e.sessionAllows = append(e.sessionAllows, fp)
			e.mu.Unlock()
		case localui.ApprovalAlwaysAllow:
			fp := scriptFingerprint(input.Script)
			e.mu.Lock()
			e.localCfg.AlwaysAllowedPatterns = append(e.localCfg.AlwaysAllowedPatterns, fp)
			e.mu.Unlock()
			if saveErr := localconfig.Save(e.localCfg, localconfig.DefaultPath()); saveErr != nil {
				log.Warnf("[local] failed to save always-allow config: %v", saveErr)
			}
			// ApprovalAllow falls through to execution
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

	// CWD = worktree override if set, otherwise first writable allowed path.
	cwd := e.localCfg.WorkingDir()
	if input.WorkDir != "" {
		cwd = input.WorkDir
	}

	// Wrap script with ulimit resource caps (Unix only; ulimit is not available on Windows).
	script := input.Script
	if runtime.GOOS != "windows" {
		script = e.wrapWithLimits(input.Script)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()

	log.Infof("[local] running job %s (timeout=%v, sandbox=%s, cwd=%s)", input.JobID, timeout, e.localCfg.Sandbox, cwd)
	log.Debugf("[local] effective PATH: %s", e.effectivePATH())

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
		// /dev/ paths are not real device nodes on Windows — skip to avoid false positives like 2>/dev/null.
		if runtime.GOOS == "windows" && strings.Contains(pat, `/dev/`) {
			log.Debugf("[local] skipping Unix device pattern %q on Windows", pat)
			continue
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			log.Warnf("[local] invalid blocked_pattern %q: %v", pat, err)
			continue
		}
		// /dev/null is a harmless sink on any OS — neutralize it before testing
		testScript := script
		if strings.Contains(pat, `/dev/`) {
			testScript = strings.ReplaceAll(script, "/dev/null", "")
		}
		if re.MatchString(testScript) {
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
	// Session and always-allow lists bypass confirmation.
	if e.isSessionAllowed(script) || e.isAlwaysAllowed(script) {
		return false
	}
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

// buildBasePATH computes the sanitized platform PATH once at startup.
// The warning about stripped Windows Store stubs is logged here so it appears
// once in the runner log rather than once per job.
func buildBasePATH() string {
	if runtime.GOOS == "darwin" {
		return "/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	}
	if runtime.GOOS == "windows" {
		raw := os.Getenv("PATH")
		if raw == "" {
			raw = `C:\Windows\System32;C:\Windows;C:\Windows\System32\Wbem`
		}
		return sanitizeWindowsPATH(raw)
	}
	return "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

// effectivePATH returns the subprocess PATH for this job: the sanitized base
// PATH (computed once at startup) with any discovered interpreter dirs prepended.
func (e *localExecutor) effectivePATH() string {
	return e.prependDiscoveredDirs(e.basePATH)
}

// prependDiscoveredDirs prepends the parent directories of any configured
// interpreter paths to base, deduplicating entries as it goes.
func (e *localExecutor) prependDiscoveredDirs(base string) string {
	if len(e.localCfg.Interpreters) == 0 {
		return base
	}
	seen := make(map[string]struct{})
	var extra []string
	for _, path := range e.localCfg.Interpreters {
		if path == "" {
			continue
		}
		dir := filepath.Dir(path)
		if _, ok := seen[dir]; !ok {
			seen[dir] = struct{}{}
			extra = append(extra, dir)
		}
	}
	if len(extra) == 0 {
		return base
	}
	return strings.Join(extra, string(filepath.ListSeparator)) +
		string(filepath.ListSeparator) + base
}

// buildEnv constructs a minimal, safe environment for the subprocess.
// Dangerous loader variables are stripped; job-provided vars are merged on top.
func (e *localExecutor) buildEnv(jobEnvVars map[string]string) []string {
	home := e.localCfg.WorkingDir()

	env := make([]string, 0, 4+len(jobEnvVars))
	env = append(env,
		"PATH="+e.effectivePATH(),
		"HOME="+home,
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
	)

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
	if err := auditlog.AppendLine(e.localCfg.AuditLog, entry); err != nil {
		log.Warnf("[local] audit log write: %v", err)
	}
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
		// Default max token size is 64 KB which silently drops longer lines
		// (e.g. a single-line JSON payload with embedded file contents).
		// Use 10 MB to handle large script outputs.
		scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
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
