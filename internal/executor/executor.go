package executor

import (
	"context"
	"time"

	"workflowfiesta-runner/internal/config"
)

// Input contains everything needed to run a script in a container.
type Input struct {
	Image      string
	Script     string
	EnvVars    map[string]string
	Timeout    time.Duration
	OutputChan chan<- string
}

// Executor runs a script in an isolated container environment.
type Executor interface {
	Execute(ctx context.Context, input Input) (exitCode int, err error)
}

// New returns a Docker or Kubernetes executor based on config.
func New(cfg *config.Config) Executor {
	if cfg.ExecutorType == "kubernetes" {
		return &kubernetesExecutor{cfg: cfg}
	}
	return &dockerExecutor{cfg: cfg}
}
