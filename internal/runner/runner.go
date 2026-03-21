package runner

import (
	"context"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"workflowfiesta-runner/internal/api"
	"workflowfiesta-runner/internal/config"
	"workflowfiesta-runner/internal/executor"
)

// StatusSink receives runner lifecycle events for display in a UI or CLI.
type StatusSink interface {
	SetConnected(connected bool)
	SetIdle()
	SetJobRunning(jobID, image, scriptBlurb string)
	SetJobComplete(jobID, image string, exitCode int)
	AppendLog(line string)
}

type Runner struct {
	cfg        *config.Config
	client     *api.Client
	executor   executor.Executor
	sinks      []StatusSink
	activeJobs sync.Map     // jobID -> struct{}: deduplicates re-dispatched jobs
	semaphore  chan struct{} // limits concurrent job executions
}

func New(cfg *config.Config) *Runner {
	apiURL := cfg.APIURL
	client := api.New(apiURL, cfg.Token)
	maxJobs := cfg.MaxConcurrentJobs
	if maxJobs <= 0 {
		maxJobs = 4
	}
	return &Runner{
		cfg:       cfg,
		client:    client,
		executor:  executor.NewWithClient(cfg, client),
		semaphore: make(chan struct{}, maxJobs),
	}
}

// WithSink adds a StatusSink that receives lifecycle events from this runner.
// Multiple sinks can be added; events are fanned out to all of them.
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
	// Connect with retry
	r.client.ConnectWithRetry(ctx)
	r.notify(func(s StatusSink) { s.SetConnected(true) })

	// Start heartbeat goroutine
	go r.heartbeatLoop(ctx)

	// Job channel
	jobChan := make(chan api.Job, 10)

	// Start listener
	go r.client.Listen(ctx, jobChan)

	// Process jobs
	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			r.client.Close()
			r.notify(func(s StatusSink) { s.SetConnected(false) })
			return ctx.Err()
		case job := <-jobChan:
			if _, loaded := r.activeJobs.LoadOrStore(job.JobID, struct{}{}); loaded {
				log.Infof("Skipping duplicate dispatch of job %s (already running)", job.JobID)
				continue
			}
			wg.Add(1)
			go func(j api.Job) {
				defer wg.Done()
				defer r.activeJobs.Delete(j.JobID)
				// Acquire concurrency slot before executing
				select {
				case r.semaphore <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-r.semaphore }()
				r.handleJob(ctx, j)
			}(job)
		}
	}
}

func scriptBlurb(script string) string {
	// Return first non-empty line, capped at 80 chars.
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
	log.Infof("Starting job %s (image: %s)", job.JobID, job.DockerImage)

	blurb := scriptBlurb(job.Script)
	r.notify(func(s StatusSink) { s.SetJobRunning(job.JobID, job.DockerImage, blurb) })

	if err := r.client.ReportJobClaimed(job.JobID); err != nil {
		log.Warnf("Failed to claim job %s: %v", job.JobID, err)
	}

	outputChan := make(chan string, 100)
	doneChan := make(chan struct{})

	timeout := time.Duration(job.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	// Collect and stream output concurrently
	var outputBuilder strings.Builder
	go func() {
		defer close(doneChan)
		for chunk := range outputChan {
			outputBuilder.WriteString(chunk)
			r.client.StreamOutput(job.JobID, chunk)
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
	})
	close(outputChan)
	<-doneChan

	if err != nil {
		log.Errorf("Job %s failed: %v", job.JobID, err)
		r.client.ReportJobFailed(job.JobID, err.Error())
		r.notify(func(s StatusSink) { s.SetJobComplete(job.JobID, job.DockerImage, 1) })
		r.notify(func(s StatusSink) {
			s.AppendLog("[error] " + err.Error() + "\n")
			s.SetIdle()
		})
		return
	}

	output := outputBuilder.String()
	log.Infof("Job %s completed with exit code %d", job.JobID, exitCode)
	r.client.ReportJobComplete(job.JobID, exitCode, output)
	r.notify(func(s StatusSink) { s.SetJobComplete(job.JobID, job.DockerImage, exitCode) })
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
			if err := r.client.SendHeartbeat(); err != nil {
				log.Warnf("Heartbeat failed: %v", err)
			}
			// WebSocket-level ping so the server's auto-pong resets our read
			// deadline. This detects silent TCP drops within ~75s.
			if err := r.client.SendPing(); err != nil {
				log.Warnf("Ping failed: %v", err)
			}
		}
	}
}
