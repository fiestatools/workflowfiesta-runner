package config

import (
	"os"
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

	// Determine executor type: explicit override, k8s auto-detect, or docker.
	executorType := os.Getenv("CONTAINER_RUNTIME")
	if executorType == "" {
		if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
			executorType = "kubernetes"
		} else {
			executorType = "docker"
		}
	}

	localConfigPath := os.Getenv("LOCAL_CONFIG_PATH")
	if localConfigPath == "" {
		localConfigPath = localconfig.DefaultPath()
	}

	return &Config{
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
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
