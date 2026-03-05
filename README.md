# WorkflowFiesta Runner

A self-hosted runner for [WorkflowFiesta](https://github.com/ss-libs/workflowfiesta) that connects via WebSocket and executes jobs in isolated containers on your own infrastructure. Supports both Docker and Kubernetes.

## What it does

The runner:
1. Connects to your WorkflowFiesta API via WebSocket
2. Listens for incoming job assignments
3. Runs the job script inside an isolated container (Docker or Kubernetes)
4. Streams output back to the API in real time
5. Reports the exit code and final output when the job completes

## Container runtimes

The runner auto-detects the environment:

- **Docker** (default) — pulls the image and runs a container via the Docker socket. Used when running directly on a host or inside Docker Compose.
- **Kubernetes** — creates a one-shot `Job` for each script, streams logs from the pod, and deletes the Job on completion. Activated automatically when `KUBERNETES_SERVICE_HOST` is set (i.e. inside any pod), or explicitly via `CONTAINER_RUNTIME=kubernetes`.

## Installation

### Download a pre-built binary

Download the latest release binary for your platform from the [releases page](https://github.com/ss-libs/workflowfiesta-runner/releases).

```sh
# Linux amd64 example
curl -L https://github.com/ss-libs/workflowfiesta-runner/releases/latest/download/workflowfiesta-runner-linux-amd64 \
  -o /usr/local/bin/workflowfiesta-runner
chmod +x /usr/local/bin/workflowfiesta-runner
```

### Build from source

Requires Go 1.24+.

```sh
git clone https://github.com/ss-libs/workflowfiesta-runner
cd workflowfiesta-runner
go build -o workflowfiesta-runner ./cmd/runner
```

## Registration

Before the runner can connect, register it with your WorkflowFiesta instance to obtain a token.

```sh
workflowfiesta-runner register \
  --api-url http://your-workflowfiesta:3001 \
  --name my-runner \
  --org-id your-org-id
```

This prints the runner ID and token. Save them — the token is shown only once.

## Running

### On a host (Docker mode)

```sh
export WORKFLOWFIESTA_API_URL=http://your-workflowfiesta:3001
export WORKFLOWFIESTA_TOKEN=<token from registration>
export WORKFLOWFIESTA_RUNNER_ID=<id from registration>
export WORKFLOWFIESTA_RUNNER_NAME=my-runner

workflowfiesta-runner run
```

### As a Docker container

Mount the Docker socket so the runner can start job containers.

```sh
docker run -d \
  --name workflowfiesta-runner \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e WORKFLOWFIESTA_API_URL=http://your-workflowfiesta:3001 \
  -e WORKFLOWFIESTA_TOKEN=<your-token> \
  -e WORKFLOWFIESTA_RUNNER_ID=<your-runner-id> \
  -e WORKFLOWFIESTA_RUNNER_NAME=my-runner \
  ghcr.io/ss-libs/workflowfiesta-runner:latest
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

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `WORKFLOWFIESTA_TOKEN` | — | Runner authentication token (required) |
| `WORKFLOWFIESTA_API_URL` | `http://localhost:3001` | Base URL of the WorkflowFiesta API |
| `WORKFLOWFIESTA_RUNNER_ID` | — | Runner ID obtained via `register` |
| `WORKFLOWFIESTA_RUNNER_NAME` | `unnamed-runner` | Human-readable name shown in the UI |
| `WORKFLOWFIESTA_LABELS` | — | Comma-separated labels (e.g. `linux,x86_64,gpu`) for job routing |
| `CONTAINER_RUNTIME` | auto-detect | `docker` or `kubernetes` |
| `DOCKER_SOCKET` | `/var/run/docker.sock` | Docker daemon socket path (Docker mode only) |
| `KUBERNETES_NAMESPACE` | `default` | Namespace in which to create Jobs (Kubernetes mode) |
| `KUBERNETES_IMAGE_PULL_SECRET` | — | `imagePullSecrets` name for private registries (Kubernetes mode) |

## Commands

```
workflowfiesta-runner run                      Start the runner
workflowfiesta-runner register --api-url ...  Register a new runner
workflowfiesta-runner version                  Print version
```
