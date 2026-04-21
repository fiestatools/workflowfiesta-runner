# workflowfiesta-runner — Architecture

## Overview

workflowfiesta-runner is a self-hosted job execution agent written in Go. It talks to a WorkflowFiesta API instance over plain HTTPS — heartbeat and job-poll requests on short tickers, one-shot POSTs to stream output and report completion. There is no persistent socket. Three executor back-ends are supported: Docker, Kubernetes, and Local (direct host execution).

The binary ships in two variants:
- **Headless build** (`nolocalui` tag): for servers and CI pipelines. No GUI dependency.
- **GUI build** (default): includes Fyne desktop UI for desktop/developer machines. Enables the approval dialog, status window, registration wizard, and system-tray icon.

---

## Package Layout

```
workflowfiesta-runner/
├── cmd/
│   └── runner/
│       └── main.go          Entry point. Cobra root command, sub-commands, cliSink
├── internal/
│   ├── api/
│   │   └── client.go        HTTP client: heartbeat, poll, report helpers
│   ├── config/
│   │   └── config.go        Config struct; env-var loading; credentials.env fallback
│   ├── executor/
│   │   ├── executor.go      Executor interface + factory (New / NewWithClient)
│   │   ├── docker.go        Docker executor
│   │   ├── kubernetes.go    Kubernetes Job executor
│   │   ├── local.go         Local host executor (sandboxing, approval gate, audit log)
│   │   ├── local_sandbox_linux.go   Landlock implementation (build: linux)
│   │   └── local_sandbox_darwin.go  Seatbelt/sandbox-exec implementation (build: darwin)
│   ├── localconfig/
│   │   └── config.go        runner.yaml schema, Load/Save, defaults
│   ├── localui/
│   │   ├── types.go         ApprovalResult, ApprovalRequest, ApprovalCallbacks
│   │   ├── approval.go      Fyne approval dialog (build: !nolocalui)
│   │   ├── headless.go      Terminal approval prompt (used when Headless=true)
│   │   ├── register.go      4-step registration + local-config wizard (RunRegisterWizard)
│   │   ├── settings.go      Settings window (OpenSettingsWindow)
│   │   ├── statuswindow.go  StatusWindow: always-visible status + log panel
│   │   ├── wizard.go        Local-config-only setup wizard (RunWizard / init-local)
│   │   └── launch.go        RunAutoLaunch: double-click entry point; first-run wizard
│   └── runner/
│       └── runner.go        Runner: job dispatch loop, concurrency semaphore, heartbeat
```

---

## Dependency Graph

```
cmd/runner/main.go
  ├── internal/config          ← env-var loading, credentials.env
  ├── internal/localconfig     ← runner.yaml schema
  ├── internal/localui         ← Fyne GUI (optional, gated by nolocalui tag)
  └── internal/runner
        ├── internal/api        ← HTTP client (heartbeat + poll + report)
        └── internal/executor
              ├── internal/localconfig
              └── internal/localui  ← approval gate for local executor
```

Key constraint: `internal/localui` must not import `internal/runner` to avoid a cycle (local executor calls localui for approval). The runner calls localui only through the `ApprovalReporter` interface defined in `executor/local.go`.

---

## Core Data Flow

```
WorkflowFiesta API
       │  every 3 s:  GET /api/runner/jobs/next
       │  every 30 s: POST /api/runner/heartbeat
       ▼
  runner.Run() poll loop
       │  PollNextJob() → Job (or nil)
       │  dedup via activeJobs sync.Map
       │  concurrency via semaphore (default 4)
       ▼
  runner.handleJob()
       │  1. (tool job) handleToolJob  OR  (script job) executor.Execute()
       │  2. StreamOutput chunks → POST /api/runner/jobs/:id/output (concurrent)
       │  3. ReportJobComplete   → POST /api/runner/jobs/:id/complete
       │     ReportJobFailed     → POST /api/runner/jobs/:id/fail
       ▼
  Executor (docker | kubernetes | local)
       │  stdout+stderr → outputChan<string>
       │  returns (exitCode int, err error)
       ▼
  StatusSink(s)  (cliSink or StatusWindow)
       └─ SetJobRunning / AppendLog / SetJobComplete / SetIdle
```

---

## Build Tags

| Tag | Effect |
|---|---|
| *(none)* | Full GUI build: Fyne dependency included |
| `nolocalui` | Headless build: all Fyne symbols replaced by stubs; approval falls back to terminal prompt |
| `linux` | Enables `local_sandbox_linux.go` (Landlock) |
| `darwin` | Enables `local_sandbox_darwin.go` (Seatbelt) |

The `nolocalui` build produces a smaller binary suitable for Docker containers and servers.

---

## Concurrency Model

- One goroutine per active job (goroutine-per-request pattern).
- A buffered channel (`semaphore`) caps concurrent jobs at `MaxConcurrentJobs` (default 4, overridable via `WORKFLOWFIESTA_MAX_CONCURRENT_JOBS`).
- `sync.Map` (`activeJobs`) prevents duplicate execution if the same `jobId` is returned by two polls (e.g. retry after a flaky network).
- Two goroutines drive all outbound traffic: the 3 s job-poll loop in `Run()` and the 30 s heartbeat loop in `heartbeatLoop()`. `api.Client.SetOrgID` is the only shared-state write; it's guarded by the client's mutex.
- Liveness is conveyed exclusively by the 30 s heartbeat POST. The backend considers a runner offline when `lastSeen` is older than ~90 s. There is no ping/pong.

---

## GUI Architecture (GUI build only)

Fyne requires the event loop on the main OS thread (mandatory on macOS). The pattern used:

1. `localui.RunAutoLaunch` or `localui.StartTray` calls `a.Run()` on the main thread, which blocks until `localui.QuitApp()`.
2. The runner itself runs in a `go func()` goroutine.
3. All Fyne widget mutations go through `fyne.Do(func() { ... })` to marshal back to the main event loop.
4. The `StatusWindow` implements `runner.StatusSink` so the runner goroutine can update UI state in a thread-safe way.

---

## External Dependencies

| Package | Purpose |
|---|---|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/sirupsen/logrus` | Structured logging |
| `net/http` (stdlib) | HTTP client for heartbeat / poll / job reporting |
| `github.com/docker/docker` | Docker Engine API |
| `k8s.io/client-go` | Kubernetes API |
| `fyne.io/fyne/v2` | Cross-platform desktop GUI (GUI build only) |
| `golang.org/x/sys/unix` | Landlock syscall wrappers (Linux) |
| `gopkg.in/yaml.v3` | runner.yaml parsing |
