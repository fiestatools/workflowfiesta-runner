# Security

This document describes the security mechanisms of the local executor. The Docker and Kubernetes executors rely on container isolation and are not covered here.

---

## Threat Model

The local executor allows a remote WorkflowFiesta agent to run shell scripts on the operator's machine. The threats considered are:

1. **Unintended destructive commands** (accidental `rm -rf`, disk format).
2. **Privilege escalation / lateral movement** via filesystem access outside the working directory.
3. **Exfiltration** via outbound network connections.
4. **Resource exhaustion** (CPU, memory, disk) by a runaway script.

The security design is defense-in-depth: four independent layers, each of which can stop or limit a malicious or buggy script.

---

## Layer 1: Blocked Patterns

Hard-coded and user-configurable regexes that reject jobs before any execution. These are absolute blocks — they cannot be bypassed by session or always-allow lists.

**Default patterns** (from `localconfig.Default()`):

| Pattern | Blocks |
|---|---|
| `rm\s+-rf\s+/` | `rm -rf /` and variants |
| `rm\s+-rf\s+~` | `rm -rf ~` |
| `dd\s.*of=/dev/[a-z]` | `dd` writing directly to a disk device |
| `mkfs\.` | Filesystem formatting commands |
| `:.*>.*\s*/dev/[a-z]` | Fork bomb / redirect to device |

Additional patterns can be added to `blocked_patterns` in `runner.yaml`.

**Implementation:** `blockedPatternCheck` compiles each pattern with `regexp.Compile` per call (invalid regexes are logged and skipped). Matching is against the full script string.

---

## Layer 2: Approval Gate

An interactive human-in-the-loop check. The approval decision determines the execution path:

| Decision | Effect on future identical scripts |
|---|---|
| **Deny** | Job rejected immediately |
| **Allow** (once) | Runs this time; next job with same script will prompt again |
| **Allow for session** | SHA-256 fingerprint added to `sessionAllows` (in-memory, cleared on restart) |
| **Always allow** | SHA-256 fingerprint saved to `always_allowed_patterns` in `runner.yaml` |

**Script fingerprinting:** `sha256.Sum256([]byte(script))` — a hex string of the entire script content. Any modification to the script (including whitespace) produces a different fingerprint.

**Approval modes** (`confirm` in `runner.yaml`):

- `"always"`: every job prompts, unless the fingerprint is in a bypass list.
- `"destructive"` (default): prompts only when the script matches one of the destructive patterns: `\brm\b`, `\bmv\b`, `\brmdir\b`, `\btruncate\b`, or `>\s*\S` (output redirect).
- `"never"`: no prompts. Use only in fully trusted environments.

**Timeout:** if the operator does not respond within `confirm_timeout` seconds, the job is automatically denied.

---

## Layer 3: Kernel Sandbox (`sandbox: kernel`)

### Linux — Landlock LSM

**Requirement:** Linux kernel ≥ 5.13. Older kernels receive a warning and run without Landlock.

**How it works:**

1. The goroutine executing the script calls `runtime.LockOSThread()`. The OS thread is then restricted by Landlock and is intentionally never returned to the Go runtime pool (no `UnlockOSThread` call), preventing the restricted thread from contaminating other goroutines.

2. `landlockCreateRuleset` probes the Landlock ABI version via `landlock_create_ruleset(NULL, 0, LANDLOCK_CREATE_RULESET_VERSION)`.

3. A ruleset is created with read-write access rights:
   - `EXECUTE`, `WRITE_FILE`, `READ_FILE`, `READ_DIR`, `REMOVE_DIR`, `REMOVE_FILE`, `MAKE_DIR`, `MAKE_REG`, `MAKE_SYM`

4. Rules are added for:
   - **System paths** (read-only): `/usr`, `/bin`, `/lib`, `/lib64`, `/etc`, `/proc`, `/dev/null`, `/dev/urandom`, `/tmp`
   - **Configured `allowed_paths`** (read-write or read-only based on `:ro` suffix)

5. `landlockRestrictSelf` activates the ruleset on the current thread.

6. The script subprocess is forked from the restricted thread.

**Network isolation on Linux:**
When `network: none` is set, the command is wrapped with `unshare -n /bin/sh <script>`. This creates a new network namespace with no interfaces except loopback. Requires Linux user namespaces (available on most modern distros).

### macOS — Seatbelt (sandbox-exec)

**How it works:**

A TinyScheme profile is generated at runtime from `LocalConfig` and passed to `sandbox-exec -p <profile> /bin/sh <script>`.

**Profile structure:**

```scheme
(version 1)
(deny default)

; Essential system paths (read)
(allow file-read* (subpath "/usr"))
(allow file-read* (subpath "/bin"))
; ... /sbin, /lib, /System, /Library, /private/etc, /dev ...
(allow file-read* (literal "/"))
(allow process-exec)
(allow process-fork)
(allow signal)

; User-configured paths
(allow file-read* file-write* (subpath "/home/user/projects"))
(allow file-read* (subpath "/home/user/Documents"))  ; :ro path

; Network (one of):
(allow network*)                                      ; network: all
(allow network-bind (local ip))                       ; network: localhost
(deny network-outbound (remote ip "*:*"))
(allow network-outbound (remote ip "localhost:*"))
(deny network*)                                       ; network: none
```

`sandbox-exec` is part of macOS; no external dependencies required. If `sandbox-exec` is not found (unusual), execution falls back to the unsandboxed path with a warning.

### Windows

No kernel sandbox is implemented on Windows. Only blocked patterns, the approval gate, and the resource limits (where applicable) apply.

---

## Layer 4: Resource Limits (Unix only)

Scripts are wrapped with `ulimit` before execution:

```sh
ulimit -t <max_timeout> -v 1048576 2>/dev/null
<original script>
```

| Limit | Value |
|---|---|
| CPU time (`-t`) | `max_timeout` seconds |
| Virtual memory (`-v`) | 1 GB (1,048,576 KB) |

The `2>/dev/null` suppression prevents `ulimit` errors from appearing in job output on systems that restrict the virtual memory limit (e.g., some container environments). The script still runs if `ulimit` silently fails.

---

## Minimal Environment

The subprocess receives a clean environment (see [executors/local.md](./executors/local.md#environment-construction)). Notably absent:
- `LD_PRELOAD`, `DYLD_INSERT_LIBRARIES`, `LD_LIBRARY_PATH` — prevents shared-library hijacking.
- `PYTHONPATH`, `RUBYOPT`, `NODE_PATH` — prevents language-level path injection.
- Session variables from the parent shell.

Job-provided `envVars` from the API are merged in but cannot override `PATH` at a privileged level because the environment is constructed fresh, not inherited.

---

## Audit Log

Every job execution writes a JSONL entry to `~/.workflowfiesta/audit.log` (configurable via `audit_log`). This provides a tamper-evident (append-only file, mode 0600) record for review.

Fields: `time`, `job_id`, `script`, `decision`, `reason`, `exit_code`, `duration_ms`.

---

## Known Limitations

- **Landlock protects filesystem access but not process creation**: a script can still spawn arbitrary child processes. Combined with the network sandbox and allowed-paths restriction, this limits damage but does not eliminate it.
- **Session/always-allow bypasses are fingerprint-based**: if the same fingerprint is re-used for a different (malicious) script, the bypass applies. In practice the SHA-256 pre-image resistance makes this infeasible.
- **`confirm: never` removes the human check entirely**: use only when the WorkflowFiesta server and all AI agents that can invoke this runner are fully trusted.
- **Windows has no kernel sandbox**: rely on blocked patterns and the approval gate for protection.
