package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workflowfiesta-runner/internal/api"
)

func newTestClient(srv *httptest.Server) *api.Client {
	return api.New(srv.URL, "test-token")
}

// ── PollNextJob ───────────────────────────────────────────────────────────────

func TestClient_PollNextJob_NoJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer server.Close()

	client := newTestClient(server)
	job, status, err := client.PollNextJob()
	if err != nil {
		t.Fatalf("PollNextJob failed: %v", err)
	}
	if status != 204 {
		t.Errorf("expected status 204, got %d", status)
	}
	if job != nil {
		t.Errorf("expected nil job, got %+v", job)
	}
}

func TestClient_PollNextJob_WithJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jobId":          "job-123",
			"dockerImage":    "ubuntu:latest",
			"script":         "echo hello",
			"envVars":        map[string]string{"FOO": "bar"},
			"timeoutSeconds": 60,
		})
	}))
	defer server.Close()

	client := newTestClient(server)
	job, status, err := client.PollNextJob()
	if err != nil {
		t.Fatalf("PollNextJob failed: %v", err)
	}
	if status != 200 {
		t.Errorf("expected status 200, got %d", status)
	}
	if job == nil {
		t.Fatal("expected a job, got nil")
	}
	if job.JobID != "job-123" {
		t.Errorf("expected jobId=job-123, got %q", job.JobID)
	}
	if job.DockerImage != "ubuntu:latest" {
		t.Errorf("expected dockerImage=ubuntu:latest, got %q", job.DockerImage)
	}
}

// ── SendHeartbeat ─────────────────────────────────────────────────────────────

func TestSendHeartbeat_SendsCapabilities(t *testing.T) {
	var captured map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true,"orgId":""}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	_, status, err := client.SendHeartbeat("idle", []string{"tool_dispatch", "script_library"}, "linux", "amd64", "v0.7.0")
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 {
		t.Errorf("expected HTTP 200, got %d", status)
	}

	caps, ok := captured["capabilities"].([]interface{})
	if !ok {
		t.Fatalf("capabilities not in request body: %v", captured)
	}
	if len(caps) != 2 {
		t.Errorf("expected 2 capabilities, got %d: %v", len(caps), caps)
	}
	if captured["status"] != "idle" {
		t.Errorf("expected status=idle, got %v", captured["status"])
	}
}

func TestSendHeartbeat_SendsOSArch(t *testing.T) {
	var captured map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"orgId":""}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	_, _, err := client.SendHeartbeat("busy", []string{}, "windows", "amd64", "v0.7.0")
	if err != nil {
		t.Fatal(err)
	}
	if captured["os"] != "windows" {
		t.Errorf("expected os=windows, got %v", captured["os"])
	}
	if captured["arch"] != "amd64" {
		t.Errorf("expected arch=amd64, got %v", captured["arch"])
	}
	if captured["version"] != "v0.7.0" {
		t.Errorf("expected version=v0.7.0, got %v", captured["version"])
	}
}

func TestSendHeartbeat_ReturnsOrgID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"orgId":"org-abc"}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	orgID, status, err := client.SendHeartbeat("idle", []string{}, "linux", "amd64", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 {
		t.Errorf("expected HTTP 200, got %d", status)
	}
	if orgID != "org-abc" {
		t.Errorf("expected orgId=org-abc, got %q", orgID)
	}
}

func TestSendHeartbeat_HTTP400_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
	}))
	defer server.Close()

	client := newTestClient(server)
	_, status, err := client.SendHeartbeat("idle", nil, "", "", "")
	if err == nil {
		t.Error("expected error for HTTP 400, got nil")
	}
	if status != 400 {
		t.Errorf("expected HTTP 400, got %d", status)
	}
}

// ── ReportJobComplete ─────────────────────────────────────────────────────────

func TestClient_ReportJobComplete(t *testing.T) {
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &body)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	if err := client.ReportJobComplete("job-123", 0, "hello output"); err != nil {
		t.Fatalf("ReportJobComplete failed: %v", err)
	}
	if body["exitCode"].(float64) != 0 {
		t.Errorf("expected exitCode=0")
	}
	if body["output"] != "hello output" {
		t.Errorf("expected output=hello output, got %q", body["output"])
	}
}

// ── Approval endpoints ────────────────────────────────────────────────────────

func TestClient_ReportApprovalPending(t *testing.T) {
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &body)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	if err := client.ReportApprovalPending("job-pending-1", "my-runner"); err != nil {
		t.Fatalf("ReportApprovalPending failed: %v", err)
	}
	if body["runnerName"] != "my-runner" {
		t.Errorf("expected runnerName=my-runner, got %q", body["runnerName"])
	}
}

func TestClient_ReportApprovalResolved(t *testing.T) {
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &body)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	if err := client.ReportApprovalResolved("job-resolved-1", true); err != nil {
		t.Fatalf("ReportApprovalResolved failed: %v", err)
	}
	if approved, ok := body["approved"].(bool); !ok || !approved {
		t.Errorf("expected approved=true, got %v", body["approved"])
	}
}

// ── ListServerScripts ─────────────────────────────────────────────────────────

func TestListServerScripts_Basic(t *testing.T) {
	scripts := []map[string]interface{}{
		{"name": "deploy.sh", "description": "deploys", "tags": []string{"infra"}, "updated_at": "2026-01-01T00:00:00Z"},
		{"name": "build.sh", "description": "builds", "tags": []string{}, "updated_at": "2026-01-02T00:00:00Z"},
	}
	data, _ := json.Marshal(scripts)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	client := newTestClient(server)
	metas, err := client.ListServerScripts()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Errorf("expected 2 scripts, got %d", len(metas))
	}
	if metas[0].Name != "deploy.sh" {
		t.Errorf("expected deploy.sh first, got %q", metas[0].Name)
	}
}

// ── PushScript ────────────────────────────────────────────────────────────────

func TestPushScript_Basic(t *testing.T) {
	var captured map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/api/runner/scripts") {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	client := newTestClient(server)
	err := client.PushScript("test.sh", "echo test", "runs tests", []string{"test"})
	if err != nil {
		t.Fatal(err)
	}

	if captured["name"] != "test.sh" {
		t.Errorf("name mismatch: got %v", captured["name"])
	}
	if captured["content"] != "echo test" {
		t.Errorf("content mismatch: got %v", captured["content"])
	}
	if captured["description"] != "runs tests" {
		t.Errorf("description mismatch: got %v", captured["description"])
	}
	tags, _ := captured["tags"].([]interface{})
	if len(tags) != 1 || tags[0] != "test" {
		t.Errorf("tags mismatch: got %v", captured["tags"])
	}
}

// ── GetScript ─────────────────────────────────────────────────────────────────

func TestGetScript_Basic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":"echo hello","name":"hello.sh"}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	content, err := client.GetScript("hello.sh")
	if err != nil {
		t.Fatal(err)
	}
	if content != "echo hello" {
		t.Errorf("expected 'echo hello', got %q", content)
	}
}

func TestGetScript_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":"Not found"}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	_, err := client.GetScript("missing.sh")
	if err == nil {
		t.Error("expected error for 404 response, got nil")
	}
}
