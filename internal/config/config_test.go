package config_test

import (
	"os"
	"testing"

	"workflowfiesta-runner/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	os.Unsetenv("WORKFLOWFIESTA_API_URL")
	os.Unsetenv("WORKFLOWFIESTA_TOKEN")
	os.Unsetenv("DOCKER_SOCKET")

	cfg := config.Load()

	if cfg.APIURL != "http://localhost:3001" {
		t.Errorf("expected default API URL, got %s", cfg.APIURL)
	}
	if cfg.DockerSocket != "/var/run/docker.sock" {
		t.Errorf("expected default docker socket, got %s", cfg.DockerSocket)
	}
	if cfg.Token != "" {
		t.Errorf("expected empty token, got %s", cfg.Token)
	}
}

func TestLoad_EnvVars(t *testing.T) {
	os.Setenv("WORKFLOWFIESTA_API_URL", "http://my-api:3001")
	os.Setenv("WORKFLOWFIESTA_TOKEN", "my-token")
	os.Setenv("WORKFLOWFIESTA_LABELS", "linux,x86_64,gpu")
	defer func() {
		os.Unsetenv("WORKFLOWFIESTA_API_URL")
		os.Unsetenv("WORKFLOWFIESTA_TOKEN")
		os.Unsetenv("WORKFLOWFIESTA_LABELS")
	}()

	cfg := config.Load()

	if cfg.APIURL != "http://my-api:3001" {
		t.Errorf("expected custom API URL, got %s", cfg.APIURL)
	}
	if cfg.Token != "my-token" {
		t.Errorf("expected token, got %s", cfg.Token)
	}
	if len(cfg.Labels) != 3 {
		t.Errorf("expected 3 labels, got %d", len(cfg.Labels))
	}
}
