//go:build !nolocalui

package localui_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workflowfiesta-runner/internal/localui"
)

// ── callRegisterAPI ───────────────────────────────────────────────────────────

func TestCallRegisterAPI_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/runners/register" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"id":    "runner-abc-123",
			"token": "tok_super_secret",
		})
	}))
	defer srv.Close()

	result, err := localui.RegisterAPI(srv.URL, "my-runner", "org-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RunnerID != "runner-abc-123" {
		t.Errorf("RunnerID = %q, want %q", result.RunnerID, "runner-abc-123")
	}
	if result.Token != "tok_super_secret" {
		t.Errorf("Token = %q, want %q", result.Token, "tok_super_secret")
	}
	if result.RunnerName != "my-runner" {
		t.Errorf("RunnerName = %q, want %q", result.RunnerName, "my-runner")
	}
	if result.APIURL != srv.URL {
		t.Errorf("APIURL = %q, want %q", result.APIURL, srv.URL)
	}
}

func TestCallRegisterAPI_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "organization not found", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := localui.RegisterAPI(srv.URL, "runner", "bad-org")
	if err == nil {
		t.Fatal("expected error for non-201 response")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status code, got: %v", err)
	}
}

func TestCallRegisterAPI_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := localui.RegisterAPI(srv.URL, "runner", "org")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestCallRegisterAPI_ConnectionRefused(t *testing.T) {
	_, err := localui.RegisterAPI("http://localhost:19999", "runner", "org")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "connection") {
		t.Errorf("error should mention connection failure, got: %v", err)
	}
}

func TestCallRegisterAPI_TrailingSlashStripped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the path is exactly /api/runners/register (no double slash).
		if r.URL.Path != "/api/runners/register" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": "x", "token": "y"})
	}))
	defer srv.Close()

	// API URL with trailing slash — should still work.
	_, err := localui.RegisterAPI(srv.URL+"/", "runner", "org")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── writeCredentials ─────────────────────────────────────────────────────────

func TestWriteCredentials_ContentsCorrect(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.env")

	r := &localui.RegistrationResult{
		RunnerID:   "id-001",
		Token:      "tok_abc",
		RunnerName: "my-runner",
		APIURL:     "http://localhost:3001",
	}
	if err := localui.WriteCredentials(credPath, r); err != nil {
		t.Fatalf("WriteCredentials: %v", err)
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("read credentials file: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"WORKFLOWFIESTA_API_URL=http://localhost:3001",
		"WORKFLOWFIESTA_TOKEN=tok_abc",
		"WORKFLOWFIESTA_RUNNER_ID=id-001",
		"WORKFLOWFIESTA_RUNNER_NAME=my-runner",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("credentials file should contain %q\ngot:\n%s", want, content)
		}
	}
}

func TestWriteCredentials_FileMode(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.env")

	r := &localui.RegistrationResult{Token: "tok", RunnerID: "id", RunnerName: "n", APIURL: "http://x"}
	if err := localui.WriteCredentials(credPath, r); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(credPath)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("credentials file mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestWriteCredentials_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "sub", "credentials.env")

	r := &localui.RegistrationResult{Token: "t", RunnerID: "i", RunnerName: "n", APIURL: "u"}
	if err := localui.WriteCredentials(credPath, r); err != nil {
		t.Fatalf("WriteCredentials with nested path: %v", err)
	}
	if _, err := os.Stat(credPath); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestWriteCredentials_HasExportPrefix(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.env")

	r := &localui.RegistrationResult{Token: "t", RunnerID: "i", RunnerName: "n", APIURL: "u"}
	localui.WriteCredentials(credPath, r)

	data, _ := os.ReadFile(credPath)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && !strings.HasPrefix(line, "export ") {
			t.Errorf("every line should start with 'export ', got: %q", line)
		}
	}
}
