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

// RenewConflictError identifies a permanent lease loss reported by the queue
// (HTTP 409 on renew): the job is no longer owned by this worker. Callers use
// errors.As with this type to abort immediately instead of retrying.
var RenewConflictError = queueclient.ErrLeaseConflict

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
	SourceURL   string `json:"source_url,omitempty"`
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
//
// It is an ALIAS of the queue module's public client type (the same pattern as
// Worker below): the worker and the queue speak one artifact contract, and the
// historical toClientArtifact/fromClientArtifact mappers — six hand-written
// field lists that drifted (e.g. ClosedGOP reached queue.Artifact before the
// queue module's model) — no longer exist because there is only one type to
// edit. A new artifact field is added once, in queue/client, and every layer
// compiles against it.
type Artifact = queueclient.Artifact

// Worker is the worker registration payload.
type Worker = queueclient.Worker
type WorkerStatus = queueclient.WorkerStatus

const (
	WorkerStatusUnknown  = queueclient.WorkerStatusUnknown
	WorkerStatusReady    = queueclient.WorkerStatusReady
	WorkerStatusBusy     = queueclient.WorkerStatusBusy
	WorkerStatusDraining = queueclient.WorkerStatusDraining
	WorkerStatusOffline  = queueclient.WorkerStatusOffline
)

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

// Register announces this worker to the queue's liveness registry.
func (c *Client) Register(ctx context.Context, w Worker) error {
	return c.q.RegisterWorker(ctx, w)
}

// Heartbeat keeps the worker visible to queue health and autoscaling.
func (c *Client) Heartbeat(ctx context.Context) error {
	return c.q.HeartbeatWorker(ctx, c.workerID)
}

// Retry requests a retry for a failed job on the queue.
func (c *Client) Retry(ctx context.Context, id string) error {
	return c.q.Retry(ctx, id)
}

// Claim atomically claims the next available job, or returns nil when empty.
func (c *Client) Claim(ctx context.Context) (*Job, error) {
	claimed, err := c.q.Claim(ctx, c.workerID)
	return fromClaimed(claimed, err)
}

// ClaimWait performs one bounded long-poll claim for any claimable state
// (pending AND rendered). Rendered jobs carry their durable artifact on claim
// so the worker skips rendering and retries only the external publication.
func (c *Client) ClaimWait(ctx context.Context, wait time.Duration) (*Job, error) {
	claimed, err := c.q.ClaimWait(ctx, c.workerID, wait)
	return fromClaimed(claimed, err)
}

// ClaimFinalization atomically claims a completed parent row so this worker
// can assemble the chunked artifact (see processor.ParentFinalizer).
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
		result[i] = &Job{ID: child.ID, ParentJobID: child.ParentJobID, ChunkIndex: child.ChunkIndex, FrameRange: fromClientFrameRange(child.FrameRange), State: State(child.State), Artifact: child.Artifact}
	}
	return result, nil
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
		Artifact:       claimed.Artifact,
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
	return c.q.Complete(ctx, id, c.workerID, artifact)
}

// Fail reports a job that could not be rendered.
func (c *Client) Fail(ctx context.Context, id, reason string) error {
	return c.q.Fail(ctx, id, c.workerID, reason)
}

// Rendered reports a job whose render completed and was durably stored, but
// whose external publication (Drive) failed. The job stays claimable for a
// publication-only retry.
func (c *Client) Rendered(ctx context.Context, id, reason string, artifact Artifact) error {
	return c.q.Rendered(ctx, id, c.workerID, reason, artifact)
}

// Renew extends the lease on a running job, signalling liveness during a long
// render. It fails if the job expired and was requeued to another worker.
func (c *Client) Renew(ctx context.Context, id string) error {
	return c.q.Renew(ctx, id, c.workerID)
}

// ReportProgress records live render progress (last frame position) for a
// running job owned by this worker. The wire contract lives in the public
// queue client; this adapter only fills in the worker identity.
func (c *Client) ReportProgress(ctx context.Context, id string, framesDone, framesTotal int64) error {
	return c.q.ReportProgress(ctx, id, c.workerID, queueclient.Progress{
		FramesDone:  int(framesDone),
		TotalFrames: int(framesTotal),
	})
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
		out[i] = queueclient.AssetRef{Hash: a.Hash, LogicalPath: a.LogicalPath, SourceURL: a.SourceURL}
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
		out[i] = AssetRef{Hash: a.Hash, LogicalPath: a.LogicalPath, SourceURL: a.SourceURL}
	}
	return out
}
