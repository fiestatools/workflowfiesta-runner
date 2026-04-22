# Local Runtime Mode

Run workflow scripts directly on your machine — no Docker or Kubernetes required.

---

## Overview

The `local` executor runs scripts in a subprocess on the host, using layered security controls:

| Layer | Default | Opt-in |
|---|---|---|
| Blocked pattern check | Minimal safe list | Full blocklist in config |
| Confirmation gate | Prompt for destructive ops | `confirm: always` or `never` |
| Audit log | Always on | — |
| Resource limits | `ulimit -t 120 -v 1GB` via shell | Lower in config |
| Minimal environment | `PATH`, `HOME`, `TERM`, `LANG` only | — |
| Path allowlist (kernel) | Off (`sandbox: none`) | `sandbox: kernel` |
| Network restriction | Off (`network: all`) | `network: none` / `localhost` |
| Auto-deny timeout | 60 s | `confirm_timeout` in config |

---

## Quick start

### 1. Register the runner

In the WorkflowFiesta web UI, go to **Runners → "Set up a runner"**, name your runner, and copy the generated code (starts with `RNR-`).

```sh
workflowfiesta-runner register --code RNR-xxxxxxx-xxxxxxx-xxx
```

For self-hosted instances, add `--api-url`:

```sh
workflowfiesta-runner register \
  --code RNR-xxxxxxx-xxxxxxx-xxx \
  --api-url https://your-instance.workflowfiesta.com
```

Copy the environment variables from the output and export them:

```sh
export WORKFLOWFIESTA_API_URL=https://app.workflowfiesta.com
export WORKFLOWFIESTA_TOKEN=<token from output>
export WORKFLOWFIESTA_RUNNER_ID=<runner-id from output>
export WORKFLOWFIESTA_RUNNER_NAME=my-laptop
```

### 2. Configure (GUI wizard)

```sh
workflowfiesta-runner init-local
```

The wizard walks through:
1. **Allowed folders** — which directories the agent can read and write
2. **Approval mode** — always prompt / destructive ops only / never
3. **Network** — allow all / local only / block all
4. Saves `~/.workflowfiesta/runner.yaml`

Or skip the wizard and use the defaults immediately (broad home-dir access, prompt on destructive ops):

```sh
# No init-local needed — defaults are written on first run
workflowfiesta-runner run-local
```

### 3. Run

```sh
# GUI mode: system tray icon + approval popup (default)
workflowfiesta-runner run-local

# Headless mode: terminal y/n prompt — for SSH or CI
workflowfiesta-runner run-local --headless

# Override config file location
workflowfiesta-runner run-local --config /path/to/runner.yaml
```

---

## Config file (`~/.workflowfiesta/runner.yaml`)

### Default (non-technical user)

```yaml
allowed_paths:
  - ~/             # full home directory

confirm: destructive   # prompt before rm, mv, truncate, output redirects
network: all           # scripts can reach the internet
max_timeout: 120       # 2 minutes per job
confirm_timeout: 60    # 60 s to respond before auto-deny

blocked_patterns:
  - 'rm\s+-rf\s+/'       # deleting root
  - 'rm\s+-rf\s+~'       # deleting home
  - 'dd\s.*of=/dev/[a-z]'
  - 'mkfs\.'
  - ':.*>.*\s*/dev/[a-z]'

sandbox: none           # no kernel-level isolation (broadest compatibility)
audit_log: ~/.workflowfiesta/audit.log
```

### Locked-down (power user)

```yaml
allowed_paths:
  - ~/projects/my-project    # read-write: only this project
  - ~/Documents:ro           # read-only: can read but not modify Documents

confirm: always              # prompt for every single job
network: none                # block all outbound network
max_timeout: 30
confirm_timeout: 15

blocked_patterns:
  - 'rm\s+-rf\s+/'
  - 'rm\s+-rf\s+~'
  - 'dd\s.*of=/dev/[a-z]'
  - 'mkfs\.'
  - 'sudo'
  - 'curl\s.*\|.*sh'
  - 'wget\s.*\|.*sh'
  - 'chmod\s.*777'
  - 'eval\s'

sandbox: kernel              # Landlock (Linux ≥5.13) or sandbox-exec (macOS)
audit_log: ~/secure-runner-audit.log
```

### Config reference

| Key | Type | Default | Description |
|---|---|---|---|
| `allowed_paths` | list | `["~/"]` | Directories accessible to scripts. Append `:ro` for read-only. |
| `confirm` | string | `"destructive"` | `"always"`, `"destructive"`, or `"never"` |
| `network` | string | `"all"` | `"all"`, `"localhost"`, or `"none"` |
| `max_timeout` | int (s) | `120` | Maximum script runtime. Jobs exceeding this are killed. |
| `confirm_timeout` | int (s) | `60` | Seconds before an unanswered approval dialog auto-denies. |
| `blocked_patterns` | list | (see above) | Regex patterns. Any match blocks execution before the approval prompt. |
| `sandbox` | string | `"none"` | `"none"` or `"kernel"` (see below). |
| `audit_log` | string | `~/.workflowfiesta/audit.log` | Path to the JSON audit log. |

