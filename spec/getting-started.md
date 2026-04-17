# Getting Started

This guide covers downloading the runner binary, registering with a WorkflowFiesta instance, and starting the runner in each supported mode.

---

## Prerequisites

- A running WorkflowFiesta instance (self-hosted or cloud). Note the API URL (e.g. `http://localhost:3001` or `https://your-instance.workflowfiesta.com`).
- Your organization ID (visible in the WorkflowFiesta web UI under Settings → Organization).
- For Docker mode: Docker Engine installed and the Docker socket accessible.
- For Kubernetes mode: a kubeconfig with permission to create Jobs in the target namespace, or the runner running inside the cluster.
- For Local mode: nothing extra; scripts run directly on the host.

---

## Download

Pre-built binaries are published on the GitHub Releases page. Download the binary for your platform:

| Platform | Filename |
|---|---|
| Linux x86-64 (headless) | `workflowfiesta-runner-linux-amd64` |
| macOS arm64 (GUI) | `workflowfiesta-runner-darwin-arm64` |
| Windows x86-64 (GUI) | `workflowfiesta-runner-windows-amd64-gui.exe` |
| Windows x86-64 (headless) | `workflowfiesta-runner-windows-amd64.exe` |

Make the binary executable on Linux/macOS:

```bash
chmod +x workflowfiesta-runner-linux-amd64
mv workflowfiesta-runner-linux-amd64 /usr/local/bin/workflowfiesta-runner
```

---

## Option A: Desktop (GUI) Setup — recommended for developer machines

Double-click the binary (macOS or Windows GUI build) or run it without arguments. The first-run wizard opens automatically.

**Step 1 — Connect & Register**
- Enter your API URL, Organization ID, and a name for this runner (defaults to your hostname).
- Click "Connect & Register". The runner calls `POST /api/runner/register` and saves credentials to `~/.workflowfiesta/credentials.env`.

**Step 2 — Local Permissions**
- Choose an approval mode: prompt for every job, prompt for risky operations (default), or never prompt.
- Choose network access for scripts: allow all (default), localhost only, or block all.

**Step 3 — Starting**
The wizard transitions to "Runner is starting…", opens the status window, and sets up the system tray icon. The runner is now connected.

To reopen the status window or quit: right-click the system tray / menu bar icon.

---

## Option B: Command-Line Registration (Docker/Kubernetes/server)

### 1. Register the runner

```bash
workflowfiesta-runner register \
  --api-url https://your-instance.workflowfiesta.com \
  --name my-docker-runner \
  --org-id your-org-id
```

Output:
```
Runner registered successfully!
Runner ID: abc123
Token: wfr_xxxxxxxxxxxx

Set environment variables:
  export WORKFLOWFIESTA_API_URL=https://your-instance.workflowfiesta.com
  export WORKFLOWFIESTA_TOKEN=wfr_xxxxxxxxxxxx
  export WORKFLOWFIESTA_RUNNER_ID=abc123
  export WORKFLOWFIESTA_RUNNER_NAME=my-docker-runner

Then run: workflowfiesta-runner run
```

### 2. Set environment variables

```bash
export WORKFLOWFIESTA_API_URL=https://your-instance.workflowfiesta.com
export WORKFLOWFIESTA_TOKEN=wfr_xxxxxxxxxxxx
export WORKFLOWFIESTA_RUNNER_ID=abc123
export WORKFLOWFIESTA_RUNNER_NAME=my-docker-runner
```

Or place them in a `.env` file and `source` it.

### 3. Start the runner

**Docker mode** (default on Linux/macOS outside Kubernetes):
```bash
workflowfiesta-runner run
```

**Kubernetes mode** (auto-detected inside a cluster; override with `CONTAINER_RUNTIME=kubernetes`):
```bash
CONTAINER_RUNTIME=kubernetes workflowfiesta-runner run
```

**Local mode** (scripts execute directly on the host):
```bash
workflowfiesta-runner run-local --headless
```

---

## Option C: Local Runner GUI Registration

Use `register-local` to combine registration and local-config setup in one GUI wizard:

```bash
workflowfiesta-runner register-local
```

This opens the 4-step Fyne wizard (Connect & Register → Token display → Local Permissions → Done), writes `runner.yaml` and `credentials.env` to `~/.workflowfiesta/`, then prints the start command:

```
Credentials saved to ~/.workflowfiesta/credentials.env
Start the runner with:

  source ~/.workflowfiesta/credentials.env
  workflowfiesta-runner run-local
```

---

## Running as a System Service

### systemd (Linux)

```ini
[Unit]
Description=WorkflowFiesta Runner
After=docker.service

[Service]
EnvironmentFile=/etc/workflowfiesta/runner.env
ExecStart=/usr/local/bin/workflowfiesta-runner run
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now workflowfiesta-runner
```

### Docker container

```dockerfile
FROM debian:12-slim
COPY workflowfiesta-runner /usr/local/bin/workflowfiesta-runner
CMD ["workflowfiesta-runner", "run"]
```

```bash
docker run -d \
  -e WORKFLOWFIESTA_API_URL=https://your-instance.workflowfiesta.com \
  -e WORKFLOWFIESTA_TOKEN=wfr_xxx \
  -e WORKFLOWFIESTA_RUNNER_NAME=docker-runner \
  -v /var/run/docker.sock:/var/run/docker.sock \
  my-runner-image
```

---

## Verifying the Connection

A connected runner shows:
- CLI: a green `●  Connected  <name> → <api-url>` banner.
- GUI: the status window badge changes from "Offline" (red) to "Idle" (green).
- Web UI (Settings → Runners): the runner appears with status "idle".
