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

type Runner struct {
	cfg      *config.Config
	client   *api.Client
	executor executor.Executor
}

func New(cfg *config.Config) *Runner {
	apiURL := cfg.APIURL
	return &Runner{
		cfg:      cfg,
		client:   api.New(apiURL, cfg.Token),
		executor: executor.New(cfg),
	}
}

func (r *Runner) Run(ctx context.Context) error {
	// Connect with retry
	r.client.ConnectWithRetry(ctx)

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
			return ctx.Err()
		case job := <-jobChan:
			wg.Add(1)
			go func(j api.Job) {
				defer wg.Done()
				r.handleJob(ctx, j)
			}(job)
		}
	}
}

func (r *Runner) handleJob(ctx context.Context, job api.Job) {
	log.Infof("Starting job %s (image: %s)", job.JobID, job.DockerImage)

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
		return
	}

	output := outputBuilder.String()
	log.Infof("Job %s completed with exit code %d", job.JobID, exitCode)
	r.client.ReportJobComplete(job.JobID, exitCode, output)
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
		}
	}
}
