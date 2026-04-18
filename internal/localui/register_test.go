//go:build !nolocalui

package localui_test

import (
	"encoding/json"
	"io"
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
		if r.URL.Path != "/api/runner/register" {
			http.NotFound(w, r)
			return
		}
		// Verify the request body shape
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		_ = json.Unmarshal(body, &req)
		if req["code"] == "" {
			t.Errorf("request body missing 'code' field: %s", body)
		}
		if req["name"] == "" {
			t.Errorf("request body missing 'name' field: %s", body)
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"uid":            "runner-abc-123",
			"token":          "tok_super_secret",
			"orgUid":         "org-xyz-789",
			"name":           "my-runner",
			"environmentUid": "env-456",
		})
	}))
	defer srv.Close()

	result, err := localui.RegisterAPI(srv.URL, "RNR-fakecode", "my-runner")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RunnerUID != "runner-abc-123" {
		t.Errorf("RunnerUID = %q, want %q", result.RunnerUID, "runner-abc-123")
	}
	if result.Token != "tok_super_secret" {
		t.Errorf("Token = %q, want %q", result.Token, "tok_super_secret")
	}
	if result.RunnerName != "my-runner" {
		t.Errorf("RunnerName = %q, want %q", result.RunnerName, "my-runner")
	}
	if result.OrgUID != "org-xyz-789" {
		t.Errorf("OrgUID = %q, want %q", result.OrgUID, "org-xyz-789")
	}
	if result.EnvironmentUID != "env-456" {
		t.Errorf("EnvironmentUID = %q, want %q", result.EnvironmentUID, "env-456")
	}
	if result.APIURL != srv.URL {
		t.Errorf("APIURL = %q, want %q", result.APIURL, srv.URL)
	}
}

func TestCallRegisterAPI_FriendlyErrorFromServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid or expired registration code"})
	}))
	defer srv.Close()

	_, err := localui.RegisterAPI(srv.URL, "RNR-bad", "runner")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "expired") && !strings.Contains(err.Error(), "Invalid") {
		t.Errorf("error should surface server message, got: %v", err)
	}
}

func TestCallRegisterAPI_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := localui.RegisterAPI(srv.URL, "RNR-x", "runner")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestCallRegisterAPI_ConnectionRefused(t *testing.T) {
	_, err := localui.RegisterAPI("http://localhost:19999", "RNR-x", "runner")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "connection") {
		t.Errorf("error should mention connection failure, got: %v", err)
	}
}

func TestCallRegisterAPI_TrailingSlashStripped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the path is exactly /api/runner/register (no double slash).
		if r.URL.Path != "/api/runner/register" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"uid": "x", "token": "y"})
	}))
	defer srv.Close()

	// API URL with trailing slash — should still work.
	_, err := localui.RegisterAPI(srv.URL+"/", "RNR-x", "runner")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCallRegisterAPI_FallsBackToBodyName(t *testing.T) {
	// When the server doesn't echo back a name, the runner keeps the one it sent.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"uid": "x", "token": "y"})
	}))
	defer srv.Close()

	r, err := localui.RegisterAPI(srv.URL, "RNR-x", "supplied-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.RunnerName != "supplied-name" {
		t.Errorf("RunnerName = %q, want %q", r.RunnerName, "supplied-name")
	}
}

// ── writeCredentials ─────────────────────────────────────────────────────────

func TestWriteCredentials_ContentsCorrect(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.env")

	r := &localui.RegistrationResult{
		RunnerUID:  "id-001",
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

	r := &localui.RegistrationResult{Token: "tok", RunnerUID: "id", RunnerName: "n", APIURL: "http://x"}
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

	r := &localui.RegistrationResult{Token: "t", RunnerUID: "i", RunnerName: "n", APIURL: "u"}
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

	r := &localui.RegistrationResult{Token: "t", RunnerUID: "i", RunnerName: "n", APIURL: "u"}
	_ = localui.WriteCredentials(credPath, r)

	data, _ := os.ReadFile(credPath)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && !strings.HasPrefix(line, "export ") {
			t.Errorf("every line should start with 'export ', got: %q", line)
		}
	}
}
