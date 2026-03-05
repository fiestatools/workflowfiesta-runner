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

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

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
		log.Warnf("Connection failed (attempt %d): %v, retrying in %v", attempt, err, delay)

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
			log.Warnf("WebSocket read error: %v", err)
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
				jobChan <- job
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
	return c.conn.WriteMessage(websocket.TextMessage, data)
}
