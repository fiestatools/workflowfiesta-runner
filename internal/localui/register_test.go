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
		// Verify the request body shape.
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		_ = json.Unmarshal(body, &req)
		if req["code"] == "" {
			t.Errorf("request body missing 'code' field: %s", body)
		}
		if _, ok := req["documentsPath"]; !ok {
			t.Errorf("request body missing 'documentsPath' field: %s", body)
		}
		if _, ok := req["shell"]; !ok {
			t.Errorf("request body missing 'shell' field: %s", body)
		}
		if _, ok := req["name"]; ok {
			t.Errorf("request body should not include 'name' (server holds the name): %s", body)
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"uid":            "runner-abc-123",
			"token":          "tok_super_secret",
			"orgUid":         "org-xyz-789",
			"name":           "my-laptop",
			"environmentUid": "env-456",
		})
	}))
	defer srv.Close()

	result, err := localui.RegisterAPI(srv.URL, "RNR-fakecode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RunnerUID != "runner-abc-123" {
		t.Errorf("RunnerUID = %q, want %q", result.RunnerUID, "runner-abc-123")
	}
	if result.Token != "tok_super_secret" {
		t.Errorf("Token = %q, want %q", result.Token, "tok_super_secret")
	}
	if result.RunnerName != "my-laptop" {
		t.Errorf("RunnerName = %q, want %q", result.RunnerName, "my-laptop")
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

	_, err := localui.RegisterAPI(srv.URL, "RNR-bad")
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

	_, err := localui.RegisterAPI(srv.URL, "RNR-x")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestCallRegisterAPI_ConnectionRefused(t *testing.T) {
	_, err := localui.RegisterAPI("http://localhost:19999", "RNR-x")
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
	_, err := localui.RegisterAPI(srv.URL+"/", "RNR-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

func TestFrontendURLFrom(t *testing.T) {
	tests := []struct {
		name   string
		apiURL string
		want   string
	}{
		{"prod api domain", "https://api.workflowfiesta.com", "https://app.workflowfiesta.com"},
		{"staging api domain", "https://staging.api.workflowfiesta.com", "https://staging.app.workflowfiesta.com"},
		{"prod app domain unchanged", "https://app.workflowfiesta.com", "https://app.workflowfiesta.com"},
		{"staging app domain unchanged", "https://staging.app.workflowfiesta.com", "https://staging.app.workflowfiesta.com"},
		{"self-hosted unchanged", "https://my-instance.example.com", "https://my-instance.example.com"},
		{"localhost backend to frontend", "http://localhost:5000", "http://localhost:3000"},
		{"127.0.0.1 backend to frontend", "http://127.0.0.1:5000", "http://127.0.0.1:3000"},
		{"localhost other port unchanged", "http://localhost:8080", "http://localhost:8080"},
		{"trailing slash stripped", "https://api.workflowfiesta.com/", "https://app.workflowfiesta.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := localui.FrontendURLFrom(tt.apiURL)
			if got != tt.want {
				t.Errorf("FrontendURLFrom(%q) = %q, want %q", tt.apiURL, got, tt.want)
			}
		})
	}
}
