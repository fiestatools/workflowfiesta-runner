# CLI Commands

The binary is named `workflowfiesta-runner`. All sub-commands are registered with Cobra. If the binary is launched with no arguments and the GUI build is active, it enters the auto-launch path (double-click behavior). In a headless build with no arguments, it prints a usage message and waits for Enter.

---

## `run`

Start the runner and connect to WorkflowFiesta.

```
workflowfiesta-runner run
```

**Requirements:**
- `WORKFLOWFIESTA_TOKEN` must be set (or present in `~/.workflowfiesta/credentials.env`).

**Behavior:**
1. Loads config from environment variables and credentials file.
2. Determines executor type: `CONTAINER_RUNTIME` → Kubernetes auto-detect (`KUBERNETES_SERVICE_HOST`) → Windows default (`local`) → `docker`.
3. Creates a `runner.Runner` with a `cliSink` (ASCII output to stdout).
4. Connects to the API WebSocket with automatic retry.
5. Runs the job dispatch loop (see [api-client.md](./api-client.md) and [architecture.md](./architecture.md)).
6. Shuts down cleanly on SIGINT / SIGTERM.

**Notes:**
- `run` uses the Docker executor by default on Linux/macOS. To use the local executor from the CLI, use `run-local` instead.
- Concurrency is controlled by `WORKFLOWFIESTA_MAX_CONCURRENT_JOBS` (default 4).

---

## `run-local`

Run the runner in local executor mode. Scripts execute directly on the host (not in a container).

```
workflowfiesta-runner run-local [flags]
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--config` | `~/.workflowfiesta/runner.yaml` | Path to `runner.yaml` |
| `--headless` | `false` | Disable GUI; use terminal y/n approval prompts instead |

**Requirements:**
- `WORKFLOWFIESTA_TOKEN` must be set.
- A `runner.yaml` must exist (create with `init-local` or `register-local`). If it does not exist, defaults are used.

**Behavior:**
- Sets `ExecutorType = "local"` regardless of `CONTAINER_RUNTIME`.
- In GUI mode (default): opens the `StatusWindow`, connects to the system tray, and runs the runner in a background goroutine. The Fyne event loop blocks the main thread.
- In headless mode (`--headless`): behaves like `run` but uses the local executor with terminal approval prompts.
- The settings gear button in the status window opens `OpenSettingsWindow`, which writes changes back to `runner.yaml` and hot-swaps the in-memory `LocalConfig`.

---

## `register`

Register a new runner with WorkflowFiesta. Prints the runner ID and token to stdout.

```
workflowfiesta-runner register --api-url <url> --name <name> --org-id <org-id>
```

**Flags (all required):**

| Flag | Description |
|---|---|
| `--api-url` | Full URL of the WorkflowFiesta API, e.g. `https://app.example.com` |
| `--name` | Display name for this runner |
| `--org-id` | UUID of the organization to register under |

**Behavior:**
- POSTs to `<api-url>/api/runners/register` with `{ name, org_id }`.
- On success, prints the runner ID, token, and the `export` commands to paste into a shell.
- Does NOT write credentials to disk; the user must set environment variables manually.

---

## `register-local`

Register a new runner AND configure local executor settings via a GUI wizard. Writes credentials and `runner.yaml` automatically.

```
workflowfiesta-runner register-local [--config <path>]
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--config` | `~/.workflowfiesta/runner.yaml` | Where to write `runner.yaml` |

**Behavior:**
- Opens the 4-step Fyne wizard:
  1. **Connect & Register**: API URL, Org ID, Runner Name → calls registration API.
  2. **Token**: displays runner ID and token; offers a "Copy Token" button. Token is also saved automatically to `credentials.env`.
  3. **Local Permissions**: allowed paths, approval mode, timeouts, network mode.
  4. **Done**: shows the start command.
- Writes `~/.workflowfiesta/runner.yaml` (local config).
- Writes `~/.workflowfiesta/credentials.env` (shell export lines, mode 0600).
- On success, prints the commands to start the runner.

**Error handling:**
- Requires a display (`DISPLAY` or `WAYLAND_DISPLAY` on Linux; always available on macOS/Windows).
- Registration errors are shown inline in the wizard with friendly messages (connection refused, host not found, TLS error, duplicate name, wrong org ID, etc.).

---

## `init-local`

Interactive GUI wizard to create or update `runner.yaml` without registering a new runner. Use this to reconfigure an already-registered runner.

```
workflowfiesta-runner init-local [--config <path>]
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--config` | `~/.workflowfiesta/runner.yaml` | Path to write |

**Behavior:**
- Opens a 4-step wizard: Folder Access → Approval & Timeouts → Network Access → Review.
- Does not call the registration API; only writes `runner.yaml`.

---

## `version`

Print the build version string.

```
workflowfiesta-runner version
```

Output: `workflowfiesta-runner <version>` (e.g. `workflowfiesta-runner v1.2.0` or `workflowfiesta-runner dev`).

The `version` variable is set at link time via `-ldflags "-X main.version=<tag>"`. Development builds default to `dev`.

---

## Auto-launch (no sub-command, GUI build)

When the binary is started with no arguments and the GUI is available:

1. `config.Load()` is called. If a token is already present (env var or `credentials.env`), it loads `runner.yaml` and starts the runner immediately via `RunAutoLaunch`.
2. If no token is found, `showFirstRunWizard` is shown — the 3-step in-app wizard (Connect & Register → Local Permissions → Starting…).
3. After registration, the wizard transitions to a "starting" screen, opens the `StatusWindow` and system tray, and hides itself.

This path is the primary UX for end-users on macOS and Windows who install the GUI app.
