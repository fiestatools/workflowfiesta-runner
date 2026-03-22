package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"workflowfiesta-runner/internal/api"
)

func TestClient_Heartbeat(t *testing.T) {
	var received map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/runner/heartbeat" || r.Method != "POST" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := api.New(server.URL, "test-token")
	if err := client.SendHeartbeat("idle"); err != nil {
		t.Fatalf("SendHeartbeat failed: %v", err)
	}
	if received["status"] != "idle" {
		t.Errorf("expected status=idle, got %q", received["status"])
	}
}

func TestClient_PollNextJob_NoJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer server.Close()

	client := api.New(server.URL, "test-token")
	job, err := client.PollNextJob()
	if err != nil {
		t.Fatalf("PollNextJob failed: %v", err)
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

	client := api.New(server.URL, "test-token")
	job, err := client.PollNextJob()
	if err != nil {
		t.Fatalf("PollNextJob failed: %v", err)
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

func TestClient_ReportJobComplete(t *testing.T) {
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &body)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := api.New(server.URL, "test-token")
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

func TestClient_ReportApprovalPending(t *testing.T) {
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &body)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := api.New(server.URL, "test-token")
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

	client := api.New(server.URL, "test-token")
	if err := client.ReportApprovalResolved("job-resolved-1", true); err != nil {
		t.Fatalf("ReportApprovalResolved failed: %v", err)
	}
	if approved, ok := body["approved"].(bool); !ok || !approved {
		t.Errorf("expected approved=true, got %v", body["approved"])
	}
}
