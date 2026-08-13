// Package queue implements the pull-based job queue client.
//
// Workers never receive push requests from the orchestrator: they claim
// available jobs, hold a lease while rendering, and complete or fail them.
package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// AssetRef points at an asset in the central artifact store.
type AssetRef struct {
	Hash string `json:"hash"`
	URL  string `json:"url"`
}

// Job is a single overlay render request pulled from the queue.
type Job struct {
	ID          string          `json:"id"`
	OverlaySpec json.RawMessage `json:"overlay_spec"`
	Assets      []AssetRef      `json:"assets"`
	Lease       time.Duration   `json:"lease"`
}

// Result is the artifact produced for a completed job.
type Result struct {
	ArtifactURL string `json:"artifact_url"`
}

// Client claims and reports jobs against a central queue.
type Client struct {
	endpoint string
	workerID string
	http     *http.Client
}

// New creates a queue client for the given worker.
func New(endpoint, workerID string) *Client {
	return &Client{
		endpoint: endpoint,
		workerID: workerID,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Claim atomically claims the next available job, or returns nil when empty.
func (c *Client) Claim(ctx context.Context) (*Job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/jobs/claim", bytes.NewReader([]byte(`{"worker":"`+c.workerID+`"}`)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil // queue empty
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claim: unexpected status %d", resp.StatusCode)
	}

	var job Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, err
	}
	return &job, nil
}

// Complete reports a successfully rendered job.
func (c *Client) Complete(ctx context.Context, id string, result Result) error {
	return c.report(ctx, id, "complete", result)
}

// Fail reports a job that could not be rendered.
func (c *Client) Fail(ctx context.Context, id, reason string) error {
	return c.report(ctx, id, "fail", map[string]string{"reason": reason})
}

// Renew extends the lease on a running job, signalling liveness during a long
// render. It fails if the job expired and was requeued to another worker.
func (c *Client) Renew(ctx context.Context, id string) error {
	return c.report(ctx, id, "renew", nil)
}

func (c *Client) report(ctx context.Context, id, state string, payload any) error {
	body := map[string]any{
		"worker": c.workerID,
		"state":  state,
		"data":   payload,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/jobs/"+id+"/"+state, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("report: unexpected status %d", resp.StatusCode)
	}
	return nil
}
