package api_test

import (
	"context"
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
