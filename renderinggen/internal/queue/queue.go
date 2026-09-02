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

// JobSchemaV1 identifies the renderinggen.job.v1 envelope.
const JobSchemaV1 = "renderinggen.job"

// JobSchemaVersionV1 is the version of the renderinggen.job.v1 envelope.
const JobSchemaVersionV1 = 1

const (
	JobTypeRenderSegment  = "render_segment"
	JobTypeOverlayPrepare = "overlay.prepare"
	JobTypeOverlayRender  = "overlay.render"
)

const workerClaimWait = 20 * time.Second

// State is the lifecycle state of a job, as reported on claim.
type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
	StateRendered  State = "rendered"
)

// AssetRef points at an asset in the central artifact store by content hash
// and the logical path it must be materialized at in the job workspace.
type FrameRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type AssetRef struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
}

// Job is one render SEGMENT pulled from the queue. RenderPlan is either the
// semantic renderinggen.overlay-plan.v1 emitted by PipelineGen; the worker
// compiles it exclusively to chronon.render-plan.v2.
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
	Lease      time.Duration   `json:"lease"`

	// State and Artifact are populated on claim: a re-claimed rendered job
	// carries its stored artifact so the worker can skip rendering.
	State    State     `json:"state,omitempty"`
	Artifact *Artifact `json:"artifact,omitempty"`
}

// Artifact is the metadata of the artifact produced for a completed job,
// including the copy-only certification VeloxEditing uses to assemble the
// overlay without re-decoding or re-encoding it.
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
	return fromClaimed(claimed, err)
}

// ClaimPending blocks efficiently until render work is available or ctx is
// cancelled. The queue server wakes this long poll on submit/requeue, so the
// worker no longer pays a fixed idle ticker before starting the next render.
func (c *Client) ClaimPending(ctx context.Context) (*Job, error) {
	for {
		claimed, err := c.q.ClaimPendingWait(ctx, c.workerID, workerClaimWait)
		if err != nil {
			return fromClaimed(claimed, err)
		}
		if claimed != nil {
			return fromClaimed(claimed, nil)
		}
		if ctx.Err() != nil {
			return nil, nil
		}
	}
}

// ClaimPendingWait performs one bounded long-poll claim. It is exposed for
// tests and callers that need explicit wait control.
func (c *Client) ClaimPendingWait(ctx context.Context, wait time.Duration) (*Job, error) {
	claimed, err := c.q.ClaimPendingWait(ctx, c.workerID, wait)
	return fromClaimed(claimed, err)
}

// ClaimRendered claims only jobs awaiting external publication.
func (c *Client) ClaimFinalization(ctx context.Context, parentID string) (*Job, bool, error) {
	claimed, ok, err := c.q.ClaimFinalization(ctx, parentID, c.workerID)
	if err != nil || !ok || claimed == nil {
		job, convErr := fromClaimed(claimed, err)
		return job, false, convErr
	}
	job, err := fromClaimed(claimed, nil)
	return job, true, err
}

func (c *Client) Children(ctx context.Context, parentID string) ([]*Job, error) {
	children, err := c.q.Children(ctx, parentID)
	if err != nil {
		return nil, err
	}
	result := make([]*Job, len(children))
	for i := range children {
		child := children[i]
		result[i] = &Job{ID: child.ID, ParentJobID: child.ParentJobID, ChunkIndex: child.ChunkIndex, FrameRange: fromClientFrameRange(child.FrameRange), State: State(child.State), Artifact: fromClientArtifact(child.Artifact)}
	}
	return result, nil
}

func (c *Client) ClaimRendered(ctx context.Context) (*Job, error) {
	for {
		claimed, err := c.q.ClaimRenderedWait(ctx, c.workerID, workerClaimWait)
		if err != nil {
			return fromClaimed(claimed, err)
		}
		if claimed != nil {
			return fromClaimed(claimed, nil)
		}
		if ctx.Err() != nil {
			return nil, nil
		}
	}
}

// ClaimRenderedWait performs one bounded long-poll publication claim.
func (c *Client) ClaimRenderedWait(ctx context.Context, wait time.Duration) (*Job, error) {
	claimed, err := c.q.ClaimRenderedWait(ctx, c.workerID, wait)
	return fromClaimed(claimed, err)
}

