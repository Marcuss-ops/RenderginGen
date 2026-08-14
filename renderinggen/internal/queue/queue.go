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

// Artifact is the metadata of the artifact produced for a completed job,
// including the copy-only certification VeloxEditing uses to assemble the
// overlay without re-decoding or re-encoding it.
type Artifact struct {
	ID                 string `json:"id,omitempty"`
	Kind               string `json:"kind,omitempty"`
	StorageKey         string `json:"storage_key,omitempty"`
	URL                string `json:"url,omitempty"`
	SHA256             string `json:"sha256,omitempty"`
	MimeType           string `json:"mime_type,omitempty"`
	SizeBytes          int64  `json:"size_bytes,omitempty"`
	Width              int    `json:"width,omitempty"`
	Height             int    `json:"height,omitempty"`
	FPSNum             int    `json:"fps_num,omitempty"`
	FPSDen             int    `json:"fps_den,omitempty"`
	FrameCount         int    `json:"frame_count,omitempty"`
	DurationUS         int64  `json:"duration_us,omitempty"`
	ProfileID          string `json:"profile_id,omitempty"`
	CopyEligible       bool   `json:"copy_eligible,omitempty"`
	Codec              string `json:"codec,omitempty"`
	CodecProfile       string `json:"codec_profile,omitempty"`
	ClosedGOP          bool   `json:"closed_gop,omitempty"`
	FirstFrameKeyframe bool   `json:"first_frame_keyframe,omitempty"`
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

// Complete reports a successfully rendered job along with its artifact.
func (c *Client) Complete(ctx context.Context, id string, artifact Artifact) error {
	return c.report(ctx, id, "complete", artifact)
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
