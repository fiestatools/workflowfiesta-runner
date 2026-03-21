# Local UI — Fyne Desktop Interface

The `internal/localui` package provides the desktop GUI for the local executor mode. It is built with [Fyne v2](https://fyne.io/) and conditionally compiled: the `nolocalui` build tag replaces all Fyne symbols with stubs.

---

## Build Variants

| Build tag | GUI available | `HasGUI` constant |
|---|---|---|
| *(none)* | Yes — full Fyne GUI | `true` |
| `nolocalui` | No — stubs only | `false` |

When built with `nolocalui`, `RequestApproval` falls back to `headlessApproval` (terminal prompt), and all window/tray functions are no-ops.

---

## Headless Mode

Setting `localui.Headless = true` (via `run-local --headless`) disables all Fyne windows and uses terminal prompts instead. This allows the local executor to be used over SSH or in CI pipelines without a display.

On Linux, `hasDisplay()` checks `$DISPLAY` or `$WAYLAND_DISPLAY`. If neither is set and `HasGUI` is true, wizard commands return an error rather than crashing with a nil display.

---

## Approval Dialog (`approval.go`)

Shown when a job requires human approval before execution.

### Window layout

```
┌──────────────────────────────────────┐
│ ⚠  Job Approval Required             │
│    Review the script before allowing │
├──────────────────────────────────────┤
│ From workflow on  <runner-name>      │
│ ┌──────────────────────────────────┐ │
│ │  <script preview, first 10 lines>│ │
│ └──────────────────────────────────┘ │
│ Auto-deny in 120s  [========    ]    │
├──────────────────────────────────────┤
│ ✕ Deny  Allow for session  Always allow  ✓ Allow │
└──────────────────────────────────────┘
```

### Decision outcomes

| Button | `ApprovalResult` | Effect |
|---|---|---|
| Deny | `ApprovalDeny` (0) | Job rejected; exit code 1 |
| Allow | `ApprovalAllow` (1) | Script runs once |
| Allow for session | `ApprovalAllowSession` (2) | Script fingerprint added to in-memory session list |
| Always allow | `ApprovalAlwaysAllow` (3) | Fingerprint saved to `always_allowed_patterns` in `runner.yaml` |

### Countdown

A 1-second ticker counts down from `ConfirmTimeout`. At zero, `ApprovalDeny` fires automatically. The countdown label and progress bar update via `fyne.Do()`.

### Sound

When `sound_on_approval: true` is configured, `playApprovalSound()` is called on window show.

### Headless fallback

When `Headless = true`, `headlessApproval` reads a `y/N` response from stdin with the same timeout. Auto-deny fires on timeout.

---

## Status Window (`statuswindow.go`)

The `StatusWindow` is always visible while the runner is active. It implements `runner.StatusSink` and receives all lifecycle events from the runner goroutine via thread-safe `fyne.Do()` calls.

### Sections

**Header**
- WorkflowFiesta logo icon
- Runner name and API URL
- Status badge: Offline (red) / Idle (green) / Running (amber)
- Settings gear button (opens Settings window)

**Stats strip** (4 columns)
- TODAY: total jobs dispatched this session
- PASSED: jobs with exit code 0
- FAILED: jobs with non-zero exit code
- UPTIME: mm:ss elapsed since runner started (switches to hh:mm after 1 hour)

**CTA banner** (shown when connected)
- "Your runner is live!" with a "Start Working →" button linking to the web app.
- The web URL is derived from the API URL: port 3001 → 3000, or `/api` suffix stripped.

**Active job card** (shown only while a job is running, amber background)
- Job ID, Docker image pill, script preview (first line)
- Live elapsed timer (MM:SS, updates every second)

**Recent jobs** (up to 5)
- Pass/fail dot, job ID, image, time-ago label

**Terminal output panel**
- Monospace `RichText` widget capped at 500 segments (oldest 100 trimmed when over limit)
- Auto-scrolls to bottom on each `AppendLog` call
- "Clear" button resets segments

### Thread safety

All widget mutations use `fyne.Do(func() { ... })`. The `StatusWindow` is created on the main thread before `a.Run()` but its methods are called from runner goroutines.

### Window size persistence

Window size is polled every second; written to Fyne preferences (`status.window.width`, `status.window.height`) after 3 consecutive stable readings to avoid spurious writes during content reflows.

---

## Registration Wizard (`register.go` — `RunRegisterWizard`)

A 4-step Fyne window opened by `register-local`.

| Step | Title | Content |
|---|---|---|
| 0 | Connect & Register | API URL, Org ID, Runner Name fields + "Connect & Register" button |
| 1 | Runner Registered! | Token display (monospace), Runner ID, Copy button; "Token saved automatically" note |
| 2 | Local Permissions | Allowed paths (multiline entry + Browse…), confirm radio, timeouts, sound checkbox, network radio |
| 3 | All Set! | Start command in terminal block, credential path warning |

Navigation: progress dots (active dot is a wider pill), Back/Next/Save buttons.

Registration API call (`callRegisterAPI`) runs in a goroutine; wizard shows "Connecting…" while waiting. Friendly error messages are shown inline for: connection refused, host not found, TLS error, 400 bad request, 401/403 auth, 404 wrong path, 409 duplicate name, 500 server error.

---

## Setup Wizard (`wizard.go` — `RunWizard`)

A 4-step wizard opened by `init-local`. Only configures `runner.yaml`; does not call the registration API.

| Step | Title |
|---|---|
| 0 | Folder Access |
| 1 | Approval & Timeouts |
| 2 | Network Access |
| 3 | Review Settings (summary + Save & Start) |

---

## Settings Window (`settings.go` — `OpenSettingsWindow`)

A scrollable settings window opened by the gear button in the status window or via the system tray.

**Sections:**
- **Execution**: confirm mode (radio), confirm timeout, max timeout
- **Sound**: play sound on approval (checkbox)
- **Security**: allowed paths (multiline entry), network mode (radio)
- **Permissions (Advanced)**: list of always-allowed fingerprints with "Remove" buttons. Session allowlist clears on restart.
- **Approval Popup Position**: X/Y coordinate entries (stored in Fyne preferences, not `runner.yaml`)
- **Actions**: "Open Audit Log" button (opens with default system handler)

Clicking "Save & Close" writes `runner.yaml` and calls the `onSave` callback, which hot-swaps the `LocalConfig` pointer in the running config without restarting.

---

## Auto-Launch (`launch.go` — `RunAutoLaunch`)

The zero-CLI entry point for double-click launches.

1. `config.Load()` — reads env vars and credentials file.
2. If `Token != ""`: load `runner.yaml`, set `ExecutorType = "local"`, call `startFn(cfg)` (opens windows + tray).
3. If `Token == ""`: show `showFirstRunWizard` (3-step in-app wizard). On completion, call `startFn(cfg)` and hide the wizard.
4. `a.Run()` blocks the main thread (Fyne event loop).

`startFn` is a callback to avoid import cycles between `localui` and `runner`.

---

## System Tray

The system tray provides a minimal menu for the running runner:
- Icon: WorkflowFiesta logo
- Menu: runner name (disabled), separator, "Show Status Window", "Quit Runner"

`StartTray` / `SetupTray` both call `fyne.CurrentApp().SetSystemTrayMenu(...)` and block until `QuitApp()`.

---

## Design Tokens

All Fyne windows use a consistent dark-mode color palette:

| Token | Hex | Use |
|---|---|---|
| `colorSurface` | `#0f172a` | Window background |
| `colorCard` | `#1e293b` | Card / panel background |
| `colorBorder` | `#334155` | Card borders |
| `colorText` | `#f8fafc` | Primary text |
| `colorMuted` | `#94a3b8` | Secondary text |
| `colorSuccess` | `#22c55e` | Pass / connected |
| `colorAmber` | `#f59e0b` | Warning / running |
| `colorDanger` | `#ef4444` | Error / failed / offline |
| `colorTermBg` | `#020817` | Terminal panel background |