---

## Approval dialog

When a job requires approval, a small window appears at the bottom-right of your screen:

```
┌──────────────────────────────────────────────────────┐
│  WorkflowFiesta  ·  Job Request              [×]    │
├──────────────────────────────────────────────────────┤
│  find ~/Documents -name "*.pdf" | head -20           │
│                                                      │
│  From: my-laptop                                     │
│                                                      │
│  Auto-deny in 28s ░░░░░░░░░░░░░░░░░░░░░░░           │
│                                                      │
│               [  Deny  ]   [  Allow  ]              │
└──────────────────────────────────────────────────────┘
```

In headless mode (`--headless`), the same prompt appears in the terminal:

```
╔══════════════════════════════════════════╗
║   WorkflowFiesta · Job Request           ║
╚══════════════════════════════════════════╝
Runner : my-laptop
Job ID : abc123
Script :
find ~/Documents -name "*.pdf" | head -20

Allow? [y/N] (auto-deny in 60s):
```

---

## Kernel-level sandboxing (`sandbox: kernel`)

Opt-in. Enforces `allowed_paths` at the OS level.

| Platform | Mechanism | Requirement |
|---|---|---|
| Linux | Landlock | Kernel ≥ 5.13 |
| macOS | `sandbox-exec` (Seatbelt) | macOS 10.15+ |
| Windows / other | Warning + no-op | — |

If the kernel is too old or `sandbox-exec` is missing, the runner logs a warning and continues without OS-level isolation. Process-level controls (blocked patterns, confirmation, resource limits) still apply.

**Network restriction** (`network: none`) on Linux uses `unshare -n` to create a private network namespace (requires user namespaces, enabled by default on most distros). On macOS, a `(deny network*)` rule is added to the Seatbelt profile.

---

## Audit log

One JSON object per line at `~/.workflowfiesta/audit.log`:

```json
{"time":"2026-03-07T14:23:01Z","job_id":"abc123","script":"find ~/Documents...","decision":"approved","exit_code":0,"duration_ms":1842}
{"time":"2026-03-07T14:25:10Z","job_id":"def456","script":"rm -rf /","decision":"blocked","reason":"blocked_pattern:rm\\s+-rf\\s+/"}
{"time":"2026-03-07T14:26:00Z","job_id":"ghi789","script":"ls ~/projects","decision":"denied","reason":"user_denied"}
```

---

## Build

### Full binary (includes GUI — for desktop use)

Requires gcc and display libraries:

```sh
# macOS
xcode-select --install  # if not already installed
go get fyne.io/fyne/v2@v2.5.1
go mod tidy
go build -o workflowfiesta-runner ./cmd/runner

# Linux (Debian/Ubuntu)
sudo apt-get install gcc libgl1-mesa-dev xorg-dev
go get fyne.io/fyne/v2@v2.5.1
go mod tidy
go build -o workflowfiesta-runner ./cmd/runner
```

### Server binary (no GUI — for CI/server deployments)

No CGO required; builds without Fyne:

```sh
go build -tags nolocalui -o workflowfiesta-runner-server ./cmd/runner
```

In `nolocalui` builds, `run-local --headless` still works with terminal prompts; the `init-local` wizard returns an error (create `~/.workflowfiesta/runner.yaml` manually instead).

---

## Verification checklist

1. **`init-local`** — run the wizard, pick a test folder, confirm `~/.workflowfiesta/runner.yaml` is written.
2. **Approval flow** — trigger a job with `rm` in the script; confirm the dialog/prompt appears, Allow/Deny work, audit log is written.
3. **Blocked pattern** — trigger a job containing `rm -rf /`; confirm it is rejected before the approval dialog with a `blocked_pattern` error in the audit log.
4. **Auto-deny** — let the confirmation dialog expire; confirm `"decision":"denied","reason":"user_denied"` in the audit log.
5. **Headless mode** — `run-local --headless`, trigger a job; confirm terminal `y/N` prompt appears.
6. **Path enforcement** (`sandbox: kernel`) — trigger a job that reads outside `allowed_paths`; confirm it fails at the OS level.

---

## Mobile (future)

Fyne supports iOS and Android from the same codebase. The local executor degrades gracefully on platforms without kernel sandboxing — the mobile OS enforces file access via its own permission system. Build with:

```sh
fyne package -os ios
fyne package -os android
```