func fromClaimed(claimed *queueclient.ClaimedJob, err error) (*Job, error) {
	if err != nil {
		return nil, err
	}
	if claimed == nil {
		return nil, nil
	}
	return &Job{
		ID:             claimed.ID,
		Schema:         claimed.Schema,
		Version:        claimed.Version,
		IdempotencyKey: claimed.IdempotencyKey,
		JobType:        claimed.JobType,
		ParentJobID:    claimed.ParentJobID,
		ChunkIndex:     claimed.ChunkIndex,
		FrameRange:     fromClientFrameRange(claimed.FrameRange),
		RenderPlan:     claimed.RenderPlan,
		Assets:         fromClientAssets(claimed.Assets),
		Lease:          claimed.Lease,
		State:          State(claimed.State),
		Artifact:       fromClientArtifact(claimed.Artifact),
	}, nil
}

// Submit enqueues a job through the shared public queue contract.
func (c *Client) Submit(ctx context.Context, job Job) error {
	return c.q.Submit(ctx, queueclient.Job{
		ID: job.ID, Schema: job.Schema, Version: job.Version,
		IdempotencyKey: job.IdempotencyKey, JobType: job.JobType,
		ParentJobID: job.ParentJobID, ChunkIndex: job.ChunkIndex,
		FrameRange: toClientFrameRange(job.FrameRange),
		RenderPlan: job.RenderPlan, Assets: toClientAssets(job.Assets),
	})
}

// Complete reports a successfully rendered job along with its artifact.
func (c *Client) Complete(ctx context.Context, id string, artifact Artifact) error {
	return c.q.Complete(ctx, id, c.workerID, toClientArtifact(artifact))
}

// Fail reports a job that could not be rendered.
func (c *Client) Fail(ctx context.Context, id, reason string) error {
	return c.q.Fail(ctx, id, c.workerID, reason)
}

// Rendered reports a job whose render completed and was durably stored, but
// whose external publication (Drive) failed. The job stays claimable for a
// publication-only retry.
func (c *Client) Rendered(ctx context.Context, id, reason string, artifact Artifact) error {
	return c.q.Rendered(ctx, id, c.workerID, reason, toClientArtifact(artifact))
}

// Renew extends the lease on a running job, signalling liveness during a long
// render. It fails if the job expired and was requeued to another worker.
func (c *Client) Renew(ctx context.Context, id string) error {
	return c.q.Renew(ctx, id, c.workerID)
}

func toClientFrameRange(in *FrameRange) *queueclient.FrameRange {
	if in == nil {
		return nil
	}
	return &queueclient.FrameRange{Start: in.Start, End: in.End}
}

func toClientAssets(in []AssetRef) []queueclient.AssetRef {
	if in == nil {
		return nil
	}
	out := make([]queueclient.AssetRef, len(in))
	for i, a := range in {
		out[i] = queueclient.AssetRef{Hash: a.Hash, LogicalPath: a.LogicalPath}
	}
	return out
}

func fromClientFrameRange(in *queueclient.FrameRange) *FrameRange {
	if in == nil {
		return nil
	}
	return &FrameRange{Start: in.Start, End: in.End}
}

func fromClientAssets(in []queueclient.AssetRef) []AssetRef {
	if in == nil {
		return nil
	}
	out := make([]AssetRef, len(in))
	for i, a := range in {
		out[i] = AssetRef{Hash: a.Hash, LogicalPath: a.LogicalPath}
	}
	return out
}

func toClientArtifact(in Artifact) queueclient.Artifact {
	return queueclient.Artifact{
		ID:                 in.ID,
		Kind:               in.Kind,
		StorageKey:         in.StorageKey,
		ArtifactURL:        in.ArtifactURL,
		ArtifactHash:       in.ArtifactHash,
		ContentType:        in.ContentType,
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
		Backend:            in.Backend,
		ChrononVersion:     in.ChrononVersion,
		Metrics:            in.Metrics,
		DriveFileID:        in.DriveFileID,
		DriveLink:          in.DriveLink,
		Container:          in.Container,
		PixelFormat:        in.PixelFormat,
		AudioStreams:       in.AudioStreams,
	}
}

func fromClientArtifact(in *queueclient.Artifact) *Artifact {
	if in == nil {
		return nil
	}
	return &Artifact{
		ID:                 in.ID,
		Kind:               in.Kind,
		StorageKey:         in.StorageKey,
		ArtifactURL:        in.ArtifactURL,
		ArtifactHash:       in.ArtifactHash,
		ContentType:        in.ContentType,
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
		Backend:            in.Backend,
		ChrononVersion:     in.ChrononVersion,
		Metrics:            in.Metrics,
		DriveFileID:        in.DriveFileID,
		DriveLink:          in.DriveLink,
		Container:          in.Container,
		PixelFormat:        in.PixelFormat,
		AudioStreams:       in.AudioStreams,
	}
}
