package runner

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// reposCacheDir returns the path to the bare clone cache for a given repo URL.
// Uses a SHA256 hash of the URL to create a stable directory name.
func reposCacheDir(repoURL string) string {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(repoURL)))[:16]
	return filepath.Join(os.Getenv("HOME"), ".wf-runner", "repos", hash)
}

// worktreePath returns the path for a run's worktree.
func worktreePath(runID string) string {
	return filepath.Join(os.Getenv("HOME"), ".wf-runner", "worktrees", runID)
}

// EnsureWorktree ensures the bare repo cache is up to date and creates a
// worktree at the standard path for the given run. Returns the worktree path.
func EnsureWorktree(repoURL, ref, runID string) (string, error) {
	cacheDir := reposCacheDir(repoURL)
	wtPath := worktreePath(runID)

	// Clone bare if not already present.
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(cacheDir), 0755); err != nil {
			return "", fmt.Errorf("create repos cache dir: %w", err)
		}
		cmd := exec.Command("git", "clone", "--bare", repoURL, cacheDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git clone --bare: %w\n%s", err, out)
		}
	} else {
		// Fetch latest from origin — best-effort, don't fail if offline.
		cmd := exec.Command("git", "-C", cacheDir, "fetch", "--all", "--prune")
		cmd.CombinedOutput() //nolint:errcheck
	}

	// Remove stale worktree if it exists.
	if _, err := os.Stat(wtPath); err == nil {
		RemoveWorktree(cacheDir, wtPath)
	}

	// Create parent directory for worktrees.
	if err := os.MkdirAll(filepath.Dir(wtPath), 0755); err != nil {
		return "", fmt.Errorf("create worktrees dir: %w", err)
	}

	// Resolve the ref — prefer origin/<ref> for branch names.
	resolvedRef := ref
	if resolvedRef == "" {
		resolvedRef = "HEAD"
	}

	cmd := exec.Command("git", "-C", cacheDir, "worktree", "add", "--detach", wtPath, resolvedRef)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Try with origin/ prefix for branch names.
		cmd2 := exec.Command("git", "-C", cacheDir, "worktree", "add", "--detach", wtPath, "origin/"+ref)
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("git worktree add: %w\n%s\n%s", err, out, out2)
		}
	}

	return wtPath, nil
}

// RemoveWorktree removes the worktree directory and prunes the reference from the bare repo.
func RemoveWorktree(cacheDir, wtPath string) {
	exec.Command("git", "-C", cacheDir, "worktree", "remove", "--force", wtPath).Run() //nolint:errcheck
	exec.Command("git", "-C", cacheDir, "worktree", "prune").Run()                     //nolint:errcheck
	os.RemoveAll(wtPath)                                                               //nolint:errcheck
}

// CleanupWorktree is called after a job completes to remove the worktree.
func CleanupWorktree(repoURL, runID string) {
	cacheDir := reposCacheDir(repoURL)
	wtPath := worktreePath(runID)
	RemoveWorktree(cacheDir, wtPath)
}
