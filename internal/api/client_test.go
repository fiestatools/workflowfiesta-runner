package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"

	"workflowfiesta-runner/internal/api"
)

func TestClientConnect(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error: %v", err)
			return
		}
		defer conn.Close()
		// Echo messages back
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			conn.WriteMessage(mt, msg)
		}
	}))
	defer server.Close()

	// Replace http:// with ws:// for the test
	wsURL := "ws" + server.URL[4:]
	client := api.New(wsURL, "test-token")

	err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	if err := client.SendHeartbeat(); err != nil {
		t.Errorf("SendHeartbeat failed: %v", err)
	}
}

func TestClientReportJobComplete(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	received := make(chan []byte, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()
		_, msg, _ := conn.ReadMessage()
		received <- msg
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[4:]
	client := api.New(wsURL, "test-token")
	client.Connect(context.Background())
	defer client.Close()

	client.ReportJobComplete("job-123", 0, "hello output")
	msg := <-received

	if string(msg) == "" {
		t.Error("expected message")
	}
}

func TestClient_ReportApprovalPending_SendsCorrectMessage(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	received := make(chan []byte, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error: %v", err)
			return
		}
		defer conn.Close()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		received <- msg
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[4:]
	client := api.New(wsURL, "test-token")
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	if err := client.ReportApprovalPending("job-pending-1", "my-runner"); err != nil {
		t.Fatalf("ReportApprovalPending failed: %v", err)
	}

	msg := <-received
	var parsed map[string]interface{}
	if err := json.Unmarshal(msg, &parsed); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}
	if parsed["type"] != "job:approval_pending" {
		t.Errorf("expected type=job:approval_pending, got %q", parsed["type"])
	}
	if parsed["jobId"] != "job-pending-1" {
		t.Errorf("expected jobId=job-pending-1, got %q", parsed["jobId"])
	}
	if parsed["runnerName"] != "my-runner" {
		t.Errorf("expected runnerName=my-runner, got %q", parsed["runnerName"])
	}
}

func TestClient_ReportApprovalResolved_SendsCorrectMessage(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	received := make(chan []byte, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error: %v", err)
			return
		}
		defer conn.Close()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		received <- msg
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[4:]
	client := api.New(wsURL, "test-token")
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	if err := client.ReportApprovalResolved("job-resolved-1", true); err != nil {
		t.Fatalf("ReportApprovalResolved failed: %v", err)
	}

	msg := <-received
	var parsed map[string]interface{}
	if err := json.Unmarshal(msg, &parsed); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}
	if parsed["type"] != "job:approval_resolved" {
		t.Errorf("expected type=job:approval_resolved, got %q", parsed["type"])
	}
	if parsed["jobId"] != "job-resolved-1" {
		t.Errorf("expected jobId=job-resolved-1, got %q", parsed["jobId"])
	}
	if approved, ok := parsed["approved"].(bool); !ok || !approved {
		t.Errorf("expected approved=true, got %v", parsed["approved"])
	}
}

func TestClient_ReportApprovalResolved_ApprovedFalse(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	received := make(chan []byte, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		received <- msg
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[4:]
	client := api.New(wsURL, "test-token")
	client.Connect(context.Background())
	defer client.Close()

	client.ReportApprovalResolved("job-denied-1", false)

	msg := <-received
	var parsed map[string]interface{}
	json.Unmarshal(msg, &parsed)

	if approved, ok := parsed["approved"].(bool); !ok || approved {
		t.Errorf("expected approved=false, got %v", parsed["approved"])
	}
}
