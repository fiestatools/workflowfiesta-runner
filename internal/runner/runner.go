package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"workflowfiesta-runner/internal/api"
	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/executor"
	"workflowfiesta-runner/internal/localconfig"
)

// RunnerCapabilities advertises which structured-tool features this binary supports.
// Checked by the platform before routing tool jobs; old runners with empty capabilities
// receive bash-script jobs only.
var RunnerCapabilities = []string{"tool_dispatch", "script_library", "git_worktrees"}

// StatusSink receives runner lifecycle events for display in a UI or CLI.
type StatusSink interface {
	SetConnected(connected bool)
	SetIdle()
	SetJobRunning(jobID, image, scriptBlurb string)
	SetJobComplete(jobID, image string, exitCode int)
	AppendLog(line string)
}

type Runner struct {
	cfg         *config.Config
	client      *api.Client
	executor    executor.Executor
	toolHandler *executor.ToolHandler
	sinks       []StatusSink
	activeJobs  sync.Map     // jobID -> struct{}: deduplicates concurrent polls
	semaphore   chan struct{} // limits concurrent job executions
}

func New(cfg *config.Config) *Runner {
	client := api.New(cfg.APIURL, cfg.Token)
	maxJobs := cfg.MaxConcurrentJobs
	if maxJobs <= 0 {
		maxJobs = 4
	}
	return &Runner{
		cfg:         cfg,
		client:      client,
		executor:    executor.NewWithClient(cfg, client),
		toolHandler: executor.NewToolHandler(cfg.LocalConfig),
		semaphore:   make(chan struct{}, maxJobs),
	}
}

// WithSink adds a StatusSink that receives lifecycle events from this runner.
func (r *Runner) WithSink(sink StatusSink) *Runner {
	r.sinks = append(r.sinks, sink)
	return r
}

func (r *Runner) notify(fn func(StatusSink)) {
	for _, s := range r.sinks {
		fn(s)
	}
}

func (r *Runner) Run(ctx context.Context) error {
	log.Info("[runner] starting HTTP poll loop")

	// Send initial heartbeat so the API marks us online immediately.
	// Response includes org_id — use it to namespace the script library.
	orgID, err := r.client.SendHeartbeat("idle", RunnerCapabilities, runtime.GOOS, runtime.GOARCH, r.cfg.Version)
	if err != nil {
		log.Warnf("[runner] initial heartbeat failed: %v", err)
	} else if orgID != "" {
		r.toolHandler.SetOrgID(orgID)
		r.toolHandler.SetSyncer(r.client)
		// Persist org_id to runner.yaml if it changed.
		if r.cfg.LocalConfig != nil && r.cfg.LocalConfig.OrgID != orgID {
			r.cfg.LocalConfig.OrgID = orgID
			if saveErr := localconfig.Save(r.cfg.LocalConfig, localconfig.DefaultPath()); saveErr != nil {
				log.Warnf("[runner] failed to persist org_id: %v", saveErr)
			}
		}
		// Pull server scripts on startup — bootstrap org library.
		go r.syncServerScripts(orgID)
	}
	r.notify(func(s StatusSink) { s.SetConnected(true) })

	go r.heartbeatLoop(ctx)

	// Poll for jobs every 3 seconds
	pollTicker := time.NewTicker(3 * time.Second)
	defer pollTicker.Stop()

	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			r.notify(func(s StatusSink) { s.SetConnected(false) })
			return ctx.Err()
		case <-pollTicker.C:
			job, err := r.client.PollNextJob()
			if err != nil {
				log.Warnf("[runner] poll error: %v", err)
				continue
			}
			if job == nil {
				continue // no pending job
			}

			if _, loaded := r.activeJobs.LoadOrStore(job.JobID, struct{}{}); loaded {
				log.Infof("[runner] skipping already-running job %s", job.JobID)
				continue
			}

			wg.Add(1)
			go func(j api.Job) {
				defer wg.Done()
				defer r.activeJobs.Delete(j.JobID)
				select {
				case r.semaphore <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-r.semaphore }()
				r.handleJob(ctx, j)
			}(*job)
		}
	}
}

func scriptBlurb(script string) string {
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 80 {
			return line[:77] + "…"
		}
		return line
	}
	return script
}

