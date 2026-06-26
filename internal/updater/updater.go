package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"workflowfiesta-runner/internal/auditlog"
)

const (
	downloadTimeout   = 5 * time.Minute
	drainTimeout      = 10 * time.Minute
	drainPollInterval = 5 * time.Second

	repoOwner = "fiestatools"
	repoName  = "workflowfiesta-runner"
)

// Options configures a single auto-update run.
type Options struct {
	// Version is the currently running binary version (e.g. "v0.11.5").
	// Pass "dev" to skip the check entirely for local builds.
	Version string

	// IsGUI selects the correct release asset variant (GUI vs headless)
	// and controls the progress display style.
	IsGUI bool

	// Log receives human-readable progress messages. Safe from any goroutine.
	Log func(string)

	// IsIdle returns true when no jobs are currently active.
	IsIdle func() bool

	// Drain notifies the server to stop dispatching jobs to this runner.
	// Nil = skip server notification (still waits locally via IsIdle).
	Drain func() error

	// Quit stops the current process after the new binary has been launched.
	// GUI: pass localui.QuitApp. Headless: pass func() { os.Exit(0) }.
	Quit func()

	// CancelDrain reverses a drain that was initiated but not completed (e.g.
	// download failure, relaunch failure, drain timeout). Nil = do nothing.
	CancelDrain func()

	// SkipRelaunch skips spawning a replacement process. Used by the standalone
	// `update` subcommand, which is a separate process from the running runner.
	SkipRelaunch bool

	// AuditLogPath is the path to write audit log entries. Pass "" to disable.
	AuditLogPath string

	// AutoUpdate installs the update immediately when found.
	// Default false: check runs in background, OnUpdateAvailable is called instead.
	AutoUpdate bool

	// OnUpdateAvailable is called when a newer version is found and AutoUpdate
	// is false. Receives the latest version string so the caller can show a
	// banner (GUI) or print a notice (headless). Nil = do nothing.
	OnUpdateAvailable func(latestVersion string)
}

// Start launches the update check in a background goroutine.
func Start(opts Options) {
	go run(opts)
}

// Install runs the full update flow synchronously — drain, download, verify,
// replace, relaunch. Used by the `update` subcommand and the GUI "Update Now"
// button. Blocks until the binary has been replaced and the new process is
// launched, then calls opts.Quit.
func Install(opts Options) {
	applyUpdate(opts)
}

type updateEntry struct {
	Time        string `json:"time"`
	Event       string `json:"event"`
	FromVersion string `json:"from_version,omitempty"`
	ToVersion   string `json:"to_version,omitempty"`
	Error       string `json:"error,omitempty"`
}

func writeAudit(path, event, from, to, errMsg string) {
	_ = auditlog.AppendLine(path, updateEntry{
		Time:        time.Now().UTC().Format(time.RFC3339),
		Event:       event,
		FromVersion: from,
		ToVersion:   to,
		Error:       errMsg,
	})
}

func run(opts Options) {
	if opts.Version == "dev" {
		return
	}

	rel, found := detectUpdate(opts)
	if !found {
		return
	}

	if opts.AutoUpdate {
		// Opt-in: install immediately.
		applyUpdate(opts)
		return
	}

	// Default: notify only.
	if opts.OnUpdateAvailable != nil {
		opts.OnUpdateAvailable(rel.Version())
	}
}

// detectUpdate checks GitHub for a release newer than opts.Version.
// Returns the release and true if one is found, nil and false otherwise.
func detectUpdate(opts Options) (*selfupdate.Release, bool) {
	log := opts.Log

	githubSource, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		log(fmt.Sprintf("[updater] init failed: %v", err))
		return nil, false
	}

	src := &progressSource{inner: githubSource}

	u, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:    src,
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
		Filters:   []string{regexp.QuoteMeta(platformAsset(opts.IsGUI))},
	})
	if err != nil {
		log(fmt.Sprintf("[updater] init failed: %v", err))
		return nil, false
	}

	detectCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo := selfupdate.NewRepositorySlug(repoOwner, repoName)
	rel, found, err := u.DetectLatest(detectCtx, repo)
	if err != nil {
		log(fmt.Sprintf("[updater] version check failed: %v", err))
		return nil, false
	}
	if !found || !rel.GreaterThan(opts.Version) {
		return nil, false
	}
	return rel, true
}

