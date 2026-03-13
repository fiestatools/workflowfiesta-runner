package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"workflowfiesta-runner/internal/localconfig"
)

type Config struct {
	APIURL              string
	Token               string
	RunnerID            string
	Name                string
	DockerSocket        string
	Labels              []string
	ExecutorType        string // "docker", "kubernetes", or "local"
	KubeNamespace       string // KUBERNETES_NAMESPACE
	KubeImagePullSecret string // KUBERNETES_IMAGE_PULL_SECRET
	LocalConfigPath     string // path to runner.yaml (local executor only)
	LocalConfig         *localconfig.LocalConfig // loaded local config (set by run-local)
}

// CredentialsFilePath returns the path to the auto-saved credentials file.
func CredentialsFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".workflowfiesta", "credentials.env")
}

// applyCredentialsFile reads ~/.workflowfiesta/credentials.env (written by the
// GUI registration wizard) and fills in any fields not already set via env vars.
func applyCredentialsFile(cfg *Config) {
	data, err := os.ReadFile(CredentialsFilePath())
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "export ")
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 || kv[1] == "" {
			continue
		}
		switch kv[0] {
		case "WORKFLOWFIESTA_API_URL":
			cfg.APIURL = kv[1]
		case "WORKFLOWFIESTA_TOKEN":
			cfg.Token = kv[1]
		case "WORKFLOWFIESTA_RUNNER_ID":
			cfg.RunnerID = kv[1]
		case "WORKFLOWFIESTA_RUNNER_NAME":
			cfg.Name = kv[1]
		}
	}
}

func Load() *Config {
	labels := []string{}
	if raw := os.Getenv("WORKFLOWFIESTA_LABELS"); raw != "" {
		for _, l := range strings.Split(raw, ",") {
			l = strings.TrimSpace(l)
			if l != "" {
				labels = append(labels, l)
			}
		}
	}

	dockerSocket := os.Getenv("DOCKER_SOCKET")
	if dockerSocket == "" {
		dockerSocket = "/var/run/docker.sock"
	}

	// Determine executor type: explicit override, k8s auto-detect, Windows default, or docker.
	executorType := os.Getenv("CONTAINER_RUNTIME")
	if executorType == "" {
		if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
			executorType = "kubernetes"
		} else if runtime.GOOS == "windows" {
			executorType = "local"
		} else {
			executorType = "docker"
		}
	}

	localConfigPath := os.Getenv("LOCAL_CONFIG_PATH")
	if localConfigPath == "" {
		localConfigPath = localconfig.DefaultPath()
	}

	cfg := &Config{
		APIURL:              getEnv("WORKFLOWFIESTA_API_URL", "http://localhost:3001"),
		Token:               os.Getenv("WORKFLOWFIESTA_TOKEN"),
		RunnerID:            os.Getenv("WORKFLOWFIESTA_RUNNER_ID"),
		Name:                getEnv("WORKFLOWFIESTA_RUNNER_NAME", "unnamed-runner"),
		DockerSocket:        dockerSocket,
		Labels:              labels,
		ExecutorType:        executorType,
		KubeNamespace:       getEnv("KUBERNETES_NAMESPACE", "default"),
		KubeImagePullSecret: os.Getenv("KUBERNETES_IMAGE_PULL_SECRET"),
		LocalConfigPath:     localConfigPath,
	}

	// Fall back to the saved credentials file if no token was found in env vars.
	if cfg.Token == "" {
		applyCredentialsFile(cfg)
	}

	return cfg
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
