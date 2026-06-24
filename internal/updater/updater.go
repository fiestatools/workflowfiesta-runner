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
	repoOwner   = "fiestatools"
	repoName    = "workflowfiesta-runner"
	fullTimeout = 5 * time.Minute
)

type updateEntry struct {
	Time        string `json:"time"`
	Event       string `json:"event"`
	FromVersion string `json:"from_version,omitempty"`
	ToVersion   string `json:"to_version,omitempty"`
	Error       string `json:"error,omitempty"`
}

func writeAudit(auditLogPath, event, from, to, errMsg string) {
	_ = auditlog.AppendLine(auditLogPath, updateEntry{
		Time:        time.Now().UTC().Format(time.RFC3339),
		Event:       event,
		FromVersion: from,
		ToVersion:   to,
		Error:       errMsg,
	})
}

// Run checks GitHub for a newer release. If one is found and isIdle returns
// true (no job is active), it downloads, verifies, installs, relaunches, and
// then calls quitFn to stop the current process.
func Run(currentVersion string, isGUI bool, logFn func(string), isIdle func() bool, quitFn func(), auditLogPath string) {

	githubSource, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{})
	if err != nil {
		logFn(fmt.Sprintf("[updater] init failed: %v", err))
		return
	}

	// Hold a direct pointer to progressSource so we can set onProgress after
	// DetectLatest (when we know the asset size) but before UpdateTo downloads.
	src := &progressSource{inner: githubSource}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:    src,
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
		Filters:   []string{regexp.QuoteMeta(platformAsset(isGUI))},
	})
	if err != nil {
		logFn(fmt.Sprintf("[updater] init failed: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), fullTimeout)
	defer cancel()

	repo := selfupdate.NewRepositorySlug(repoOwner, repoName)
	rel, found, err := updater.DetectLatest(ctx, repo)
	if err != nil {
		logFn(fmt.Sprintf("[updater] version check failed: %v", err))
		return
	}
	if !found || !rel.GreaterThan(currentVersion) {
		return
	}

	sizeMB := float64(rel.AssetByteSize) / 1024 / 1024
	logFn(fmt.Sprintf("[updater] update available: %s → %s (%.1f MB)", currentVersion, rel.Version(), sizeMB))
	writeAudit(auditLogPath, "update_started", currentVersion, rel.Version(), "")

	if isIdle != nil && !isIdle() {
		logFn(fmt.Sprintf("[updater] update %s ready — will apply on next restart (job in progress)", rel.Version()))
		writeAudit(auditLogPath, "update_deferred", currentVersion, rel.Version(), "job in progress")
		return
	}

	// Wire up progress now that we know the asset size.
	// src.onProgress is nil during DetectLatest (no download happens there)
	src.onProgress = newProgressCallback(int64(rel.AssetByteSize), isGUI, logFn)

	exePath, err := os.Executable()
	if err != nil {
		logFn(fmt.Sprintf("[updater] resolve executable: %v", err))
		writeAudit(auditLogPath, "update_failed", currentVersion, rel.Version(), err.Error())
		return
	}

	if err := updater.UpdateTo(ctx, rel, exePath); err != nil {
		logFn(fmt.Sprintf("[updater] install failed: %v", err))
		writeAudit(auditLogPath, "update_failed", currentVersion, rel.Version(), err.Error())
		return
	}

	logFn(fmt.Sprintf("[updater] updated to %s — restarting", rel.Version()))
	writeAudit(auditLogPath, "update_installed", currentVersion, rel.Version(), "")

	if err := relaunch(); err != nil {
		logFn(fmt.Sprintf("[updater] relaunch failed: %v", err))
		writeAudit(auditLogPath, "update_failed", currentVersion, rel.Version(), err.Error())
		return
	}

	quitFn()
}

func relaunch() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exePath, os.Args[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

// platformAsset returns the release asset filename for the current OS/arch.
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
