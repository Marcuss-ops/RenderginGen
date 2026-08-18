package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClaimedJob is the payload returned to a worker on a successful claim. It is
// a subset of Job plus the lease the worker must hold (and renew) while
// rendering.
type ClaimedJob struct {
	ID             string `json:"id"`
	Schema         string `json:"schema,omitempty"`
	Version        int    `json:"version,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	JobType        string `json:"job_type,omitempty"`

	RenderPlan json.RawMessage `json:"render_plan"`
	Assets     []AssetRef      `json:"assets"`
	Lease      time.Duration   `json:"lease"`

	// State and Artifact are populated when a rendered job is re-claimed, so
	// the worker can skip rendering and only retry publication.
	State    State     `json:"state,omitempty"`
	Artifact *Artifact `json:"artifact,omitempty"`
}

// Claim atomically claims the next pending job for workerID. It returns a nil
// job when the queue is empty.
func (c *Client) Claim(ctx context.Context, workerID string) (*ClaimedJob, error) {
	return c.claim(ctx, workerID, "")
}

// ClaimPending claims only jobs that still need rendering.
func (c *Client) ClaimPending(ctx context.Context, workerID string) (*ClaimedJob, error) {
	return c.claim(ctx, workerID, "pending")
}

// ClaimRendered claims only jobs awaiting external publication.
func (c *Client) ClaimRendered(ctx context.Context, workerID string) (*ClaimedJob, error) {
	return c.claim(ctx, workerID, "rendered")
}

func (c *Client) claim(ctx context.Context, workerID, state string) (*ClaimedJob, error) {
	body, err := json.Marshal(map[string]string{"worker": workerID, "state": state})
	if err != nil {
		return nil, fmt.Errorf("queue claim marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs/claim", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("queue claim request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("queue claim do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil // queue empty
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue claim: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var job ClaimedJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, fmt.Errorf("queue claim decode: %w", err)
	}
	return &job, nil
}

// Complete reports a successfully rendered job along with its artifact.
func (c *Client) Complete(ctx context.Context, id, workerID string, artifact Artifact) error {
	return c.report(ctx, id, workerID, "complete", artifact)
}

// Fail reports a job that could not be rendered.
func (c *Client) Fail(ctx context.Context, id, workerID, reason string) error {
	return c.report(ctx, id, workerID, "fail", map[string]string{"reason": reason})
}

// Rendered reports a job whose render completed and was durably stored, but
// whose external publication (Drive) failed. The job stays claimable for a
// publication-only retry.
func (c *Client) Rendered(ctx context.Context, id, workerID, reason string, artifact Artifact) error {
	return c.report(ctx, id, workerID, "rendered", map[string]any{"reason": reason, "artifact": artifact})
}

// Renew extends the lease on a running job owned by workerID. It fails if the
// job expired and was requeued to another worker.
func (c *Client) Renew(ctx context.Context, id, workerID string) error {
	return c.report(ctx, id, workerID, "renew", nil)
}

// report POSTs a worker action (complete/fail/renew) to the job endpoint. The
// wire body carries the worker identity plus an optional data payload, matching
// the queue server's handlers.
func (c *Client) report(ctx context.Context, id, workerID, action string, payload any) error {
	body := map[string]any{"worker": workerID}
	if payload != nil {
		body["data"] = payload
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("queue %s marshal: %w", action, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/jobs/"+url.PathEscape(id)+"/"+action, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("queue %s request: %w", action, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue %s do: %w", action, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue %s: HTTP %d: %s", action, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
