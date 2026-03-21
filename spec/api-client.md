# API Client — WebSocket Protocol

The runner communicates with the WorkflowFiesta API exclusively over a single persistent WebSocket connection. The client lives in `internal/api/client.go`.

---

## Connection

**Endpoint:** `<WORKFLOWFIESTA_API_URL>/runner-ws`
`http://` and `https://` are rewritten to `ws://` and `wss://` automatically.

**Authentication:**
- HTTP header: `Authorization: Bearer <token>`
- Query parameter: `?token=<token>` (fallback for environments that strip headers)

**Close codes:**
- `4001` or `4003`: authentication failure. The runner calls `log.Fatalf` immediately — no reconnect.

---

## Reconnection

`ConnectWithRetry` retries indefinitely with linear back-off:

```
delay = min(attempt * 2, 30) seconds
```

- Attempt 1: 2 s
- Attempt 2: 4 s
- Attempt 5+: 30 s (cap)

Reconnects are also triggered automatically by `Listen()` whenever `ReadMessage()` returns an error (including deadline exceeded from a missed Pong).

---

## Ping / Pong Keep-Alive

The runner uses two keep-alive mechanisms simultaneously:

1. **Application heartbeat** — every 30 s, sends `{ "type": "heartbeat" }` as a JSON text frame. The server uses this to update the runner's `last_seen_at` timestamp and mark it online.

2. **WebSocket PING frame** — every 30 s, sends a WebSocket control PING. The ws npm library on the server side auto-responds with a PONG. On PONG receipt, the read deadline is extended by 75 s.

**Dead connection detection:** if no PONG arrives within 75 s of a PING, the read deadline fires and `ReadMessage()` returns an error. `Listen()` clears `c.conn` and calls `ConnectWithRetry`.

---

## Message Types

All messages are JSON text frames.

### Incoming (API → Runner)

#### `job`

Dispatches a job to the runner.

```json
{
  "type": "job",
  "jobId": "job_abc123",
  "dockerImage": "ubuntu:22.04",
  "script": "echo hello world",
  "envVars": {
    "MY_VAR": "value"
  },
  "timeoutSeconds": 300
}
```

| Field | Type | Description |
|---|---|---|
| `jobId` | string | Unique job identifier |
| `dockerImage` | string | Container image to use (Docker/Kubernetes) or execution context label (Local) |
| `script` | string | Shell script to execute |
| `envVars` | object | Environment variables to pass to the script |
| `timeoutSeconds` | int | Job timeout; 0 defaults to 5 minutes |

### Outgoing (Runner → API)

#### `heartbeat`

Sent every 30 s to indicate the runner is alive.

```json
{ "type": "heartbeat" }
```

#### `job:claimed`

Sent immediately after the runner dequeues a job, before execution begins. Prevents the server from re-dispatching.

```json
{ "type": "job:claimed", "jobId": "job_abc123" }
```

#### `job:output`

Streams a chunk of stdout/stderr output. Sent for each chunk received from the executor's output channel.

```json
{ "type": "job:output", "jobId": "job_abc123", "chunk": "hello world\n" }
```

#### `job:complete`

Sent when the script finishes successfully (even if exit code != 0).

```json
{
  "type": "job:complete",
  "jobId": "job_abc123",
  "exitCode": 0,
  "output": "hello world\n"
}
```

#### `job:failed`

Sent when the executor itself fails (not a non-zero exit code, but an infrastructure error such as image pull failure or timeout).

```json
{ "type": "job:failed", "jobId": "job_abc123", "error": "timeout after 5m0s" }
```

#### `job:approval_pending`

Sent by the local executor when a job requires human approval before execution. The API uses this to update the job's status in the database.

```json
{ "type": "job:approval_pending", "jobId": "job_abc123", "runnerName": "my-laptop" }
```

#### `job:approval_resolved`

Sent after the local operator approves or denies a job.

```json
{ "type": "job:approval_resolved", "jobId": "job_abc123", "approved": true }
```

---

## Thread Safety

- `c.conn` is protected by `c.mu` mutex.
- All `send()` calls acquire `c.mu`.
- `Listen()` reads without the mutex (Gorilla WebSocket: one reader, one writer goroutine is safe).
- On a write error, `send()` nils `c.conn` so `Listen()` detects the failure on the next `ReadMessage()` deadline expiry and reconnects.

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
