package localconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault_Values(t *testing.T) {
	cfg := Default()
	if cfg.Confirm != "destructive" {
		t.Errorf("Confirm = %q, want %q", cfg.Confirm, "destructive")
	}
	if cfg.Network != "all" {
		t.Errorf("Network = %q, want %q", cfg.Network, "all")
	}
	if cfg.Sandbox != "none" {
		t.Errorf("Sandbox = %q, want %q", cfg.Sandbox, "none")
	}
	if cfg.MaxTimeout != 120 {
		t.Errorf("MaxTimeout = %d, want 120", cfg.MaxTimeout)
	}
	if cfg.ConfirmTimeout != 60 {
		t.Errorf("ConfirmTimeout = %d, want 60", cfg.ConfirmTimeout)
	}
	if len(cfg.AllowedPaths) == 0 {
		t.Error("AllowedPaths should not be empty")
	}
	if len(cfg.BlockedPatterns) == 0 {
		t.Error("BlockedPatterns should not be empty")
	}
}

func TestLoad_FileNotExist_ReturnsDefaults(t *testing.T) {
	cfg, err := Load("/no/such/path/runner.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if cfg.Confirm != "destructive" {
		t.Errorf("expected default confirm, got %q", cfg.Confirm)
	}
}

func TestLoad_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runner.yaml")
	yaml := `
allowed_paths:
  - ~/projects
  - ~/Documents:ro
confirm: always
network: none
max_timeout: 30
confirm_timeout: 10
sandbox: kernel
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Confirm != "always" {
		t.Errorf("Confirm = %q, want %q", cfg.Confirm, "always")
	}
	if cfg.Network != "none" {
		t.Errorf("Network = %q, want %q", cfg.Network, "none")
	}
	if cfg.MaxTimeout != 30 {
		t.Errorf("MaxTimeout = %d, want 30", cfg.MaxTimeout)
	}
	if cfg.Sandbox != "kernel" {
		t.Errorf("Sandbox = %q, want %q", cfg.Sandbox, "kernel")
	}
	if len(cfg.AllowedPaths) != 2 {
		t.Errorf("AllowedPaths len = %d, want 2", len(cfg.AllowedPaths))
	}
}

func TestLoad_ExpandsTilde(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runner.yaml")
	yaml := "allowed_paths:\n  - ~/projects\naudit_log: ~/audit.log\n"
	os.WriteFile(path, []byte(yaml), 0o600)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	for _, p := range cfg.AllowedPaths {
		if strings.HasPrefix(p, "~") {
			t.Errorf("tilde not expanded: %q", p)
		}
		if !strings.HasPrefix(p, home) {
			t.Errorf("path %q does not start with home %q", p, home)
		}
	}
	if strings.HasPrefix(cfg.AuditLog, "~") {
		t.Errorf("AuditLog tilde not expanded: %q", cfg.AuditLog)
	}
}

func TestLoad_ROSuffixStripped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runner.yaml")
	yaml := "allowed_paths:\n  - ~/Documents:ro\n"
	os.WriteFile(path, []byte(yaml), 0o600)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cfg.AllowedPaths {
		if strings.HasSuffix(p, ":ro") {
			t.Errorf("ExpandedAllowedPaths still has :ro suffix: %q", p)
		}
	}
}

func TestIsPathReadOnly(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg := &LocalConfig{
		AllowedPaths: []string{
			"~/projects",
			"~/Documents:ro",
		},
	}
	docsExpanded := filepath.Join(home, "Documents")
	if !cfg.IsPathReadOnly(docsExpanded) {
		t.Errorf("expected ~/Documents to be read-only")
	}
	projectsExpanded := filepath.Join(home, "projects")
	if cfg.IsPathReadOnly(projectsExpanded) {
		t.Errorf("expected ~/projects NOT to be read-only")
	}
}

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runner.yaml")

	in := Default()
	in.Confirm = "always"
	in.Network = "none"
	in.MaxTimeout = 45

	if err := Save(in, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if out.Confirm != "always" {
		t.Errorf("Confirm = %q after round-trip, want %q", out.Confirm, "always")
	}
	if out.Network != "none" {
		t.Errorf("Network = %q after round-trip", out.Network)
	}
	if out.MaxTimeout != 45 {
		t.Errorf("MaxTimeout = %d after round-trip, want 45", out.MaxTimeout)
	}
}

func TestSave_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "dir", "runner.yaml")
	if err := Save(Default(), nested); err != nil {
		t.Fatalf("Save to nested path: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestSave_FileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runner.yaml")
	if err := Save(Default(), path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestWorkingDir_FirstWritable(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg := &LocalConfig{
		AllowedPaths: []string{
			"~/Documents:ro",
			"~/",
		},
	}
	wd := cfg.WorkingDir()
	// Should skip the :ro path and return home.
	if wd != home {
		t.Errorf("WorkingDir = %q, want %q", wd, home)
	}
}

func TestWorkingDir_FallbackHome(t *testing.T) {
	cfg := &LocalConfig{AllowedPaths: []string{"/no/such/path/ever"}}
	wd := cfg.WorkingDir()
	home, _ := os.UserHomeDir()
	if wd != home {
		t.Errorf("WorkingDir fallback = %q, want home %q", wd, home)
	}
}

func TestExpandedAllowedPaths(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg := &LocalConfig{
		AllowedPaths: []string{"~/projects", "~/Documents:ro"},
	}
	paths := cfg.ExpandedAllowedPaths()
	for _, p := range paths {
		if strings.HasPrefix(p, "~") {
			t.Errorf("unexpanded tilde in %q", p)
		}
		if strings.HasSuffix(p, ":ro") {
			t.Errorf(":ro suffix still present in %q", p)
		}
		if !strings.HasPrefix(p, home) {
			t.Errorf("path %q does not start with home %q", p, home)
		}
	}
}

func TestExpandTilde_NoTilde(t *testing.T) {
	path := "/absolute/path"
	if got := expandTilde(path); got != path {
		t.Errorf("expandTilde(%q) = %q, want unchanged", path, got)
	}
}

func TestExpandTilde_TildeSlash(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := expandTilde("~/foo")
	want := home + "/foo"
	if got != want {
		t.Errorf("expandTilde(~/foo) = %q, want %q", got, want)
	}
}

func TestExpandTilde_TildeOnly(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := expandTilde("~")
	if got != home {
		t.Errorf("expandTilde(~) = %q, want %q", got, home)
	}
}

func TestDefaultPath_ContainsWorkflowfiesta(t *testing.T) {
	p := DefaultPath()
	if !strings.Contains(p, ".workflowfiesta") {
		t.Errorf("DefaultPath = %q should contain .workflowfiesta", p)
	}
	if !strings.HasSuffix(p, "runner.yaml") {
		t.Errorf("DefaultPath = %q should end with runner.yaml", p)
	}
}