// applyUpdate runs the full drain → download → verify → replace → relaunch
// sequence. Called either from the background goroutine (AutoUpdate=true) or
// directly by Install (the `update` subcommand and the GUI "Update Now" button).
func applyUpdate(opts Options) {
	if opts.Version == "dev" {
		opts.Log("[updater] dev build — skipping update")
		return
	}

	log := opts.Log

	githubSource, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		log(fmt.Sprintf("[updater] init failed: %v", err))
		return
	}

	src := &progressSource{inner: githubSource}

	u, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:    src,
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
		Filters:   []string{regexp.QuoteMeta(platformAsset(opts.IsGUI))},
	})
	if err != nil {
		log(fmt.Sprintf("[updater] init failed: %v", err))
		return
	}

	detectCtx, detectCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer detectCancel()

	repo := selfupdate.NewRepositorySlug(repoOwner, repoName)
	rel, found, err := u.DetectLatest(detectCtx, repo)
	if err != nil {
		log(fmt.Sprintf("[updater] version check failed: %v", err))
		return
	}
	if !found || !rel.GreaterThan(opts.Version) {
		log("[updater] already on the latest version")
		return
	}

	sizeMB := float64(rel.AssetByteSize) / 1024 / 1024
	log(fmt.Sprintf("[updater] updating %s → %s (%.1f MB)", opts.Version, rel.Version(), sizeMB))
	writeAudit(opts.AuditLogPath, "update_started", opts.Version, rel.Version(), "")

	// Notify the server to stop dispatching jobs.
	if opts.Drain != nil {
		if err := opts.Drain(); err != nil {
			log(fmt.Sprintf("[updater] drain notify failed (continuing anyway): %v", err))
		} else {
			log("[updater] server notified — draining")
		}
	}

	succeeded := false
	defer func() {
		if !succeeded && opts.CancelDrain != nil {
			opts.CancelDrain()
		}
	}()

	// Wait for any active job to finish.
	if !waitUntilIdle(opts.IsIdle, log) {
		log("[updater] timed out waiting for jobs to finish — will apply on next restart")
		writeAudit(opts.AuditLogPath, "update_deferred", opts.Version, rel.Version(), "drain timeout")
		return
	}

	src.onProgress = newProgressCallback(int64(rel.AssetByteSize), opts.IsGUI, log)

	downloadCtx, downloadCancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer downloadCancel()

	exePath, err := os.Executable()
	if err != nil {
		log(fmt.Sprintf("[updater] resolve executable: %v", err))
		writeAudit(opts.AuditLogPath, "update_failed", opts.Version, rel.Version(), err.Error())
		return
	}

	if err := u.UpdateTo(downloadCtx, rel, exePath); err != nil {
		log(fmt.Sprintf("[updater] install failed: %v", err))
		writeAudit(opts.AuditLogPath, "update_failed", opts.Version, rel.Version(), err.Error())
		return
	}

	writeAudit(opts.AuditLogPath, "update_installed", opts.Version, rel.Version(), "")

	if opts.SkipRelaunch {
		log(fmt.Sprintf("[updater] updated to %s — restart the runner to apply", rel.Version()))
		succeeded = true
		opts.Quit()
		return
	}

	log(fmt.Sprintf("[updater] updated to %s — restarting", rel.Version()))
	if err := relaunch(); err != nil {
		log(fmt.Sprintf("[updater] relaunch failed: %v", err))
		writeAudit(opts.AuditLogPath, "update_failed", opts.Version, rel.Version(), err.Error())
		return
	}

	succeeded = true

	opts.Quit()
}

func waitUntilIdle(isIdle func() bool, log func(string)) bool {
	if isIdle == nil || isIdle() {
		return true
	}
	log("[updater] waiting for active job to finish...")
	deadline := time.NewTimer(drainTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(drainPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			return false
		case <-ticker.C:
			if isIdle() {
				return true
			}
			log("[updater] job still running, waiting...")
		}
	}
}

func relaunch() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exePath, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

func platformAsset(isGUI bool) string {
	name := fmt.Sprintf("workflowfiesta-runner-%s-%s", runtime.GOOS, runtime.GOARCH)
	if isGUI {
		name += "-gui"
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}
