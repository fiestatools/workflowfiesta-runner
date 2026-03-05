# WorkflowFiesta Runner

A self-hosted runner for [WorkflowFiesta](https://github.com/your-org/workflowfiesta) that connects via WebSocket and executes jobs in Docker containers on your own infrastructure.

## What it does

The runner:
1. Connects to your WorkflowFiesta API via WebSocket
2. Listens for incoming job assignments
3. Pulls the specified Docker image and runs the job script inside a container
4. Streams output back to the API in real time
5. Reports the exit code and final output when the job completes

## Installation

### Download a pre-built binary

Download the latest release binary for your platform from the [releases page](https://github.com/your-org/workflowfiesta-runner/releases).

```sh
# Linux amd64 example
curl -L https://github.com/your-org/workflowfiesta-runner/releases/latest/download/workflowfiesta-runner-linux-amd64 \
  -o /usr/local/bin/workflowfiesta-runner
chmod +x /usr/local/bin/workflowfiesta-runner
```

### Build from source

Requires Go 1.23+ and Docker.

```sh
git clone https://github.com/your-org/workflowfiesta-runner
cd workflowfiesta-runner
go build -o workflowfiesta-runner ./cmd/runner
```

## Registration

Before the runner can connect, it must be registered with your WorkflowFiesta instance to obtain a token.

```sh
workflowfiesta-runner register \
  --api-url http://your-workflowfiesta:3001 \
  --name my-runner \
  --org-id your-org-id
```

This prints the runner ID and token. Save them — the token is shown only once.

## Running

Set the required environment variables and start the runner:

```sh
export WORKFLOWFIESTA_API_URL=http://your-workflowfiesta:3001
export WORKFLOWFIESTA_TOKEN=<token from registration>
export WORKFLOWFIESTA_RUNNER_ID=<id from registration>
export WORKFLOWFIESTA_RUNNER_NAME=my-runner

workflowfiesta-runner run
```

The runner will connect to the API, then wait for jobs. It automatically reconnects if the connection drops.

## Docker usage

Run the runner as a Docker container. The Docker socket must be mounted so the runner can start job containers.

```sh
docker run -d \
  --name workflowfiesta-runner \
  --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e WORKFLOWFIESTA_API_URL=http://your-workflowfiesta:3001 \
  -e WORKFLOWFIESTA_TOKEN=<your-token> \
  -e WORKFLOWFIESTA_RUNNER_ID=<your-runner-id> \
  -e WORKFLOWFIESTA_RUNNER_NAME=my-runner \
  ghcr.io/your-org/workflowfiesta-runner:latest
```

Build the image locally:

```sh
docker build -t workflowfiesta-runner .
```

## Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `WORKFLOWFIESTA_TOKEN` | Yes | — | Runner authentication token (obtained via `register`) |
| `WORKFLOWFIESTA_API_URL` | No | `http://localhost:3001` | Base URL of the WorkflowFiesta API |
| `WORKFLOWFIESTA_RUNNER_ID` | No | — | Runner ID (obtained via `register`) |
| `WORKFLOWFIESTA_RUNNER_NAME` | No | `unnamed-runner` | Human-readable runner name shown in the UI |
| `WORKFLOWFIESTA_LABELS` | No | — | Comma-separated labels (e.g. `linux,x86_64,gpu`) for job routing |
| `DOCKER_SOCKET` | No | `/var/run/docker.sock` | Path to the Docker daemon socket |

## Commands

```
workflowfiesta-runner run                       Start the runner
workflowfiesta-runner register --api-url ...   Register a new runner
workflowfiesta-runner version                   Print version
```
