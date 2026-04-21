# API Client — HTTP Protocol

The runner communicates with the WorkflowFiesta API over plain HTTPS. There is no persistent socket: the runner polls for work on a short interval, sends heartbeats on a longer interval, and issues one-shot POSTs to report progress and results. The client lives in `internal/api/client.go`.

---

## Connection

**Base URL:** `WORKFLOWFIESTA_API_URL` (e.g. `https://app.workflowfiesta.com`).

**HTTP client:** single `*http.Client` with a **30-second timeout** per request.

**Authentication:** every request carries:
- `Authorization: Bearer <token>` — the runner's persistent token, issued at registration.
- `X-Org-Id: <orgId>` — added on every request after the first heartbeat response supplies it. Lets the backend skip a cross-tenant lookup and route the request directly to the tenant DB. Safe to call concurrently via `Client.SetOrgID`.

All request and response bodies are JSON.

**Auth failure:** `GET /api/runner/jobs/next` returning 401 is treated as fatal (`log.Fatal`) — the runner exits so an operator re-registers rather than spinning. Other 4xx/5xx responses return errors and the loop continues.

---

## Loops

Two goroutines drive all outbound traffic.

### Poll loop (every 3 s)

```
for every 3 s:
  GET /api/runner/jobs/next
    → 204  → no job, continue
    → 200  → Job JSON, dispatch to handleJob goroutine
  (dedup via activeJobs sync.Map, cap concurrent jobs via semaphore)
```

### Heartbeat loop (every 30 s)

```
for every 30 s:
  POST /api/runner/heartbeat
    body: { status, capabilities, os, arch, version }
    → 200 { ok, orgId }
```

`status` is `"busy"` if any job is running, `"idle"` otherwise. `capabilities` advertises the feature flags the backend uses to decide which job types to route (see [architecture.md](./architecture.md)).

There is no separate ping/pong, no reconnect logic, and no long-lived socket. If a single request fails the runner logs and retries on the next tick.

---

## Endpoints

All paths are relative to `WORKFLOWFIESTA_API_URL`. All authenticated with the Bearer token.

### Outgoing — runner lifecycle

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/runner/heartbeat` | Liveness + advertise capabilities/OS/arch/version. Response carries `orgId`. |
| `GET` | `/api/runner/jobs/next` | Claim the next pending job for this runner. `204` = nothing queued. |

### Outgoing — job lifecycle

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/runner/jobs/:id/output` | Stream a stdout/stderr chunk. Sent once per chunk received from the executor. |
| `POST` | `/api/runner/jobs/:id/complete` | Job finished cleanly (exit code may be non-zero). |
| `POST` | `/api/runner/jobs/:id/fail` | Executor itself errored (e.g. image pull, timeout). |
| `POST` | `/api/runner/jobs/:id/worktree` | Report the local git-worktree path for jobs that provisioned one. |
| `POST` | `/api/runner/jobs/:id/approval-pending` | Local executor is waiting on an operator's y/n. |
| `POST` | `/api/runner/jobs/:id/approval-resolved` | Operator approved or denied. |

### Outgoing — script library

| Method | Path | Purpose |
|---|---|---|
| `GET`  | `/api/runner/scripts` | List script metadata for the org. |
| `POST` | `/api/runner/scripts` | Upsert a script into the org library. |
| `GET`  | `/api/runner/scripts/:name` | Fetch the full content of a named script. |

---

## Job Payload

Returned from `GET /api/runner/jobs/next`:

```json
{
  "jobId": "job_abc123",
  "dockerImage": "ubuntu:22.04",
  "script": "echo hello world",
  "envVars": { "MY_VAR": "value" },
  "timeoutSeconds": 300,
  "toolName": "",
  "toolArgs": null,
  "git_repo_url": "",
  "git_ref": ""
}
```

| Field | Type | Description |
|---|---|---|
| `jobId` | string | Unique job identifier. |
| `dockerImage` | string | Container image (Docker/K8s) or context label (Local). |
| `script` | string | Shell script to execute. Empty when `toolName` is set. |
| `envVars` | object | Environment variables to pass to the script / tool. |
| `timeoutSeconds` | int | Job timeout; `0` defaults to 5 minutes on the runner side. |
| `toolName` | string | Optional structured tool invocation (e.g. `read_file`, `run_local_script`). When set, the runner executes natively instead of spawning a subprocess. |
| `toolArgs` | object | Arguments to the structured tool. |
| `git_repo_url`, `git_ref` | string | Optional — if set and the runner advertised `git_worktrees`, it provisions a worktree and runs the job inside it. |

---

## What the runner does NOT do

For anyone coming to this code with the old WebSocket-era assumptions:

- **No persistent socket.** No WebSocket, no Server-Sent Events, no long-polled connection.
- **No Redis client.** The backend has a `runnerChannel(runnerId)` Redis topic, but the runner does not subscribe to it. Any push-to-runner mechanism built on that channel needs a bridge (see [dispatch-wakeup.md](./dispatch-wakeup.md)).
- **No ping/pong.** Liveness is conveyed by the 30 s heartbeat POST; the backend considers a runner offline when its `lastSeen` is older than ~90 s.
- **No reconnect logic.** Each request is independent; the 30 s `http.Client` timeout bounds stuck requests.

---

## Client Interface Summary

```go
func New(apiURL, token string) *Client
func (c *Client) SetOrgID(orgID string)

// Lifecycle
func (c *Client) SendHeartbeat(status string, capabilities []string, goos, goarch, version string) (orgId string, err error)
func (c *Client) PollNextJob() (*Job, error)

// Job reporting
func (c *Client) StreamOutput(jobID, chunk string) error
func (c *Client) ReportJobComplete(jobID string, exitCode int, output string) error
func (c *Client) ReportJobFailed(jobID, errMsg string) error
func (c *Client) ReportWorktreePath(jobID, worktreePath string) error
func (c *Client) ReportApprovalPending(jobID, runnerName string) error
func (c *Client) ReportApprovalResolved(jobID string, approved bool) error

// Script library
func (c *Client) ListServerScripts() ([]ScriptMeta, error)
func (c *Client) PushScript(name, content, description string, tags []string) error
func (c *Client) GetScript(name string) (content string, err error)
```
