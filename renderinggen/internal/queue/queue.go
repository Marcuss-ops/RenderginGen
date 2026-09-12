// Package queue implements the pull-based job queue client.
//
// Workers never receive push requests from the orchestrator: they claim
// available jobs, hold a lease while rendering, and complete or fail them.
// The HTTP wire contract lives in the queue's public client
// (github.com/Marcuss-ops/RenderingGen/queue/client); this package only keeps
// the worker's domain types and adapts them, so the contract cannot drift.
package queue

import (
	"context"
	"time"

	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// The envelope identity and job-type vocabulary are ALIASES of the public
// wire contract (queue/client), which owns them. Declaring them again here —
// which the worker used to do — let a value change in one module without the
// other, and a "renderinggen.job" rename would have needed three synchronized
// edits and no test.
const (
	JobSchemaV1        = queueclient.JobSchemaV1
	JobSchemaVersionV1 = queueclient.JobSchemaVersionV1
)

const (
	JobTypeRenderSegment  = queueclient.JobTypeRenderSegment
	JobTypeOverlayPrepare = queueclient.JobTypeOverlayPrepare
	JobTypeOverlayRender  = queueclient.JobTypeOverlayRender
)

// RenewConflictError identifies a permanent lease loss reported by the queue
// (HTTP 409 on renew): the job is no longer owned by this worker. Callers use
// errors.As with this type to abort immediately instead of retrying.
var RenewConflictError = queueclient.ErrLeaseConflict

// State is the lifecycle state of a job, as reported on claim. It is an
// ALIAS of the canonical wire type: the worker, the queue service and every
// producer share one State definition, so the worker can represent every
// state the queue can assign it (including finalizing and cancelled, which a
// locally-declared subset silently could not).
type State = queueclient.State

const (
	StatePending    = queueclient.StatePending
	StateRunning    = queueclient.StateRunning
	StateFinalizing = queueclient.StateFinalizing
	StateCompleted  = queueclient.StateCompleted
	StateFailed     = queueclient.StateFailed
	StateCancelled  = queueclient.StateCancelled
	StateRendered   = queueclient.StateRendered
)

// FrameRange and AssetRef are ALIASES of the public wire contract, like State,
// Artifact, Worker and Job below. The historical local declarations plus the
// toClient*/fromClient* mappers restated the same fields by hand; there is one
// declaration, so a new field can never be silently dropped by a copy.
type FrameRange = queueclient.FrameRange

type AssetRef = queueclient.AssetRef

// Job is one render SEGMENT pulled from the queue. RenderPlan is the semantic
// renderinggen.overlay-plan.v1 emitted by PipelineGen; the worker compiles it
// exclusively to chronon.render-plan.v2. Lease is populated on a claim.
//
// It is an ALIAS of the public wire type (the same pattern as Artifact and
// Worker): the worker, the queue service and every producer share one Job
// declaration — including State, Artifact, Progress and Lease.
type Job = queueclient.Job

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
	return c.q.Claim(ctx, c.workerID)
}

// ClaimWait performs one bounded long-poll claim for any claimable state
// (pending AND rendered). Rendered jobs carry their durable artifact on claim
// so the worker skips rendering and retries only the external publication.
func (c *Client) ClaimWait(ctx context.Context, wait time.Duration) (*Job, error) {
	return c.q.ClaimWait(ctx, c.workerID, wait)
}

// ClaimFinalization atomically claims a completed parent row so this worker
// can assemble the chunked artifact (see processor.ParentFinalizer).
func (c *Client) ClaimFinalization(ctx context.Context, parentID string) (*Job, bool, error) {
	return c.q.ClaimFinalization(ctx, parentID, c.workerID)
}

func (c *Client) Children(ctx context.Context, parentID string) ([]*Job, error) {
	children, err := c.q.Children(ctx, parentID)
	if err != nil {
		return nil, err
	}
	result := make([]*Job, len(children))
	for i := range children {
		result[i] = &children[i]
	}
	return result, nil
}

// Submit enqueues a job through the shared public queue contract.
func (c *Client) Submit(ctx context.Context, job Job) error {
	return c.q.Submit(ctx, job)
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
