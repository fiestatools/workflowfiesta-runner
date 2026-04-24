package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Job struct {
	JobID          string                 `json:"jobId"`
	DockerImage    string                 `json:"dockerImage"`
	Script         string                 `json:"script"`
	EnvVars        map[string]string      `json:"envVars"`
	TimeoutSeconds int                    `json:"timeoutSeconds"`
	ToolName       string                 `json:"toolName,omitempty"`
	ToolArgs       map[string]interface{} `json:"toolArgs,omitempty"`
	GitRepoURL     string                 `json:"git_repo_url,omitempty"`
	GitRef         string                 `json:"git_ref,omitempty"`
}

type Client struct {
	apiURL     string
	token      string
	orgID      string
	mu         sync.Mutex
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

// SetOrgID updates the org ID sent on every subsequent request as X-Org-Id.
// Safe to call concurrently.
func (c *Client) SetOrgID(orgID string) {
	c.mu.Lock()
	c.orgID = orgID
	c.mu.Unlock()
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
	c.mu.Lock()
	orgID := c.orgID
	c.mu.Unlock()
	if orgID != "" {
		req.Header.Set("X-Org-Id", orgID)
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
// On success with no work, returns nil, 204, nil.
// httpStatus is 0 when err is non-nil before any response (e.g. connection error).
func (c *Client) PollNextJob() (*Job, int, error) {
	resp, err := c.do("GET", "/api/runner/jobs/next", nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	status := resp.StatusCode
	if status == 204 {
		return nil, 204, nil // no pending job
	}
	if status == 401 {
		log.Fatal("[runner] authentication failed — check your token and re-register the runner")
	}
	if status >= 400 {
		b, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(b))
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		if msg == "" {
			return nil, status, fmt.Errorf("poll failed: HTTP %d", status)
		}
		return nil, status, fmt.Errorf("poll failed: HTTP %d: %s", status, msg)
	}
	var job Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, status, fmt.Errorf("decode job: %w", err)
	}
	return &job, status, nil
}

// SendHeartbeat updates last_seen, reports the runner's current status,
// advertises supported capabilities, and identifies the runner's OS/arch/version.
// Returns the runner's org_id from the server.
func (c *Client) SendHeartbeat(status string, capabilities []string, goos, goarch, version string) (string, error) {
	resp, err := c.do("POST", "/api/runner/heartbeat", map[string]interface{}{
		"status":       status,
		"capabilities": capabilities,
		"os":           goos,
		"arch":         goarch,
		"version":      version,
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(b))
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		if msg == "" {
			return "", fmt.Errorf("HTTP %d from /api/runner/heartbeat", resp.StatusCode)
		}
		return "", fmt.Errorf("HTTP %d from /api/runner/heartbeat: %s", resp.StatusCode, msg)
	}
	var body struct {
		OK    bool   `json:"ok"`
		OrgID string `json:"orgId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", nil // best-effort; don't fail heartbeat on decode error
	}
	return body.OrgID, nil
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

// RequestAgentCancel asks the platform to stop the current agent run for this runner
// without tearing down the runner process (POST /api/runner/cancel).
func (c *Client) RequestAgentCancel(jobID string) error {
	return c.post("/api/runner/cancel", map[string]string{"jobId": jobID})
}

// ReportWorktreePath reports the local worktree path for a job to the server.
func (c *Client) ReportWorktreePath(jobID, worktreePath string) error {
	return c.post("/api/runner/jobs/"+jobID+"/worktree", map[string]string{"worktree_path": worktreePath})
}

// ReportApprovalPending notifies the UI that a script is awaiting approval.
func (c *Client) ReportApprovalPending(jobID, runnerName string) error {
	return c.post("/api/runner/jobs/"+jobID+"/approval-pending", map[string]string{"runnerName": runnerName})
}

// ReportApprovalResolved notifies the UI that approval was resolved.
func (c *Client) ReportApprovalResolved(jobID string, approved bool) error {
	return c.post("/api/runner/jobs/"+jobID+"/approval-resolved", map[string]interface{}{"approved": approved})
}

// ScriptMeta holds metadata returned from the server script library.
type ScriptMeta struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Tags        []string  `json:"tags"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListServerScripts fetches the org's script library metadata from the server.
func (c *Client) ListServerScripts() ([]ScriptMeta, error) {
	resp, err := c.do("GET", "/api/runner/scripts", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d from /api/runner/scripts", resp.StatusCode)
	}
	var scripts []ScriptMeta
	if err := json.NewDecoder(resp.Body).Decode(&scripts); err != nil {
		return nil, fmt.Errorf("decode scripts: %w", err)
	}
	return scripts, nil
}

// PushScript upserts a script to the server-side script library.
// Implements executor.ScriptSyncer.
func (c *Client) PushScript(name, content, description string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	return c.post("/api/runner/scripts", map[string]interface{}{
		"name":        name,
		"content":     content,
		"description": description,
		"tags":        tags,
	})
}

// GetScript fetches the full content of a named script from the server library.
func (c *Client) GetScript(name string) (content string, err error) {
	resp, err := c.do("GET", "/api/runner/scripts/"+name, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return "", fmt.Errorf("script %q not found on server", name)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d fetching script %q", resp.StatusCode, name)
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode script %q: %w", name, err)
	}
	return body.Content, nil
}
