# Configuration

Configuration is loaded by `internal/config.Load()` at startup. Values are read from environment variables first; if `WORKFLOWFIESTA_TOKEN` is absent, the credentials file is consulted as a fallback.

---

## Environment Variables

### Required

| Variable | Description |
|---|---|
| `WORKFLOWFIESTA_TOKEN` | Runner authentication token obtained during registration. Required for all `run` / `run-local` commands. |

### Common

| Variable | Default | Description |
|---|---|---|
| `WORKFLOWFIESTA_API_URL` | `https://app.workflowfiesta.com` | Base URL of the WorkflowFiesta API. Do not include a trailing slash. |
| `WORKFLOWFIESTA_RUNNER_ID` | *(empty)* | UUID of this runner record in the database. Set automatically by `register`. |
| `WORKFLOWFIESTA_RUNNER_NAME` | `unnamed-runner` | Display name shown in the web UI. |
| `WORKFLOWFIESTA_LABELS` | *(empty)* | Comma-separated labels, e.g. `prod,linux,gpu`. Used for runner group membership. |
| `WORKFLOWFIESTA_MAX_CONCURRENT_JOBS` | `4` | Maximum number of jobs that can run simultaneously on this runner. |

### Executor Selection

| Variable | Default | Description |
|---|---|---|
| `CONTAINER_RUNTIME` | *(auto-detected)* | Override executor type: `docker`, `kubernetes`, or `local`. |

**Auto-detection order:**
1. If `CONTAINER_RUNTIME` is set, use it.
2. Else if `KUBERNETES_SERVICE_HOST` is set (running inside a cluster), use `kubernetes`.
3. Else if `runtime.GOOS == "windows"`, use `local`.
4. Otherwise use `docker`.

### Docker Executor

| Variable | Default | Description |
|---|---|---|
| `DOCKER_SOCKET` | `/var/run/docker.sock` | Path to the Docker Engine Unix socket. |

### Kubernetes Executor

| Variable | Default | Description |
|---|---|---|
| `KUBERNETES_NAMESPACE` | `default` | Namespace in which to create Kubernetes Jobs. |
| `KUBERNETES_IMAGE_PULL_SECRET` | *(empty)* | Name of an `imagePullSecrets` entry to attach to every Job pod. |

### Local Executor

| Variable | Default | Description |
|---|---|---|
| `LOCAL_CONFIG_PATH` | `~/.workflowfiesta/runner.yaml` | Path to the local executor YAML config. Only relevant for `run-local` (or `CONTAINER_RUNTIME=local`). |

---

## Credentials File Fallback

When `WORKFLOWFIESTA_TOKEN` is not set in the environment, the runner reads `~/.workflowfiesta/credentials.env`. This file is written automatically by the GUI registration wizard and by `register-local`.

**File format** (shell export lines, one per line):
```bash
export WORKFLOWFIESTA_API_URL=https://your-instance.workflowfiesta.com
export WORKFLOWFIESTA_TOKEN=wfr_xxxxxxxxxxxxxxxxxxxx
export WORKFLOWFIESTA_RUNNER_ID=abc12345-...
export WORKFLOWFIESTA_RUNNER_NAME=my-laptop
```

- Lines without `=` and lines where the value is empty are skipped.
- `export ` prefixes are stripped automatically.
- The file is loaded only for the four recognized variables above; other lines are ignored.
- File permissions: 0600 (user-read-only). The directory `~/.workflowfiesta/` has permissions 0700.

**Priority:** environment variables always take precedence over the credentials file. If `WORKFLOWFIESTA_TOKEN` is set in the shell, the file is never read.

---

## Config Struct

```go
type Config struct {
    APIURL              string
    Token               string
    RunnerID            string
    Name                string
    DockerSocket        string
    Labels              []string
    ExecutorType        string   // "docker", "kubernetes", "local"
    KubeNamespace       string
    KubeImagePullSecret string
    LocalConfigPath     string
    LocalConfig         *localconfig.LocalConfig  // populated by run-local
    MaxConcurrentJobs   int
}
```

`LocalConfig` is set by `run-local` after reading `runner.yaml` via `localconfig.Load()`. It is `nil` for Docker and Kubernetes modes.

---

## Files Written by the Runner

| Path | Mode | Written by |
|---|---|---|
| `~/.workflowfiesta/credentials.env` | 0600 | GUI wizard (`register-local`, auto-launch wizard) |
| `~/.workflowfiesta/runner.yaml` | 0600 | `init-local`, `register-local`, Settings window save |
| `~/.workflowfiesta/audit.log` | 0600 | Local executor (one JSON line per job) |
