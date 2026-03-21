package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

const (
	// pongWait is how long we wait for a pong before considering the connection dead.
	pongWait = 75 * time.Second
	// writeWait is the timeout for a single WebSocket write.
	writeWait = 10 * time.Second
)

type Job struct {
	JobID          string            `json:"jobId"`
	DockerImage    string            `json:"dockerImage"`
	Script         string            `json:"script"`
	EnvVars        map[string]string `json:"envVars"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
}

type Client struct {
	apiURL string
	token  string

	mu   sync.Mutex
	conn *websocket.Conn
}

func New(apiURL, token string) *Client {
	return &Client{apiURL: apiURL, token: token}
}

func (c *Client) Connect(ctx context.Context) error {
	wsURL := strings.Replace(c.apiURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL += "/runner-ws"

	// Add token as query param as fallback
	u, _ := url.Parse(wsURL)
	q := u.Query()
	q.Set("token", c.token)
	u.RawQuery = q.Encode()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.token)

	log.Infof("[ws] connecting to %s", wsURL)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	log.Infof("[ws] connection established")

	// Set initial read deadline and a pong handler that refreshes it.
	// This lets us detect silent TCP drops: if no pong arrives within pongWait
	// after a ping, ReadMessage() will return a deadline-exceeded error and
	// the Listen() loop will reconnect.
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	return nil
}

func (c *Client) ConnectWithRetry(ctx context.Context) {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := c.Connect(ctx)
		if err == nil {
			log.Info("Connected to WorkflowFiesta API")
			return
		}

		attempt++
		delay := time.Duration(math.Min(float64(attempt)*2, 30)) * time.Second
		log.Warnf("[ws] connection failed (attempt %d): %v, retrying in %v", attempt, err, delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (c *Client) SendHeartbeat() error {
	return c.send(map[string]string{"type": "heartbeat"})
}

// SendPing sends a WebSocket-level PING frame. The server (ws npm library)
// automatically responds with a PONG, which resets our read deadline via
// the pong handler set in Connect(). Call this alongside SendHeartbeat to
// detect silent TCP drops quickly.
func (c *Client) SendPing() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}
	return c.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait))
}

func (c *Client) ReportJobClaimed(jobID string) error {
	return c.send(map[string]string{"type": "job:claimed", "jobId": jobID})
}

func (c *Client) StreamOutput(jobID, chunk string) error {
	return c.send(map[string]string{"type": "job:output", "jobId": jobID, "chunk": chunk})
}

func (c *Client) ReportJobComplete(jobID string, exitCode int, output string) error {
	return c.send(map[string]interface{}{
		"type":     "job:complete",
		"jobId":    jobID,
		"exitCode": exitCode,
		"output":   output,
	})
}

func (c *Client) ReportJobFailed(jobID, errMsg string) error {
	return c.send(map[string]string{"type": "job:failed", "jobId": jobID, "error": errMsg})
}

func (c *Client) ReportApprovalPending(jobID, runnerName string) error {
	return c.send(map[string]string{"type": "job:approval_pending", "jobId": jobID, "runnerName": runnerName})
}

func (c *Client) ReportApprovalResolved(jobID string, approved bool) error {
	return c.send(map[string]interface{}{"type": "job:approval_resolved", "jobId": jobID, "approved": approved})
}

func (c *Client) Listen(ctx context.Context, jobChan chan<- Job) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			if closeErr, ok := err.(*websocket.CloseError); ok && (closeErr.Code == 4001 || closeErr.Code == 4003) {
				log.Fatalf("[ws] authentication failed (code %d: %s). Check your WORKFLOWFIESTA_TOKEN and re-register the runner.", closeErr.Code, closeErr.Text)
			}
			log.Warnf("[ws] read error (will reconnect): %v", err)
			c.mu.Lock()
			c.conn = nil
			c.mu.Unlock()
			// Reconnect
			c.ConnectWithRetry(ctx)
			continue
		}

		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(msg, &envelope); err != nil {
			continue
		}

		var msgType string
		if err := json.Unmarshal(envelope["type"], &msgType); err != nil {
			continue
		}

		if msgType == "job" {
			var job Job
			if err := json.Unmarshal(msg, &job); err == nil {
				// Non-blocking send: if the job buffer is full, prefer dropping
				// the duplicate dispatch over freezing the WebSocket read loop
				// (which would cause missed pong frames → connection closure).
				select {
				case jobChan <- job:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) send(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		// Nil out the connection so Listen() detects the failure on its next
		// ReadMessage() deadline expiry and triggers a reconnect.
		c.conn = nil
		return err
	}
	return nil
}