func (r *Runner) handleJob(ctx context.Context, job api.Job) {
	// ── run_local_script: expand from library, then run through security-gated executor ──
	if job.ToolName == "run_local_script" {
		r.handleRunLocalScript(ctx, job)
		return
	}

	// ── Other structured tool dispatch ────────────────────────────────────────
	// When tool_name is set, execute natively without spawning a subprocess.
	if job.ToolName != "" {
		r.handleToolJob(job)
		return
	}

	// ── Standard bash script execution ───────────────────────────────────────
	log.Infof("[runner] starting job %s (image: %s)", job.JobID, job.DockerImage)

	blurb := scriptBlurb(job.Script)
	r.notify(func(s StatusSink) { s.SetJobRunning(job.JobID, job.DockerImage, blurb) })

	outputChan := make(chan string, 100)
	doneChan := make(chan struct{})

	timeout := time.Duration(job.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	// If the job has a git repo, provision a worktree and run bash inside it.
	var bashWorkDir string
	if job.GitRepoURL != "" {
		wtPath, wtErr := EnsureWorktree(job.GitRepoURL, job.GitRef, job.JobID)
		if wtErr != nil {
			log.Errorf("[runner] worktree setup for bash job %s failed: %v", job.JobID, wtErr)
			if reportErr := r.client.ReportJobFailed(job.JobID, "worktree setup: "+wtErr.Error()); reportErr != nil {
				log.Warnf("[runner] report-fail error: %v", reportErr)
			}
			r.notify(func(s StatusSink) { s.SetJobComplete(job.JobID, job.DockerImage, 1) })
			r.notify(func(s StatusSink) { s.SetIdle() })
			return
		}
		defer CleanupWorktree(job.GitRepoURL, job.JobID)
		if reportErr := r.client.ReportWorktreePath(job.JobID, wtPath); reportErr != nil {
			log.Warnf("[runner] report worktree path: %v", reportErr)
		}
		bashWorkDir = wtPath
	}

	var outputBuilder strings.Builder
	go func() {
		defer close(doneChan)
		for chunk := range outputChan {
			outputBuilder.WriteString(chunk)
			if err := r.client.StreamOutput(job.JobID, chunk); err != nil {
				log.Warnf("[runner] stream output error: %v", err)
			}
			r.notify(func(s StatusSink) { s.AppendLog(chunk) })
		}
	}()

	exitCode, err := r.executor.Execute(ctx, executor.Input{
		JobID:      job.JobID,
		Image:      job.DockerImage,
		Script:     job.Script,
		EnvVars:    job.EnvVars,
		Timeout:    timeout,
		OutputChan: outputChan,
		WorkDir:    bashWorkDir,
	})
	close(outputChan)
	<-doneChan

	if err != nil {
		log.Errorf("[runner] job %s failed: %v", job.JobID, err)
		if reportErr := r.client.ReportJobFailed(job.JobID, err.Error()); reportErr != nil {
			log.Warnf("[runner] report-fail error: %v", reportErr)
		}
		r.notify(func(s StatusSink) { s.SetJobComplete(job.JobID, job.DockerImage, 1) })
		r.notify(func(s StatusSink) {
			s.AppendLog("[error] " + err.Error() + "\n")
			s.SetIdle()
		})
		return
	}

	output := outputBuilder.String()
	log.Infof("[runner] job %s completed with exit code %d", job.JobID, exitCode)
	if reportErr := r.client.ReportJobComplete(job.JobID, exitCode, output); reportErr != nil {
		log.Warnf("[runner] report-complete error: %v", reportErr)
	}
	r.notify(func(s StatusSink) { s.SetJobComplete(job.JobID, job.DockerImage, exitCode) })
	r.notify(func(s StatusSink) { s.SetIdle() })
}

// handleRunLocalScript loads a script from the local library and executes it through the
// security-gated executor (approval gates, resource limits) so it behaves like a normal job.
func (r *Runner) handleRunLocalScript(ctx context.Context, job api.Job) {
	scriptName := ""
	if job.ToolArgs != nil {
		if n, ok := job.ToolArgs["name"].(string); ok {
			scriptName = n
		}
	}
	if scriptName == "" {
		log.Errorf("[runner] run_local_script job %s: missing name argument", job.JobID)
		_ = r.client.ReportJobFailed(job.JobID, "run_local_script: name argument is required")
		r.notify(func(s StatusSink) { s.SetIdle() })
		return
	}

	scriptContent, err := r.toolHandler.LoadLocalScript(scriptName)
	if err != nil {
		log.Errorf("[runner] run_local_script job %s: %v", job.JobID, err)
		_ = r.client.ReportJobFailed(job.JobID, err.Error())
		r.notify(func(s StatusSink) { s.SetIdle() })
		return
	}

	// Merge env_vars from tool_args into job.EnvVars so the script receives them.
	if envVarsRaw, ok := job.ToolArgs["env_vars"].(map[string]interface{}); ok {
		if job.EnvVars == nil {
			job.EnvVars = make(map[string]string)
		}
		for k, v := range envVarsRaw {
			if s, ok := v.(string); ok {
				job.EnvVars[k] = s
			}
		}
	}

	// Re-run as a standard bash job with the loaded script content.
	job.ToolName = ""
	job.Script = scriptContent
	r.handleJob(ctx, job)
}

// syncServerScripts pulls the org's server-side script library and writes any
// scripts that are missing or older than the server version to the local library.
func (r *Runner) syncServerScripts(orgID string) {
	scripts, err := r.client.ListServerScripts()
	if err != nil {
		log.Warnf("[runner] script library sync failed (list): %v", err)
		return
	}
	if len(scripts) == 0 {
		return
	}

	home, _ := os.UserHomeDir()
	libDir := filepath.Join(home, ".workflowfiesta", "scripts", orgID)
	if mkErr := os.MkdirAll(libDir, 0o755); mkErr != nil {
		log.Warnf("[runner] script library sync: mkdir failed: %v", mkErr)
		return
	}

	synced := 0
	for _, meta := range scripts {
		scriptPath := filepath.Join(libDir, meta.Name)
		needsSync := false
		if info, statErr := os.Stat(scriptPath); os.IsNotExist(statErr) {
			needsSync = true
		} else if statErr == nil && info.ModTime().Before(meta.UpdatedAt) {
			needsSync = true
		}
		if !needsSync {
			continue
		}
		content, fetchErr := r.client.GetScript(meta.Name)
		if fetchErr != nil {
			log.Warnf("[runner] script library sync: failed to fetch %q: %v", meta.Name, fetchErr)
			continue
		}
		if writeErr := os.WriteFile(scriptPath, []byte(content), 0o755); writeErr != nil {
			log.Warnf("[runner] script library sync: failed to write %q: %v", meta.Name, writeErr)
			continue
		}
		synced++
		log.Infof("[runner] script library sync: pulled %q from server", meta.Name)
	}
	if synced > 0 {
		log.Infof("[runner] script library sync complete: %d/%d scripts updated", synced, len(scripts))
	}
}

// handleToolJob executes a structured tool job natively without spawning a subprocess.
// For run_local_script, it expands the script from the library and then routes back through
// the security-gated executor so approval gates still apply.
func (r *Runner) handleToolJob(job api.Job) {
	log.Infof("[runner] native tool job %s: %s", job.JobID, job.ToolName)
	r.notify(func(s StatusSink) { s.SetJobRunning(job.JobID, "", job.ToolName) })

	// If the job has a git repo, create an isolated worktree and scope file tools to it.
	if job.GitRepoURL != "" {
		wtPath, err := EnsureWorktree(job.GitRepoURL, job.GitRef, job.JobID)
		if err != nil {
			log.Errorf("[runner] worktree setup for job %s failed: %v", job.JobID, err)
			if reportErr := r.client.ReportJobFailed(job.JobID, "worktree setup: "+err.Error()); reportErr != nil {
				log.Warnf("[runner] report-fail error: %v", reportErr)
			}
			r.notify(func(s StatusSink) { s.SetJobComplete(job.JobID, "", 1) })
			r.notify(func(s StatusSink) { s.SetIdle() })
			return
		}
		defer CleanupWorktree(job.GitRepoURL, job.JobID)

		if reportErr := r.client.ReportWorktreePath(job.JobID, wtPath); reportErr != nil {
			log.Warnf("[runner] report worktree path error: %v", reportErr)
		}

		r.toolHandler.SetWorktreeRoot(wtPath)
		defer r.toolHandler.SetWorktreeRoot("")
	}

	var toolArgsRaw json.RawMessage
	if job.ToolArgs != nil {
		data, _ := json.Marshal(job.ToolArgs)
		toolArgsRaw = data
	}

	result, err := r.toolHandler.Execute(job.ToolName, toolArgsRaw)
	if err != nil {
		log.Errorf("[runner] tool job %s (%s) error: %v", job.JobID, job.ToolName, err)
		if reportErr := r.client.ReportJobFailed(job.JobID, err.Error()); reportErr != nil {
			log.Warnf("[runner] report-fail error: %v", reportErr)
		}
		r.notify(func(s StatusSink) { s.SetJobComplete(job.JobID, "", 1) })
		r.notify(func(s StatusSink) { s.SetIdle() })
		return
	}

	log.Infof("[runner] tool job %s (%s) complete", job.JobID, job.ToolName)
	r.notify(func(s StatusSink) { s.AppendLog(result + "\n") })
	if reportErr := r.client.ReportJobComplete(job.JobID, 0, result); reportErr != nil {
		log.Warnf("[runner] report-complete error: %v", reportErr)
	}
	r.notify(func(s StatusSink) { s.SetJobComplete(job.JobID, "", 0) })
	r.notify(func(s StatusSink) { s.SetIdle() })
}

func (r *Runner) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Report busy if any active jobs, otherwise idle
			status := "idle"
			r.activeJobs.Range(func(_, _ interface{}) bool {
				status = "busy"
				return false // stop after first
			})
			if _, err := r.client.SendHeartbeat(status, RunnerCapabilities, runtime.GOOS, runtime.GOARCH, r.cfg.Version); err != nil {
				log.Warnf("[runner] heartbeat failed: %v", err)
			}
		}
	}
}
