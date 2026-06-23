package config

import "strings"

// RunnerEnv identifies which WorkflowFiesta environment the runner is connected to.
type RunnerEnv int

const (
	EnvProd      RunnerEnv = iota // https://api.workflowfiesta.com
	EnvStaging                    // https://staging.api.workflowfiesta.com
	EnvLocal                      // http://localhost:5000
	EnvSelfHosted                 // any other / unknown instance
)

// apiPrefixes maps API URL prefixes to their environment.
var apiPrefixes = []struct {
	prefix string
	env    RunnerEnv
}{
	{"https://api.workflowfiesta.com", EnvProd},
	{"https://staging.api.workflowfiesta.com", EnvStaging},
	{"http://localhost:5000", EnvLocal},
	{"http://127.0.0.1:5000", EnvLocal},
}

// webURLs maps each known environment to its canonical frontend URL.
var webURLs = map[RunnerEnv]string{
	EnvProd:    "https://app.workflowfiesta.com",
	EnvStaging: "https://staging.app.workflowfiesta.com",
	EnvLocal:   "http://localhost:3000",
}

// DetectEnv returns the RunnerEnv for the given API URL.
// Returns EnvSelfHosted for any URL that doesn't match a known environment.
func DetectEnv(apiURL string) RunnerEnv {
	normalized := strings.TrimRight(apiURL, "/")
	for _, p := range apiPrefixes {
		if strings.HasPrefix(normalized, p.prefix) {
			return p.env
		}
	}
	return EnvSelfHosted
}

// WebURLFor returns the frontend web URL that corresponds to the given API URL.
// For known environments this is a hardcoded constant. For self-hosted instances
// it returns an empty string since the frontend URL cannot be determined.
func WebURLFor(apiURL string) string {
	return webURLs[DetectEnv(apiURL)]
}
