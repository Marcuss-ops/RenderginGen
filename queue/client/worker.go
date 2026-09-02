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
// ClaimFinalization atomically claims a parent for finalization.
func (c *Client) ClaimFinalization(ctx context.Context, parentID, workerID string) (*ClaimedJob, bool, error) {
	body, err := json.Marshal(map[string]string{"worker": workerID})
	if err != nil {
		return nil, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs/"+url.PathEscape(parentID)+"/finalize/claim", bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("queue finalize claim: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var job ClaimedJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, false, err
	}
	return &job, true, nil
}

type ClaimedJob struct {
	ID             string      `json:"id"`
	Schema         string      `json:"schema,omitempty"`
	Version        int         `json:"version,omitempty"`
	IdempotencyKey string      `json:"idempotency_key,omitempty"`
	JobType        string      `json:"job_type,omitempty"`
	ParentJobID    string      `json:"parent_job_id,omitempty"`
	ChunkIndex     int         `json:"chunk_index,omitempty"`
	FrameRange     *FrameRange `json:"frame_range,omitempty"`

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
	return c.claim(ctx, workerID, "", 0)
}

// ClaimPending claims only jobs that still need rendering.
func (c *Client) ClaimPending(ctx context.Context, workerID string) (*ClaimedJob, error) {
	return c.claim(ctx, workerID, "pending", 0)
}

// ClaimRendered claims only jobs awaiting external publication.
func (c *Client) ClaimRendered(ctx context.Context, workerID string) (*ClaimedJob, error) {
	return c.claim(ctx, workerID, "rendered", 0)
}

// ClaimWait long-polls the queue for up to maxWait, returning as soon as a
// job can be claimed. The wake-up signal is event-driven; the atomic claim
// itself is unchanged (SKIP LOCKED / memory store), so no job is ever handed
// to two workers by the wait path. It returns a nil job on timeout.
func (c *Client) ClaimWait(ctx context.Context, workerID string, maxWait time.Duration) (*ClaimedJob, error) {
	return c.claimWait(ctx, workerID, "", maxWait)
}

// ClaimPendingWait is ClaimWait restricted to jobs that still need rendering.
func (c *Client) ClaimPendingWait(ctx context.Context, workerID string, maxWait time.Duration) (*ClaimedJob, error) {
	return c.claimWait(ctx, workerID, "pending", maxWait)
}

// ClaimRenderedWait is ClaimWait restricted to jobs awaiting publication.
func (c *Client) ClaimRenderedWait(ctx context.Context, workerID string, maxWait time.Duration) (*ClaimedJob, error) {
	return c.claimWait(ctx, workerID, "rendered", maxWait)
}

func (c *Client) claimWait(ctx context.Context, workerID, state string, maxWait time.Duration) (*ClaimedJob, error) {
	if maxWait <= 0 {
		maxWait = 25 * time.Second
	}
	body, err := json.Marshal(map[string]any{
		"worker":      workerID,
		"state":       state,
		"max_wait_ms": maxWait.Milliseconds(),
	})
	if err != nil {
		return nil, fmt.Errorf("queue claim wait marshal: %w", err)
	}
	// The request outlives the long-poll window so the server can respond
	// after its own wait elapses instead of tripping the client timeout.
	reqCtx, cancel := context.WithTimeout(ctx, maxWait+10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+"/jobs/claim/wait", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("queue claim wait request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("queue claim wait do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue claim wait: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var job ClaimedJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, fmt.Errorf("queue claim wait decode: %w", err)
	}
	return &job, nil
}

func (c *Client) claim(ctx context.Context, workerID, state string, wait time.Duration) (*ClaimedJob, error) {
	waitMS := wait.Milliseconds()
	if waitMS < 0 {
		waitMS = 0
	}
	body, err := json.Marshal(struct {
		Worker string `json:"worker"`
		State  string `json:"state,omitempty"`
		WaitMS int64  `json:"wait_ms,omitempty"`
	}{Worker: workerID, State: state, WaitMS: waitMS})
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
		return nil, nil // queue empty after optional wait
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
