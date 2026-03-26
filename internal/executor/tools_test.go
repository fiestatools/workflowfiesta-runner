package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"workflowfiesta-runner/internal/localconfig"
)

// newHandler creates a ToolHandler scoped to a fresh temp directory.
func newHandler(t *testing.T) (*ToolHandler, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := localconfig.Default()
	cfg.AllowedPaths = []string{dir}
	h := NewToolHandler(cfg)
	h.SetOrgID("") // ensure clean org state
	return h, dir
}

func args(kv ...interface{}) json.RawMessage {
	m := make(map[string]interface{}, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	data, _ := json.Marshal(m)
	return data
}

// ── read_file ─────────────────────────────────────────────────────────────────

func TestReadFile_Basic(t *testing.T) {
	h, dir := newHandler(t)
	f := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(f, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := h.Execute("read_file", args("path", f))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1\tline1") {
		t.Errorf("expected line-numbered output, got: %s", out)
	}
	if !strings.Contains(out, "3\tline3") {
		t.Errorf("expected line 3, got: %s", out)
	}
}

func TestReadFile_OffsetLimit(t *testing.T) {
	h, dir := newHandler(t)
	f := filepath.Join(dir, "lines.txt")
	if err := os.WriteFile(f, []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := h.Execute("read_file", args("path", f, "offset", float64(2), "limit", float64(2)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2\tb") {
		t.Errorf("expected line 2, got: %s", out)
	}
	if !strings.Contains(out, "3\tc") {
		t.Errorf("expected line 3, got: %s", out)
	}
	if strings.Contains(out, "1\ta") {
		t.Errorf("offset not respected, line 1 present in: %s", out)
	}
	if strings.Contains(out, "4\td") {
		t.Errorf("limit not respected, line 4 present in: %s", out)
	}
}

// ── write_file ────────────────────────────────────────────────────────────────

func TestWriteFile_Basic(t *testing.T) {
	h, dir := newHandler(t)
	f := filepath.Join(dir, "out.txt")
	out, err := h.Execute("write_file", args("path", f, "content", "hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Written:") {
		t.Errorf("expected Written: in output, got: %s", out)
	}
	data, readErr := os.ReadFile(f)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "hello world" {
		t.Errorf("content mismatch: got %q", string(data))
	}
}

func TestWriteFile_NoAutoIndex(t *testing.T) {
	h, dir := newHandler(t)
	f := filepath.Join(dir, "script.py")
	_, err := h.Execute("write_file", args("path", f, "content", `"""Does something useful."""\nprint("hi")`))
	if err != nil {
		t.Fatal(err)
	}
	// No .meta.json should be created alongside the .py file (write_file ≠ save_local_script)
	metaPath := f + ".meta.json"
	if _, statErr := os.Stat(metaPath); !os.IsNotExist(statErr) {
		t.Errorf("write_file should not create .meta.json, but %s exists", metaPath)
	}
}

// ── edit_file ─────────────────────────────────────────────────────────────────

func TestEditFile_Basic(t *testing.T) {
	h, dir := newHandler(t)
	f := filepath.Join(dir, "editable.txt")
	if err := os.WriteFile(f, []byte("foo bar baz"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := h.Execute("edit_file", args("path", f, "old_string", "bar", "new_string", "BAR"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Edited:") {
		t.Errorf("expected Edited: in output, got: %s", out)
	}
	data, _ := os.ReadFile(f)
	if string(data) != "foo BAR baz" {
		t.Errorf("edit not applied correctly: got %q", string(data))
	}
}

func TestEditFile_NotFound(t *testing.T) {
	h, dir := newHandler(t)
	f := filepath.Join(dir, "nonexistent.txt")
	out, err := h.Execute("edit_file", args("path", f, "old_string", "x", "new_string", "y"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Error") {
		t.Errorf("expected error string for missing file, got: %s", out)
	}
}

// ── list_dir ──────────────────────────────────────────────────────────────────

func TestListDir_Basic(t *testing.T) {
	h, dir := newHandler(t)
	// Create a file and a subdirectory
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := h.Execute("list_dir", args("path", dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "f file.txt") {
		t.Errorf("expected 'f file.txt' in output, got: %s", out)
	}
	if !strings.Contains(out, "d subdir") {
		t.Errorf("expected 'd subdir' in output, got: %s", out)
	}
}

// ── glob_files ────────────────────────────────────────────────────────────────

func TestGlobFiles_Basic(t *testing.T) {
	h, dir := newHandler(t)
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "top.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "other.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := h.Execute("glob_files", args("pattern", "**/*.txt", "cwd", dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "top.txt") {
		t.Errorf("expected top.txt in glob output, got: %s", out)
	}
	if !strings.Contains(out, "nested.txt") {
		t.Errorf("expected nested.txt in glob output, got: %s", out)
	}
	if strings.Contains(out, "other.go") {
		t.Errorf("other.go should not appear in *.txt glob, got: %s", out)
	}
}

// ── save_local_script ─────────────────────────────────────────────────────────

func TestSaveLocalScript_Basic(t *testing.T) {
	h, dir := newHandler(t)
	// Override script lib dir to use our temp dir
	h.SetOrgID("test-org")
	// Create the lib dir path manually so save can use it
	libDir := filepath.Join(dir, ".workflowfiesta-test-scripts")
	// We test via Execute — the library is in ~/.workflowfiesta/scripts/test-org
	// To avoid polluting home dir, use a tmpDir-based org that's unique
	orgID := "testorg-" + t.Name()
	h.SetOrgID(orgID)

	out, err := h.Execute("save_local_script", args(
		"name", "my-script.sh",
		"content", "#!/bin/bash\necho hello",
		"description", "prints hello",
		"tags", []interface{}{"greet"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Saved:") {
		t.Errorf("expected Saved: in output, got: %s", out)
	}

	_ = libDir // cleanup handled by OS after test
	// Verify that loading the script works
	content, loadErr := h.LoadLocalScript("my-script.sh")
	if loadErr != nil {
		t.Fatalf("LoadLocalScript failed: %v", loadErr)
	}
	if !strings.Contains(content, "echo hello") {
		t.Errorf("loaded content mismatch: %q", content)
	}
}

// mockSyncer records PushScript calls for testing.
type mockSyncer struct {
	mu    sync.Mutex
	calls []string
}

func (m *mockSyncer) PushScript(name, _, _ string, _ []string) error {
	m.mu.Lock()
	m.calls = append(m.calls, name)
	m.mu.Unlock()
	return nil
}

func TestSaveLocalScript_FiresSync(t *testing.T) {
	h, _ := newHandler(t)
	h.SetOrgID("synctest-" + t.Name())

	syncer := &mockSyncer{}
	h.SetSyncer(syncer)

	_, err := h.Execute("save_local_script", args(
		"name", "sync-me.sh",
		"content", "#!/bin/bash\necho sync",
		"description", "sync test",
	))
	if err != nil {
		t.Fatal(err)
	}

	// Give the goroutine time to fire
	// The sync is fire-and-forget, so we wait briefly
	for i := 0; i < 50; i++ {
		syncer.mu.Lock()
		n := len(syncer.calls)
		syncer.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	syncer.mu.Lock()
	defer syncer.mu.Unlock()
	found := false
	for _, name := range syncer.calls {
		if name == "sync-me.sh" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("PushScript not called with sync-me.sh; calls: %v", syncer.calls)
	}
}

// ── scriptLibDir path tests ───────────────────────────────────────────────────

func TestScriptLibDir_WithOrgID(t *testing.T) {
	h, _ := newHandler(t)
	h.SetOrgID("org-123")
	dir := h.scriptLibDir()
	if !strings.HasSuffix(dir, "/org-123") {
		t.Errorf("expected dir to end in /org-123, got: %s", dir)
	}
}

func TestScriptLibDir_NoOrgID(t *testing.T) {
	h, _ := newHandler(t)
	// orgID is already "" from newHandler
	dir := h.scriptLibDir()
	if strings.Contains(dir, "/org-") {
		t.Errorf("expected no org suffix when orgID is empty, got: %s", dir)
	}
	if !strings.HasSuffix(dir, "/scripts") {
		t.Errorf("expected dir to end in /scripts, got: %s", dir)
	}
}

// ── LoadLocalScript ───────────────────────────────────────────────────────────

func TestLoadLocalScript_Basic(t *testing.T) {
	h, _ := newHandler(t)
	h.SetOrgID("loadtest-" + t.Name())

	content := "#!/bin/bash\necho loaded"
	_, err := h.Execute("save_local_script", args(
		"name", "load-me.sh",
		"content", content,
	))
	if err != nil {
		t.Fatal(err)
	}

	loaded, loadErr := h.LoadLocalScript("load-me.sh")
	if loadErr != nil {
		t.Fatalf("LoadLocalScript failed: %v", loadErr)
	}
	if loaded != content {
		t.Errorf("content mismatch:\nwant: %q\ngot:  %q", content, loaded)
	}
}
