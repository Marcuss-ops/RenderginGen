// Package queue implements the pull-based job queue client.
//
// Workers never receive push requests from the orchestrator: they claim
// available jobs, hold a lease while rendering, and complete or fail them.
// The HTTP wire contract lives in the queue's public client
// (github.com/Marcuss-ops/RenderginGen/queue/client); this package only keeps
// the worker's domain types and adapts them, so the contract cannot drift.
package queue

import (
	"context"
	"encoding/json"
	"time"

	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
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

// Client claims and reports jobs against a central queue, delegating the wire
// contract to the queue's public client.
type Client struct {
	workerID string
	q        *queueclient.Client
}

// New creates a queue client for the given worker.
func New(endpoint, workerID string) *Client {
	return &Client{
		workerID: workerID,
		q:        queueclient.New(endpoint),
	}
}

// Claim atomically claims the next available job, or returns nil when empty.
func (c *Client) Claim(ctx context.Context) (*Job, error) {
	claimed, err := c.q.Claim(ctx, c.workerID)
	if err != nil {
		return nil, err
	}
	if claimed == nil {
		return nil, nil
	}
	return &Job{
		ID:          claimed.ID,
		OverlaySpec: claimed.OverlaySpec,
		Assets:      fromClientAssets(claimed.Assets),
		Lease:       claimed.Lease,
	}, nil
}

// Complete reports a successfully rendered job along with its artifact.
func (c *Client) Complete(ctx context.Context, id string, artifact Artifact) error {
	return c.q.Complete(ctx, id, c.workerID, toClientArtifact(artifact))
}

// Fail reports a job that could not be rendered.
func (c *Client) Fail(ctx context.Context, id, reason string) error {
	return c.q.Fail(ctx, id, c.workerID, reason)
}

// Renew extends the lease on a running job, signalling liveness during a long
// render. It fails if the job expired and was requeued to another worker.
func (c *Client) Renew(ctx context.Context, id string) error {
	return c.q.Renew(ctx, id, c.workerID)
}

func fromClientAssets(in []queueclient.AssetRef) []AssetRef {
	if in == nil {
		return nil
	}
	out := make([]AssetRef, len(in))
	for i, a := range in {
		out[i] = AssetRef{Hash: a.Hash, URL: a.URL}
	}
	return out
}

func toClientArtifact(in Artifact) queueclient.Artifact {
	return queueclient.Artifact{
		ID:                 in.ID,
		Kind:               in.Kind,
		StorageKey:         in.StorageKey,
		URL:                in.URL,
		SHA256:             in.SHA256,
		MimeType:           in.MimeType,
		SizeBytes:          in.SizeBytes,
		Width:              in.Width,
		Height:             in.Height,
		FPSNum:             in.FPSNum,
		FPSDen:             in.FPSDen,
		FrameCount:         in.FrameCount,
		DurationUS:         in.DurationUS,
		ProfileID:          in.ProfileID,
		CopyEligible:       in.CopyEligible,
		Codec:              in.Codec,
		CodecProfile:       in.CodecProfile,
		ClosedGOP:          in.ClosedGOP,
		FirstFrameKeyframe: in.FirstFrameKeyframe,
	}
}
