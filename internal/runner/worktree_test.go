package runner_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"workflowfiesta-runner/internal/runner"
)

// ── reposCacheDir ─────────────────────────────────────────────────────────────

func TestReposCacheDir_Deterministic(t *testing.T) {
	url := "https://github.com/example/repo.git"
	got1 := runner.ReposCacheDir(url)
	got2 := runner.ReposCacheDir(url)
	if got1 != got2 {
		t.Errorf("reposCacheDir not deterministic: %q != %q", got1, got2)
	}
}

func TestReposCacheDir_DifferentURLs_DifferentPaths(t *testing.T) {
	url1 := "https://github.com/example/repo-a.git"
	url2 := "https://github.com/example/repo-b.git"
	p1 := runner.ReposCacheDir(url1)
	p2 := runner.ReposCacheDir(url2)
	if p1 == p2 {
		t.Errorf("different URLs should produce different cache dirs, both got %q", p1)
	}
}

func TestReposCacheDir_UnderHomeWfRunner(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set")
	}
	got := runner.ReposCacheDir("https://github.com/example/repo.git")
	expected := filepath.Join(home, ".wf-runner", "repos")
	if !strings.HasPrefix(got, expected) {
		t.Errorf("cache dir %q does not start with %q", got, expected)
	}
}

// ── worktreePath ──────────────────────────────────────────────────────────────

func TestWorktreePath_UnderHomeWfRunner(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set")
	}
	runID := "test-run-abc123"
	got := runner.WorktreePath(runID)
	expected := filepath.Join(home, ".wf-runner", "worktrees", runID)
	if got != expected {
		t.Errorf("worktreePath = %q, want %q", got, expected)
	}
}

func TestWorktreePath_Deterministic(t *testing.T) {
	runID := "my-run-id"
	p1 := runner.WorktreePath(runID)
	p2 := runner.WorktreePath(runID)
	if p1 != p2 {
		t.Errorf("worktreePath not deterministic: %q != %q", p1, p2)
	}
}

func TestWorktreePath_DifferentRunIDs_DifferentPaths(t *testing.T) {
	p1 := runner.WorktreePath("run-1")
	p2 := runner.WorktreePath("run-2")
	if p1 == p2 {
		t.Errorf("different run IDs should produce different worktree paths, both got %q", p1)
	}
}

// ── EnsureWorktree / RemoveWorktree (integration, requires git) ───────────────

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func TestEnsureWorktree_LocalRepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}

	// Create a local bare-friendly source repo with at least one commit.
	srcDir := t.TempDir()
	cmds := [][]string{
		{"git", "-C", srcDir, "init"},
		{"git", "-C", srcDir, "config", "user.email", "test@test.com"},
		{"git", "-C", srcDir, "config", "user.name", "Test"},
		{"git", "-C", srcDir, "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup git repo: %v\n%s", err, out)
		}
	}

	runID := "wt-test-run-" + t.Name()

	// Redirect HOME so cache and worktree dirs land in a temp dir, not the real HOME.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	wtPath, err := runner.EnsureWorktree(srcDir, "HEAD", runID)
	if err != nil {
		t.Fatalf("EnsureWorktree failed: %v", err)
	}

	// Worktree directory should exist.
	if _, statErr := os.Stat(wtPath); statErr != nil {
		t.Errorf("worktree path %q does not exist: %v", wtPath, statErr)
	}

	// Cleanup should remove the directory.
	runner.CleanupWorktree(srcDir, runID)
	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Errorf("worktree path %q should have been removed after cleanup", wtPath)
	}
}

func TestEnsureWorktree_IdempotentStaleDir(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}

	srcDir := t.TempDir()
	cmds := [][]string{
		{"git", "-C", srcDir, "init"},
		{"git", "-C", srcDir, "config", "user.email", "test@test.com"},
		{"git", "-C", srcDir, "config", "user.name", "Test"},
		{"git", "-C", srcDir, "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("setup git repo: %v\n%s", err, out)
		}
	}

	runID := "wt-idempotent-" + t.Name()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// First call — creates the worktree.
	wtPath1, err := runner.EnsureWorktree(srcDir, "HEAD", runID)
	if err != nil {
		t.Fatalf("first EnsureWorktree failed: %v", err)
	}

	// Second call with same runID — should remove stale and recreate cleanly.
	wtPath2, err := runner.EnsureWorktree(srcDir, "HEAD", runID)
	if err != nil {
		t.Fatalf("second EnsureWorktree failed: %v", err)
	}

	if wtPath1 != wtPath2 {
		t.Errorf("worktree paths differ: %q vs %q", wtPath1, wtPath2)
	}

	runner.CleanupWorktree(srcDir, runID)
}
