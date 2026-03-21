# Local Executor

The local executor (`internal/executor/local.go`) runs job scripts directly on the host machine without any containerization. It is designed for developer machines where the runner needs to access local files, tools, or system state.

Because scripts run with the host user's privileges, the local executor has multiple security layers to limit what they can do.

---

## Activation

- `run-local` command forces `ExecutorType = "local"`.
- `CONTAINER_RUNTIME=local` (explicit override).
- Default on Windows (no Docker socket).

---

## Security Layers (applied in order)

### Layer 1: Blocked Pattern Check

Before any approval dialog, the script is checked against a list of regular expressions from `blocked_patterns` in `runner.yaml`. If any pattern matches, the job is immediately rejected with exit code 1 and an error message in the output log.

Default blocked patterns:
```
rm\s+-rf\s+/
rm\s+-rf\s+~
dd\s.*of=/dev/[a-z]
mkfs\.
:.*>.*\s*/dev/[a-z]
```

These are designed to catch catastrophic data destruction commands. They cannot be bypassed by session or always-allow lists.

### Layer 2: Confirmation Gate

Applies when `confirm` in `runner.yaml` is not `"never"`.

**`confirm: always`** — every job shows the approval dialog.

**`confirm: destructive`** (default) — shows the approval dialog only when the script matches destructive patterns:
- `\brm\b`
- `\bmv\b`
- `\brmdir\b`
- `\btruncate\b`
- `>\s*\S` (output redirect)

**`confirm: never`** — no approval dialog.

**Bypass lists:**
- **Session allow list** (`ApprovalAllowSession`): adds the script's SHA-256 fingerprint to an in-memory list for the current runner process. Cleared on restart.
- **Always-allow list** (`ApprovalAlwaysAllow`): adds the fingerprint to `always_allowed_patterns` in `runner.yaml` and saves immediately. Persisted across restarts.

When a job is received whose fingerprint is in either list, the approval dialog is skipped.

### Layer 3: Kernel Sandbox

Applied when `sandbox: kernel` is set (off by default).

- **Linux:** Landlock LSM restricts the process to the configured `allowed_paths`. System paths (`/usr`, `/bin`, `/lib`, `/etc`, `/proc`, `/dev/null`, `/dev/urandom`, `/tmp`) get read-only access. Configured paths get read-write or read-only access based on the `:ro` suffix. Optionally, network namespace isolation is applied with `unshare -n`.
- **macOS:** `sandbox-exec` with a generated TinyScheme profile restricts file access to system paths and configured paths. Network access is controlled with `(allow network*)`, `(deny network-outbound ...)`, or `(deny network*)`.
- **Windows:** No kernel sandbox; ulimit wrapping is also skipped.

See [security.md](../security.md) for implementation details.

### Layer 4: Resource Limits (Unix only)

The script is wrapped with `ulimit` before execution:

```sh
ulimit -t <max_timeout> -v 1048576  # 1 GB virtual memory
<original script>
```

This limits CPU time and virtual memory usage for the subprocess.

---

## Environment Construction

The executor builds a minimal, clean environment to prevent environment variable injection attacks:

```
PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin   (platform-specific)
HOME=<first writable allowed path>
TERM=xterm-256color
LANG=en_US.UTF-8
<job-provided env vars, merged on top>
```

Dangerous loader variables (`LD_PRELOAD`, `DYLD_INSERT_LIBRARIES`, etc.) are excluded because they are not included in the initial set and cannot be injected through the job's `envVars` map unless explicitly named.

**Platform PATH defaults:**
- macOS: `/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin` (covers both Intel and Apple Silicon Homebrew)
- Linux: `/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin`
- Windows: inherited from `%PATH%`

---

## Working Directory

The CWD of each script is set to `LocalConfig.WorkingDir()`, which returns the first writable (non-`:ro`) path from `allowed_paths` that exists as a directory. Falls back to `$HOME`.

---

## Output Streaming

stdout and stderr are both connected via pipes. Each is consumed by a scanner goroutine (max token size 10 MB to handle large single-line outputs). Lines are sent as `"<line>\n"` chunks to `input.OutputChan`. Both goroutines are waited on before `Execute` returns.

---

## Audit Log

Every job execution appends a JSON line to `audit_log` (default `~/.workflowfiesta/audit.log`):

```json
{
  "time": "2024-01-15T10:30:00Z",
  "job_id": "job_abc123",
  "script": "echo hello",
  "decision": "approved",
  "exit_code": 0,
  "duration_ms": 42
}
```

`decision` values: `"approved"`, `"denied"`, `"blocked"`, `"failed"`.

The audit log directory is created with mode 0700; the log file is created with mode 0600.

---

## Timeout Handling

The job timeout from the API is clamped to `max_timeout` from `runner.yaml`:

```go
if timeout == 0 || timeout > maxTimeout {
    timeout = maxTimeout
}
```

The subprocess runs with `context.WithTimeout(ctx, timeout + 5s)`. The extra 5 seconds allows the executor to finish cleanup after the script is killed.

---

## ApprovalReporter Interface

The local executor accepts an optional `ApprovalReporter` (implemented by `api.Client`):

```go
type ApprovalReporter interface {
    ReportApprovalPending(jobID, runnerName string) error
    ReportApprovalResolved(jobID string, approved bool) error
}
```

When set, the executor reports approval state changes back to the API so the web UI can show the pending/resolved status in real time.
