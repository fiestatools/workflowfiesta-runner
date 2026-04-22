# API Client — HTTP Polling Protocol

The runner communicates with the WorkflowFiesta API over HTTP REST endpoints using a poll-based model. The client lives in `internal/api/client.go`.

---

## Connection

**Base URL:** `<WORKFLOWFIESTA_API_URL>` (default: `https://app.workflowfiesta.com`)

**Authentication:**
- HTTP header: `Authorization: Bearer <token>` on every request.
- Optional header: `X-Org-Id: <orgId>` (set after first heartbeat response).

**HTTP client timeout:** 30 seconds per request.

---

## Polling Loop

The runner uses a simple poll loop (driven by `runner.Run()`):

1. **Heartbeat** — every 30 s, POST to `/api/runner/heartbeat`.
2. **Job poll** — every 3 s, GET `/api/runner/jobs/next`.
3. When a job is returned, the runner executes it and reports results via POST endpoints.

There is no persistent connection (no WebSocket). All communication is stateless HTTP request/response.

---

## Heartbeat

**Endpoint:** `POST /api/runner/heartbeat`

Sent every 30 seconds. Reports the runner's status, capabilities, and build info. The server uses this to update `last_seen_at` and mark the runner online.

**Request body:**
```json
{
  "status": "idle",
  "capabilities": ["docker", "git"],
  "os": "linux",
  "arch": "amd64",
  "version": "v1.2.0"
}
```

**Response body:**
```json
{
  "ok": true,
  "orgId": "org_abc123"
}
```

The returned `orgId` is stored and sent as `X-Org-Id` on all subsequent requests.

---

## Job Polling

**Endpoint:** `GET /api/runner/jobs/next`

Called every 3 seconds to claim the next pending job.

**Responses:**
- `204 No Content` — no pending job.
- `200 OK` — a job was claimed; body contains the job payload.
- `401 Unauthorized` — token invalid; runner calls `log.Fatal` immediately.

**Job payload:**
```json
{
  "jobId": "job_abc123",
  "dockerImage": "ubuntu:22.04",
  "script": "echo hello world",
  "envVars": {
    "MY_VAR": "value"
  },
  "timeoutSeconds": 300,
  "toolName": "run_script",
  "toolArgs": {},
  "git_repo_url": "",
  "git_ref": ""
}
```

| Field | Type | Description |
|---|---|---|
| `jobId` | string | Unique job identifier |
| `dockerImage` | string | Container image to use (Docker/Kubernetes) or execution context label (Local) |
| `script` | string | Shell script to execute |
| `envVars` | object | Environment variables to pass to the script |
| `timeoutSeconds` | int | Job timeout; 0 defaults to 5 minutes |
| `toolName` | string | Optional: tool that generated this job |
| `toolArgs` | object | Optional: arguments passed to the tool |
| `git_repo_url` | string | Optional: git repo to clone before execution |
| `git_ref` | string | Optional: git ref to checkout |

---

## Job Lifecycle Endpoints

### Stream output

**Endpoint:** `POST /api/runner/jobs/<jobId>/output`

Sent for each chunk of stdout/stderr output during execution.

```json
{ "chunk": "hello world\n" }
```

### Report completion

**Endpoint:** `POST /api/runner/jobs/<jobId>/complete`

Sent when the script finishes (even if exit code != 0).

```json
{
  "exitCode": 0,
  "output": "hello world\n"
}
```

### Report failure

**Endpoint:** `POST /api/runner/jobs/<jobId>/fail`

Sent when the executor itself fails (infrastructure error, image pull failure, timeout — not a non-zero exit code).

```json
{ "error": "timeout after 5m0s" }
```

### Report worktree path

**Endpoint:** `POST /api/runner/jobs/<jobId>/worktree`

Sent by the local executor to report the git worktree path on disk.

```json
{ "worktree_path": "/home/user/.workflowfiesta/worktrees/abc123" }
```

### Report approval pending

**Endpoint:** `POST /api/runner/jobs/<jobId>/approval-pending`

Sent by the local executor when a job requires human approval before execution.

```json
{ "runnerName": "my-laptop" }
```

### Report approval resolved

**Endpoint:** `POST /api/runner/jobs/<jobId>/approval-resolved`

Sent after the local operator approves or denies a job.

```json
{ "approved": true }
```

---

## Script Library Endpoints

### List scripts

**Endpoint:** `GET /api/runner/scripts`

Returns metadata for all scripts in the org's library.

### Get script

**Endpoint:** `GET /api/runner/scripts/<name>`

Returns `{ "content": "..." }` for a named script.

### Push script

**Endpoint:** `POST /api/runner/scripts`

Upserts a script to the org's library.

```json
{
  "name": "deploy.sh",
  "content": "#!/bin/bash\n...",
  "description": "Deploy to production",
  "tags": ["deploy", "prod"]
}
```

---

## Registration

**Endpoint:** `POST /api/runner/register`

Used by the `register` CLI command. Accepts a one-time code and returns credentials.

**Request:**
```json
{ "code": "RNR-xxxxxxx-xxxxxxx-xxx" }
```

**Response (201 Created):**
```json
{
  "uid": "abc123",
  "token": "wfr_xxxxxxxxxxxxxxxxxxxx",
  "orgUid": "org_abc",
  "name": "my-runner",
  "environmentUid": "env_abc"
}
```

---

## Thread Safety

- `c.orgID` is protected by `c.mu` mutex.
- All requests go through `c.do()` which acquires `c.mu` to read `orgID`.
- The HTTP client is safe for concurrent use.

---

## Client Interface Summary

```go
func New(apiURL, token string) *Client
func (c *Client) Connect(ctx context.Context) error
func (c *Client) ConnectWithRetry(ctx context.Context)
func (c *Client) Listen(ctx context.Context, jobChan chan<- Job)
func (c *Client) SendHeartbeat() error
func (c *Client) SendPing() error
func (c *Client) ReportJobClaimed(jobID string) error
func (c *Client) StreamOutput(jobID, chunk string) error
func (c *Client) ReportJobComplete(jobID string, exitCode int, output string) error
func (c *Client) ReportJobFailed(jobID, errMsg string) error
func (c *Client) ReportApprovalPending(jobID, runnerName string) error
func (c *Client) ReportApprovalResolved(jobID string, approved bool) error
func (c *Client) Close()
```
