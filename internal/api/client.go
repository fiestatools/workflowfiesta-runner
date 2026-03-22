package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

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
	apiURL     string
	token      string
	httpClient *http.Client
}

func New(apiURL, token string) *Client {
	return &Client{
		apiURL: apiURL,
		token:  token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) do(method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.apiURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

func (c *Client) post(path string, body interface{}) error {
	resp, err := c.do("POST", path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, path)
	}
	return nil
}

// PollNextJob claims the next pending job for this runner.
// Returns nil, nil when there is no pending job.
func (c *Client) PollNextJob() (*Job, error) {
	resp, err := c.do("GET", "/api/runner/jobs/next", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 204 {
		return nil, nil // no pending job
	}
	if resp.StatusCode == 401 {
		log.Fatal("[runner] authentication failed — check your token and re-register the runner")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("poll failed: HTTP %d", resp.StatusCode)
	}
	var job Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, fmt.Errorf("decode job: %w", err)
	}
	return &job, nil
}

// SendHeartbeat updates last_seen and reports the runner's current status.
func (c *Client) SendHeartbeat(status string) error {
	return c.post("/api/runner/heartbeat", map[string]string{"status": status})
}

// StreamOutput sends a streaming output chunk to the API for broadcast to the UI.
func (c *Client) StreamOutput(jobID, chunk string) error {
	return c.post("/api/runner/jobs/"+jobID+"/output", map[string]string{"chunk": chunk})
}

// ReportJobComplete marks the job completed.
func (c *Client) ReportJobComplete(jobID string, exitCode int, output string) error {
	return c.post("/api/runner/jobs/"+jobID+"/complete", map[string]interface{}{
		"exitCode": exitCode,
		"output":   output,
	})
}

// ReportJobFailed marks the job failed.
func (c *Client) ReportJobFailed(jobID, errMsg string) error {
	return c.post("/api/runner/jobs/"+jobID+"/fail", map[string]string{"error": errMsg})
}

// ReportApprovalPending notifies the UI that a script is awaiting approval.
func (c *Client) ReportApprovalPending(jobID, runnerName string) error {
	return c.post("/api/runner/jobs/"+jobID+"/approval-pending", map[string]string{"runnerName": runnerName})
}

// ReportApprovalResolved notifies the UI that approval was resolved.
func (c *Client) ReportApprovalResolved(jobID string, approved bool) error {
	return c.post("/api/runner/jobs/"+jobID+"/approval-resolved", map[string]interface{}{"approved": approved})
}
