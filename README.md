# WorkflowFiesta Runner

A self-hosted runner for [WorkflowFiesta](https://github.com/testfiesta/workflowfiesta) that connects via HTTP polling and executes jobs on your own infrastructure. Supports Docker, Kubernetes, and local (no-container) execution.

## What it does

The runner:
1. Polls your WorkflowFiesta API for pending jobs every 3 seconds
2. Sends a heartbeat every 30 seconds to report status and capabilities
3. Runs the job script inside an isolated container (Docker or Kubernetes) or directly on the host (local mode)
4. Streams output back to the API in real time
5. Reports the exit code and final output when the job completes

## Execution modes

The runner auto-detects the environment:

- **Docker** (default on Linux/macOS) — pulls the image and runs a container via the Docker socket. Used when running directly on a host or inside Docker Compose.
- **Kubernetes** — creates a one-shot `Job` for each script, streams logs from the pod, and deletes the Job on completion. Activated automatically when `KUBERNETES_SERVICE_HOST` is set (i.e. inside any pod), or explicitly via `CONTAINER_RUNTIME=kubernetes`.
- **Local** — runs scripts directly on the host with layered security controls (approval gates, path allowlists, network restriction). Use `run-local` instead of `run`. Ideal for developer machines.

## Installation

### Download a pre-built binary

Pre-built binaries are published on the [releases page](https://github.com/testfiesta/workflowfiesta-runner/releases/latest):

| Platform | Variant | Filename |
|---|---|---|
| macOS Apple Silicon (arm64) | GUI | `workflowfiesta-runner-darwin-arm64-gui.app.zip` |
| macOS Apple Silicon (arm64) | Headless | `workflowfiesta-runner-darwin-arm64` |
| macOS Intel (amd64) | GUI | `workflowfiesta-runner-darwin-amd64-gui.app.zip` |
| macOS Intel (amd64) | Headless | `workflowfiesta-runner-darwin-amd64` |
| Linux x86-64 | GUI | `workflowfiesta-runner-linux-amd64-gui` |
| Linux x86-64 | Headless | `workflowfiesta-runner-linux-amd64` |
| Windows x86-64 | GUI | `workflowfiesta-runner-windows-amd64-gui.exe` |
| Windows x86-64 | Headless | `workflowfiesta-runner-windows-amd64.exe` |

> **GUI vs Headless:** GUI builds include a desktop wizard and system-tray icon (requires a display). Headless builds have no GUI dependencies and are ideal for servers and CI.

> **macOS GUI:** ships as a signed, notarized `WorkflowFiesta Runner.app`, zipped. Unzip and drag it into `/Applications` — it opens straight away, no Gatekeeper workaround needed.

```sh
# Linux amd64 example
curl -L https://github.com/testfiesta/workflowfiesta-runner/releases/latest/download/workflowfiesta-runner-linux-amd64 \
  -o /usr/local/bin/workflowfiesta-runner
chmod +x /usr/local/bin/workflowfiesta-runner
```

### Build from source

Requires Go 1.25+.

```sh
git clone https://github.com/testfiesta/workflowfiesta-runner
cd workflowfiesta-runner
make build
```

## Development

A `Makefile` is provided for all common development tasks:

```sh
make help                  # Show all available targets
make build                 # Build headless binary (default)
make build-gui             # Build GUI binary (requires CGO)
make test                  # Run all tests
make test-race             # Run tests with race detector
make test-cover            # Run tests with coverage report
make lint                  # Run golangci-lint
make check                 # fmt + vet + tests (pre-commit)
make docker                # Build Docker image
make build-all             # Cross-compile all headless variants
make clean                 # Remove build artifacts
```

Version is automatically embedded from git tags. Override with `make build VERSION=v1.2.3`.

## Registration

Before the runner can connect, register it with your WorkflowFiesta instance. Get a one-time registration code from the web UI: **Runners → "Set up a runner"**, name your runner, and copy the generated code (starts with `RNR-`).

```sh
workflowfiesta-runner register --code RNR-xxxxxxx-xxxxxxx-xxx
```

For self-hosted instances, add `--api-url`:

```sh
workflowfiesta-runner register \
  --code RNR-xxxxxxx-xxxxxxx-xxx \
  --api-url https://your-instance.workflowfiesta.com
```

This prints the runner UID, token, and export commands. Save them — the token is shown only once.

## Running

### On a host (Docker mode)

```sh
export WORKFLOWFIESTA_API_URL=https://app.workflowfiesta.com
export WORKFLOWFIESTA_TOKEN=<token from registration>
export WORKFLOWFIESTA_RUNNER_ID=<id from registration>
export WORKFLOWFIESTA_RUNNER_NAME=my-runner

workflowfiesta-runner run
```

### Local mode (no Docker)

Scripts execute directly on the host with approval gates and security controls.

```sh
# Headless (SSH / CI)
workflowfiesta-runner run-local --headless

# GUI mode (desktop — system tray + approval popups)
workflowfiesta-runner run-local
```

See [docs/local-mode.md](docs/local-mode.md) for full configuration.

### As a Docker container

Mount the Docker socket so the runner can start job containers.

```sh
docker run -d \
  --name workflowfiesta-runner \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e WORKFLOWFIESTA_API_URL=https://app.workflowfiesta.com \
  -e WORKFLOWFIESTA_TOKEN=<your-token> \
  -e WORKFLOWFIESTA_RUNNER_ID=<your-runner-id> \
  -e WORKFLOWFIESTA_RUNNER_NAME=my-runner \
  ghcr.io/testfiesta/workflowfiesta-runner:latest
```

Build the image locally:

```sh
docker build -t workflowfiesta-runner .
```

### On Kubernetes

Deploy the runner as a Deployment. No Docker socket mount is needed — the runner creates Kubernetes Jobs instead.

```yaml
# k8s/runner/deployment.yaml (included in the workflowfiesta repo)
env:
  - name: CONTAINER_RUNTIME
    value: kubernetes
  - name: KUBERNETES_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace
```

The runner pod needs a ServiceAccount with permission to manage `batch/jobs` and read `pods`/`pods/log`. Reference RBAC manifests are included in the WorkflowFiesta repo at `k8s/runner/`.

```sh
kubectl apply -f k8s/runner/serviceaccount.yaml
kubectl apply -f k8s/runner/rbac.yaml
kubectl apply -f k8s/runner/deployment.yaml
```

### Desktop GUI (double-click)

On macOS/Windows, double-click the GUI binary. If no token is saved, the first-run wizard opens automatically (paste code → configure permissions → done). On subsequent launches, the runner connects immediately and shows a system-tray icon.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `WORKFLOWFIESTA_TOKEN` | — | Runner authentication token (required) |
| `WORKFLOWFIESTA_API_URL` | `https://api.workflowfiesta.com` | Base URL of the WorkflowFiesta API |
| `WORKFLOWFIESTA_RUNNER_ID` | — | Runner ID obtained via `register` |
| `WORKFLOWFIESTA_RUNNER_NAME` | `unnamed-runner` | Human-readable name shown in the UI |
| `WORKFLOWFIESTA_LABELS` | — | Comma-separated labels (e.g. `linux,x86_64,gpu`) for job routing |
| `WORKFLOWFIESTA_MAX_CONCURRENT_JOBS` | `4` | Maximum simultaneous jobs |
| `CONTAINER_RUNTIME` | auto-detect | `docker`, `kubernetes`, or `local` |
| `DOCKER_SOCKET` | `/var/run/docker.sock` | Docker daemon socket path (Docker mode only) |
| `KUBERNETES_NAMESPACE` | `default` | Namespace in which to create Jobs (Kubernetes mode) |
| `KUBERNETES_IMAGE_PULL_SECRET` | — | `imagePullSecrets` name for private registries (Kubernetes mode) |
| `LOCAL_CONFIG_PATH` | `~/.workflowfiesta/runner.yaml` | Path to local executor config (local mode only) |

## Commands

```
workflowfiesta-runner run                         Start the runner (Docker/Kubernetes)
workflowfiesta-runner run-local [--headless]       Start in local executor mode
workflowfiesta-runner register --code <code>       Register using a one-time code
workflowfiesta-runner register-local               Register + configure local mode (GUI wizard)
workflowfiesta-runner init-local                   Configure local mode without re-registering (GUI wizard)
workflowfiesta-runner version                      Print version
```
