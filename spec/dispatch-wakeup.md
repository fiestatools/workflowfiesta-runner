# Dispatch Wake-Up — Current State

## Problem being solved

Without a push channel, a newly queued job is only picked up on the runner's next `GET /api/runner/jobs/next` tick (3 s cadence). That bounds tool-dispatch latency at roughly one poll interval per hop. For short interactive tools (file reads, greps) that latency dominates the user-visible response time.

The long-term goal is to let the backend notify a specific runner the instant it has work for it, so the next poll becomes a fast-path rather than the discovery mechanism.

## What exists today

The backend defines a per-runner Redis channel and publishes a wake-up message when it queues a job for a self-hosted runner:

- Channel name: `wf:runner:<runnerId>` — `runnerChannel(runnerId)` in `backend/src/services/redis-pubsub.ts`.
- Endpoint: `POST /internal/runner-dispatch` with body `{ runnerId, jobId }` and optional `X-Org-Id` for ownership validation. It publishes `{ type: "job_available", jobId }` on the channel above.
- Worker: the Temporal worker POSTs to that endpoint immediately after inserting a `runnerJobs` row, so a fresh job emits a wake-up signal within milliseconds.

## Why it is currently a no-op

**The runner does not consume the signal.** The Go runner speaks HTTP only — see [api-client.md](./api-client.md). It has no Redis client and no persistent socket to the backend. Nothing in `internal/api/client.go` or `internal/runner/runner.go` subscribes to `wf:runner:*`.

Result: `POST /internal/runner-dispatch` publishes to a channel that has no subscribers, and the runner still learns about the job on its next 3 s poll tick — the same cadence it would have without the wake-up plumbing.

Evidence (current `main` of `workflowfiesta-mono`):

- `runnerChannel` is only defined, never subscribed: `rg 'runnerChannel|\.subscribe\(|\.psubscribe\(' backend/src` shows a lone definition in `redis-pubsub.ts`.
- `go.mod` in this repo does not depend on any Redis client or WebSocket library — verify with `grep -E 'redis|websocket' go.mod`.
- Real network effects from the poll loop alone: the runner's 3 s `pollTicker` (`internal/runner/runner.go`) and 30 s heartbeat remain the only runner-to-backend traffic.

## Closing the gap

Two plausible shapes — pick one before wiring more code to `runnerChannel`:

1. **Redis subscriber in the runner.** Add a Redis connection to `internal/api` (or a new `internal/dispatch` package) that `PSUBSCRIBE`s to `wf:runner:<runnerId>`. On receipt of `job_available`, wake the poll loop early (e.g. a condition variable that the ticker also signals). Pros: one fewer hop, no new backend component. Cons: every runner now needs Redis credentials and network reachability to the backend's Redis — self-hosted runners behind NAT/corporate firewalls may not have it.

2. **Backend bridge to a runner-facing WebSocket.** Keep the runner HTTP-only for request/response and add a new `/api/runner/events` WebSocket (or SSE) stream. The backend subscribes to `wf:runner:*` internally and forwards messages to whichever pod holds that runner's socket. Pros: no new outbound dependency on the runner side; works through the same TLS that heartbeats already traverse. Cons: another long-lived connection per runner for the backend to manage, plus cross-pod routing if the runner's WS lands on a different pod than the Redis subscriber.

Whichever path is taken, the runner's existing HTTP protocol (this directory's [api-client.md](./api-client.md)) stays the source of truth for request/response semantics. Dispatch wake-up is a latency optimisation layered on top, not a replacement for the poll loop — the poll is still the only way the runner ever actually claims a job.

## Until the gap is closed

- The extra `POST /internal/runner-dispatch` call on job creation is harmless but pure overhead — roughly one internal HTTP call per queued job, plus one Redis `PUBLISH` with zero subscribers.
- The runner's poll interval (`3 * time.Second` in `runner.Run`) is the real lower bound on tool-dispatch latency. Tightening the ticker is the cheapest available improvement if the wake-up path is not going to be consumed soon.
- Anyone investigating "why does the runner seem slow to pick up jobs?" should be pointed here rather than at the dispatch endpoint — the endpoint looks like it would help but currently does not.
