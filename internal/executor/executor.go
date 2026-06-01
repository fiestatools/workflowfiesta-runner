package executor

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	"workflowfiesta-runner/internal/config"
)

// Input contains everything needed to run a script.
type Input struct {
	JobID      string
	Image      string
	Script     string
	EnvVars    map[string]string
	Timeout    time.Duration
	OutputChan chan<- string
	// WorkDir overrides the default working directory for this job.
	// When empty, the executor uses localCfg.WorkingDir().
	// Set to a git worktree path to run bash/script jobs inside an isolated checkout.
	WorkDir string
}

// Executor runs a script in an isolated container environment.
type Executor interface {
	Execute(ctx context.Context, input Input) (exitCode int, err error)
}

// New returns an executor based on config.
func New(cfg *config.Config) Executor {
	if cfg.ExecutorType == "local" {
		log.Infof("executor selected: local")
		lc := cfg.LocalConfig
		if lc != nil {
			lc.RunnerName = cfg.Name
		}
		return newLocalExecutor(lc)
	}
	if cfg.ExecutorType == "kubernetes" {
		log.Infof("executor selected: kubernetes")
		return &kubernetesExecutor{cfg: cfg}
	}
	log.Infof("executor selected: docker")
	return &dockerExecutor{cfg: cfg}
}

// NewWithClient returns an executor based on config, passing an optional ApprovalReporter
// to the local executor for API-side approval state reporting.
func NewWithClient(cfg *config.Config, client ApprovalReporter) Executor {
	if cfg.ExecutorType == "local" {
		log.Infof("executor selected: local")
		lc := cfg.LocalConfig
		if lc != nil {
			lc.RunnerName = cfg.Name
		}
		return NewLocal(lc, client)
	}
	if cfg.ExecutorType == "kubernetes" {
		log.Infof("executor selected: kubernetes")
		return &kubernetesExecutor{cfg: cfg}
	}
	log.Infof("executor selected: docker")
	return &dockerExecutor{cfg: cfg}
}
