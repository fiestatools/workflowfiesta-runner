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
	activeJobs sync.Map     // jobID -> struct{}: deduplicates concurrent polls
	semaphore  chan struct{} // limits concurrent job executions
}

func New(cfg *config.Config) *Runner {
	client := api.New(cfg.APIURL, cfg.Token)
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

	// Send initial heartbeat so the API marks us online immediately
	if err := r.client.SendHeartbeat("idle"); err != nil {
		log.Warnf("[runner] initial heartbeat failed: %v", err)
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
	log.Infof("[runner] starting job %s (image: %s)", job.JobID, job.DockerImage)

	blurb := scriptBlurb(job.Script)
	r.notify(func(s StatusSink) { s.SetJobRunning(job.JobID, job.DockerImage, blurb) })

	outputChan := make(chan string, 100)
	doneChan := make(chan struct{})

	timeout := time.Duration(job.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
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
			if err := r.client.SendHeartbeat(status); err != nil {
				log.Warnf("[runner] heartbeat failed: %v", err)
			}
		}
	}
}
