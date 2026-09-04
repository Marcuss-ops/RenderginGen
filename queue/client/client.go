// Package client is the public HTTP client for the central RenderingGen queue
// service. Producer services (PipelineGen and any future orchestrator) import
// this package to submit render jobs and wait for the certified artifact,
// instead of hand-rolling the HTTP contract themselves.
//
// The wire contract mirrors the queue HTTP server:
//
//	POST /jobs            submit a job (409 -> ErrJobExists)
//	GET  /jobs/{id}       current state + artifact
//	GET  /jobs/depth      queue depth/stats
//	GET  /health          liveness
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrJobExists is returned by Submit when the job ID was already submitted.
// Callers treat it as idempotent success for replay/retry scenarios.
var ErrJobExists = errors.New("job already exists")

// ErrNotFound is returned by Get when the job does not exist.
var ErrNotFound = errors.New("job not found")

// State is the lifecycle state of a job.
type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
	StateRendered  State = "rendered"
)

// JobSchemaV1 identifies the renderinggen.job.v1 envelope.
const JobSchemaV1 = "renderinggen.job"

// JobSchemaVersionV1 is the version of the renderinggen.job.v1 envelope.
const JobSchemaVersionV1 = 1

// AssetRef points at an asset in the central artifact store by content hash
// and the logical path it must be materialized at in the job workspace.
type AssetRef struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
	// SourceURL is an optional durable download location. LogicalPath remains
	// the local workspace destination and must never be overloaded with a URL.
	SourceURL string `json:"source_url,omitempty"`
}

// Artifact is the metadata of a rendered artifact, including the copy-only
// certification (codec, profile, GOP/keyframe flags) VeloxEditing relies on.
type Artifact struct {
	ID                 string             `json:"id,omitempty"`
	Kind               string             `json:"kind,omitempty"`
	StorageKey         string             `json:"storage_key,omitempty"`
	ArtifactURL        string             `json:"artifact_url,omitempty"`
	ArtifactHash       string             `json:"artifact_hash,omitempty"`
	ContentType        string             `json:"content_type,omitempty"`
	SizeBytes          int64              `json:"size_bytes,omitempty"`
	Width              int                `json:"width,omitempty"`
	Height             int                `json:"height,omitempty"`
	FPSNum             int                `json:"fps_num,omitempty"`
	FPSDen             int                `json:"fps_den,omitempty"`
	FrameCount         int                `json:"frame_count,omitempty"`
	DurationUS         int64              `json:"duration_us,omitempty"`
	ProfileID          string             `json:"profile_id,omitempty"`
	CopyEligible       bool               `json:"copy_eligible,omitempty"`
	Codec              string             `json:"codec,omitempty"`
	CodecProfile       string             `json:"codec_profile,omitempty"`
	ClosedGOP          bool               `json:"closed_gop,omitempty"`
	FirstFrameKeyframe bool               `json:"first_frame_keyframe,omitempty"`
	Backend            string             `json:"backend,omitempty"`
	ChrononVersion     string             `json:"chronon_version,omitempty"`
	Metrics            map[string]float64 `json:"metrics,omitempty"`
	DriveFileID        string             `json:"drive_file_id,omitempty"`
	DriveLink          string             `json:"drive_link,omitempty"`
	Container          string             `json:"container,omitempty"`
	PixelFormat        string             `json:"pixel_format,omitempty"`
	AudioStreams       int                `json:"audio_streams,omitempty"`
}

// Job is the unit of work exchanged with the queue: one render SEGMENT. On
// submit only ID, Schema, Version, RenderPlan and Assets matter; on Get the
// server also fills in State, the attempt count, timestamps and (once
// completed) the Artifact.
type FrameRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type Job struct {
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

	State       State     `json:"state"`
	Worker      string    `json:"worker,omitempty"`
	Attempts    int       `json:"attempts"`
	CreatedAt   time.Time `json:"created_at"`
	QueuedAt    time.Time `json:"queued_at,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	LeaseUntil  time.Time `json:"lease_until,omitempty"`
	FailReason  string    `json:"fail_reason,omitempty"`

	Artifact *Artifact `json:"artifact,omitempty"`
}

// Stats is a snapshot of the queue, used for autoscaling and monitoring.
type Stats struct {
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Depth     int `json:"depth"`
}

// Client speaks the central queue's HTTP API.
type Client struct {
	baseURL string
	http    *http.Client
}

// New creates a queue client for the given RenderingGen queue endpoint.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Submit enqueues a job. A 409 (job already exists) is translated to
// ErrJobExists so callers can treat replays as idempotent.
func (c *Client) Submit(ctx context.Context, job Job) error {
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("queue submit marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("queue submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue submit do: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return nil
	case http.StatusConflict:
		return fmt.Errorf("%w: job %s", ErrJobExists, job.ID)
	default:
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue submit: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
}

// Children returns a parent's child chunks in chunk order.
func (c *Client) Children(ctx context.Context, parentID string) ([]Job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/jobs/"+url.PathEscape(parentID)+"/children", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue children: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var jobs []Job
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// Get returns the current state of a job, including its artifact once done.
func (c *Client) Get(ctx context.Context, id string) (Job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/jobs/"+url.PathEscape(id), nil)
	if err != nil {
		return Job{}, fmt.Errorf("queue get request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Job{}, fmt.Errorf("queue get do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return Job{}, fmt.Errorf("%w: job %s", ErrNotFound, id)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return Job{}, fmt.Errorf("queue get: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var job Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return Job{}, fmt.Errorf("queue get decode: %w", err)
	}
	return job, nil
}

// Depth returns a snapshot of the queue state.
func (c *Client) Depth(ctx context.Context) (Stats, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/jobs/depth", nil)
	if err != nil {
		return Stats{}, fmt.Errorf("queue depth request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Stats{}, fmt.Errorf("queue depth do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return Stats{}, fmt.Errorf("queue depth: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var stats Stats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return Stats{}, fmt.Errorf("queue depth decode: %w", err)
	}
	return stats, nil
}

// Health reports whether the queue service is reachable and healthy.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("queue health request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue health do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue health: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// Wait polls Get until the job reaches a terminal state. It retains the
// polling API for compatibility but uses exponential backoff, avoiding a
// fixed multi-second latency for short jobs while limiting request pressure.
func (c *Client) Wait(ctx context.Context, id string, interval time.Duration) (Job, error) {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	if interval > time.Second {
		interval = time.Second
	}
	backoff := interval
	for {
		job, err := c.Get(ctx, id)
		if err == nil && (job.State == StateCompleted || job.State == StateFailed) {
			return job, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Job{}, err
		}
		select {
		case <-ctx.Done():
			return Job{}, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < time.Second {
			backoff *= 2
			if backoff > time.Second {
				backoff = time.Second
			}
		}
	}
}
