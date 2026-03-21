# Local Config — runner.yaml

The `runner.yaml` file controls the behavior of the local executor. It is read at startup by `run-local` and can be modified live through the Settings window without restarting the runner.

**Default path:** `~/.workflowfiesta/runner.yaml`
**Override path:** `--config <path>` flag or `LOCAL_CONFIG_PATH` env var.
**File mode:** 0600 (written by the runner; parent directory 0700).

---

## Schema

```yaml
# Paths that scripts are allowed to read and write.
# Relative to home dir with ~ expansion. Append :ro for read-only.
allowed_paths:
  - ~/

# When to show an approval dialog before running a script.
# "always"      - every job
# "destructive" - only when the script contains rm, mv, truncate, or output redirects (default)
# "never"       - auto-approve everything
confirm: destructive

# How long (seconds) to wait for the user to approve or deny a job.
# After this timeout the job is automatically denied.
confirm_timeout: 120

# Maximum time (seconds) a single job may run. Jobs exceeding this are killed.
max_timeout: 180

# Network access for scripts (local executor only).
# "all"       - unrestricted (default)
# "localhost" - allow outbound only to localhost (Seatbelt on macOS; honor-system on Linux without kernel sandbox)
# "none"      - block all network (unshare -n on Linux, deny network* on macOS)
network: all

# OS-level sandbox for the script subprocess.
# "none"   - no kernel-level isolation (default; resource limits still apply)
# "kernel" - Landlock on Linux (kernel ≥ 5.13), Seatbelt on macOS
sandbox: none

# Regex patterns that, if matched, immediately block the job (no approval dialog).
blocked_patterns:
  - 'rm\s+-rf\s+/'
  - 'rm\s+-rf\s+~'
  - 'dd\s.*of=/dev/[a-z]'
  - 'mkfs\.'
  - ':.*>.*\s*/dev/[a-z]'

# Path to the JSONL audit log. Leave empty to disable.
audit_log: ~/.workflowfiesta/audit.log

# Play a system sound when an approval dialog appears.
sound_on_approval: false

# SHA-256 fingerprints of scripts approved with "Always allow".
# Managed automatically by the runner; edit with caution.
always_allowed_patterns: []

# UUID of the WorkflowFiesta environment this runner is associated with.
# Set automatically by the registration wizard; do not change manually.
environment_id: ""
```

---

## Field Reference

### `allowed_paths`

List of filesystem paths scripts may access. Used by:
- **Landlock (Linux, `sandbox: kernel`):** enforced at kernel level — scripts literally cannot open files outside these paths.
- **Seatbelt (macOS, `sandbox: kernel`):** enforced by `sandbox-exec` profile.
- **Working directory selection:** `WorkingDir()` returns the first writable (non-`:ro`) path that exists.

**`~` expansion:** tilde is expanded to the user's home directory on load.

**`:ro` suffix:** marks a path as read-only. Example:
```yaml
allowed_paths:
  - ~/projects       # read-write
  - ~/Documents:ro   # read-only
  - /etc:ro          # system files, read-only
```

### `confirm`

Controls when the approval dialog is shown. See [executors/local.md](./executors/local.md#layer-2-confirmation-gate) for the full logic, including bypass lists.

### `confirm_timeout`

If the user does not respond within this many seconds, the job is automatically denied. The countdown is shown in the approval dialog. Default: 120 s.

### `max_timeout`

Hard upper bound on job duration. The runner clamps the API-provided timeout to this value. On Unix, wrapped with `ulimit -t <max_timeout>`. Default: 180 s.

### `network`

Controls outbound network access:
- `"all"`: no restriction (default).
- `"localhost"`: on macOS with `sandbox: kernel`, outbound is blocked except to localhost. On Linux, requires `sandbox: kernel` + `unshare` to be meaningful.
- `"none"`: on Linux with `sandbox: kernel`, script runs in a new network namespace (`unshare -n`). On macOS with `sandbox: kernel`, `(deny network*)` is added to the Seatbelt profile.

Without `sandbox: kernel`, `network` is informational only; scripts can freely open sockets.

### `sandbox`

- `"none"` (default): only `ulimit` resource limits apply.
- `"kernel"`: activates Landlock (Linux) or Seatbelt (macOS). Falls back to `"none"` gracefully if the kernel does not support Landlock (< 5.13) or `sandbox-exec` is not found.

### `blocked_patterns`

Regular expressions (Go `regexp` syntax). If any pattern matches the script body, the job is rejected before even showing an approval dialog. The matched pattern name is included in the output log and the audit entry.

### `audit_log`

Absolute path (with `~` expansion) for the JSONL audit log. Set to empty string `""` to disable. Each line is a JSON object with `time`, `job_id`, `script`, `decision`, `exit_code`, and `duration_ms`.

### `always_allowed_patterns`

SHA-256 fingerprints (hex strings) of scripts that have been approved with "Always allow". When a job arrives whose script matches a fingerprint in this list, the approval dialog is skipped regardless of `confirm` mode.

Managed automatically by the runner. You can remove fingerprints through the Settings window's "Permissions (Advanced)" section.

### `environment_id`

UUID of the WorkflowFiesta environment this runner is associated with. Set by the registration wizard. Modifying this manually disconnects the runner from its environment.

---

## Load / Save Behavior

- **`localconfig.Load(path)`**: reads the file if it exists; applies defaults for missing fields; expands `~` in all path fields. Returns defaults if the file does not exist (first-run case).
- **`localconfig.Save(cfg, path)`**: marshals to YAML; creates parent directories (mode 0700); writes with mode 0600.
- **Live reload**: the Settings window calls `localconfig.Save` then passes the updated `*LocalConfig` to the runner via a callback. The in-memory `cfg.LocalConfig` pointer is swapped. No restart required.

---

## Runtime-Only Fields

These fields are present in the Go struct but are not persisted to YAML:

| Field | Set by | Description |
|---|---|---|
| `Headless` | `run-local --headless` flag | Skip GUI; use terminal prompts |
| `RunnerName` | `config.Load()` → `localui.launch.go` | Runner display name (from `WORKFLOWFIESTA_RUNNER_NAME`) |
