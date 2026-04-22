## Fix Intel macOS binary build, update documentation, and align spec with HTTP-only protocol

### Problem

1. **Missing Intel macOS GUI binary** — The CI release workflow only built an arm64 (Apple Silicon) GUI binary for macOS. Users on Intel Macs had no GUI build available, limiting adoption on older hardware.

2. **Outdated default API URL** — The default API URL pointed to `https://app.workflowfiesta.com` (the frontend), but the runner should communicate with `https://api.workflowfiesta.com` (the API). The "Get a code" hyperlink in the registration wizard also pointed to the wrong host since it was derived directly from the API URL without mapping `api.` → `app.`.

3. **Stale documentation** — README, specs, and getting-started guides still referenced the old WebSocket protocol, legacy `--name`/`--org-id` registration flags, and outdated org URLs (`ss-libs` → `testfiesta`). The download table was missing several platform variants.

4. **Spec described a WebSocket protocol that doesn't exist** — `spec/api-client.md` and `spec/architecture.md` documented a persistent WebSocket at `/runner-ws` with ping/pong, Gorilla WebSocket, and reconnect logic. The actual code (`internal/api/client.go`) is a plain `net/http` client — `go.mod` has no websocket or redis dependency. This created confusion for anyone reading the spec.

5. **Wizard scroll overflow** — The first-run and registration wizard body content could overflow on smaller screens because the content area wasn't scrollable.

### Solution

**CI / Build**
- Added a new `build-darwin-gui-amd64` job to the release workflow that cross-compiles the macOS GUI binary for Intel (`GOARCH=amd64`) using `clang -arch x86_64`.
- Renamed the existing macOS GUI artifact from `darwin-gui` to `darwin-gui-arm64` for clarity.
- Wired both GUI artifacts into the `publish` job's `needs` list.

**API URL & Registration**
- Changed `DefaultAPIURL` in `internal/config/config.go` to `https://api.workflowfiesta.com`.
- Added `frontendURLFrom()` helper in `internal/localui/register.go` that maps API URLs to their frontend counterparts (`api.` → `app.`, `localhost:5000` → `localhost:3000`) so the "Get a code →" hyperlink always opens the correct web UI page.
- Added tests for the new `frontendURLFrom()` mapping.

**Spec realignment (incorporates docs/spec-http-not-websocket)**
- `spec/api-client.md` — Rewritten as "API Client — HTTP Protocol". Documents the 3 s poll loop, 30 s heartbeat loop, full endpoint list, bearer-token auth, `X-Org-Id` routing header, registration endpoint, and the correct Client Interface Summary matching actual code. Includes a "What the runner does NOT do" section explicitly calling out: no WebSocket, no Redis client, no ping/pong, no reconnect logic.
- `spec/architecture.md` — Overview, package-layout comments, core data-flow diagram, concurrency model, and external-dependencies table all updated. `github.com/gorilla/websocket` replaced with `net/http` (stdlib).
- `spec/commands.md` — `run` command steps updated to describe HTTP polling with heartbeat + poll loop (no longer claims WebSocket). `register` command updated to `--code` based flow.
- `spec/dispatch-wakeup.md` — New doc describing the dispatch wake-up gap. The backend publishes `{type: 'job_available', jobId}` on `wf:runner:<runnerId>`, but nothing subscribes — the runner has no Redis client. Documents evidence and outlines two plausible fixes (runner-side Redis subscriber vs. backend-side WebSocket/SSE bridge).

**General documentation**
- Rewrote README with a full platform/variant download table, local-mode section, updated env vars, and corrected URLs throughout.
- Updated `spec/config.md`, `spec/getting-started.md`, and `docs/local-mode.md` to match the new `--code`-based registration flow.

**UI**
- Wrapped the wizard body in `NewVScroll` in both `launch.go` and `register.go` to prevent content overflow on small displays.
