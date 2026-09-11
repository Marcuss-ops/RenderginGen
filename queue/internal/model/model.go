// Package model defines the domain types shared by the queue, its HTTP server
// and every storage backend (in-memory and PostgreSQL).
package model

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
)

// State is the lifecycle state of a job.
//
// It is an ALIAS of the public wire contract type (queue/client), not a second
// definition: the queue service, the worker and every producer share exactly
// one State type, so the vocabulary cannot drift across the wire. The internal
// names below stay for readability at the storage layer.
//
// StateCancelled marks a job a producer cancelled before it reached a
// terminal state. Cancelled is terminal: the job is never claimable again and
// lease expiry never requeues it, so a cancelled job can never trigger a new
// Chronon render across lease/claim cycles. An in-flight render that was
// already claimed when the cancel landed finishes its GPU invocation (it
// cannot be preempted), but its complete/fail reports are rejected and no
// further invocation ever starts.
//
// StateRendered marks a job whose render is finished and durably stored in the
// artifact store, but whose external publication (e.g. Google Drive) failed. A
// worker re-claiming it must skip rendering and only retry the publication
// step.
type State = client.State

const (
	StatePending    = client.StatePending
	StateRunning    = client.StateRunning
	StateCompleted  = client.StateCompleted
	StateFailed     = client.StateFailed
	StateRendered   = client.StateRendered
	StateCancelled  = client.StateCancelled
	StateFinalizing = client.StateFinalizing
)

// States returns the canonical job-state vocabulary. It is the list the
// render_jobs_state_check SQL constraint must match.
func States() []State { return client.AllStates() }

// AttemptStatus is the lifecycle status of one render attempt
// (render_attempts.status). Its vocabulary mirrors the job states plus
// lease_expired, which has no job-state counterpart: an attempt that ended
// because its lease elapsed is recorded as lease_expired, while the job row
// returns to pending (or fails permanently).
type AttemptStatus string

const (
	AttemptRunning      AttemptStatus = "running"
	AttemptCompleted    AttemptStatus = "completed"
	AttemptFailed       AttemptStatus = "failed"
	AttemptLeaseExpired AttemptStatus = "lease_expired"
	AttemptRendered     AttemptStatus = "rendered"
	AttemptCancelled    AttemptStatus = "cancelled"
)

// AttemptStatuses returns the canonical attempt-status vocabulary. It is the
// list the render_attempts_status_check SQL constraint must match.
func AttemptStatuses() []AttemptStatus {
	return []AttemptStatus{
		AttemptRunning, AttemptCompleted, AttemptFailed,
		AttemptLeaseExpired, AttemptRendered, AttemptCancelled,
	}
}

const (
	JobTypeRenderSegment  = client.JobTypeRenderSegment
	JobTypeOverlayPrepare = client.JobTypeOverlayPrepare
	JobTypeOverlayRender  = client.JobTypeOverlayRender
)

// JobSchemaV1 identifies the renderinggen.job.v1 envelope. It is an alias of
// the public wire contract so the value exists once, in queue/client.
const (
	JobSchemaV1        = client.JobSchemaV1
	JobSchemaVersionV1 = client.JobSchemaVersionV1
)

// AssetRef points at an asset in the central artifact store by content hash
// and the logical path it must be materialized at in the job workspace.
type FrameRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// ValidateChunk validates chunk metadata for a job. It is the single authority
// for frame_range/chunk_index invariants (U6): both memory and postgres
// repositories must call this instead of duplicating inline checks.
func ValidateChunk(job Job) error {
	if job.FrameRange != nil && (job.FrameRange.Start < 0 || job.FrameRange.End <= job.FrameRange.Start) {
		return fmt.Errorf("invalid frame_range for job %s", job.ID)
	}
	if job.ChunkIndex < 0 {
		return fmt.Errorf("invalid chunk_index for job %s", job.ID)
	}
	return nil
}

type AssetRef struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
	SourceURL   string `json:"source_url,omitempty"`
}

// Job is a unit of work in the queue: one render SEGMENT. The renderable
// content carried in RenderPlan is the semantic OverlaySpec
// (renderinggen.overlay-plan.v1, accepted by the worker's
// overlay.CompileSemantic) for prepared jobs, or the concrete Chronon
// render-plan document on precompiled paths. The worker writes plan.json and
// Chronon3d composes every layer of the segment in a single pass.
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

	// Artifact is the rendered artifact, populated once the job completes.
	Artifact *Artifact `json:"artifact,omitempty"`

	// Progress is the last render progress reported by the owning worker
	// (nil until the first report arrives). Exposed by GET /jobs/{id}.
	Progress *Progress `json:"progress,omitempty"`
}

// Progress is the per-job render progress reported by the worker that owns
// the job's lease. FramesDone is the last frame position the renderer
// reported (absolute, already offset for chunked execution). TotalFrames is
// the segment length when known (0 = unknown). LastFrameAt is the wall-clock
// time of the last frame report and doubles as a render liveness signal.
type Progress struct {
	FramesDone  int       `json:"frames_done"`
	TotalFrames int       `json:"frames_total,omitempty"`
	LastFrameAt time.Time `json:"last_frame_at"`
	Worker      string    `json:"worker,omitempty"`
}

// Stats is a snapshot of the queue, used for autoscaling and monitoring.
// Ok reports whether the snapshot came from a successful store query: a
// failed snapshot is all-zeros with Ok=false, so consumers can distinguish
// "queue is empty" from "store unavailable".
type Stats struct {
	Pending   int  `json:"pending"`
	Running   int  `json:"running"`
	Completed int  `json:"completed"`
	Failed    int  `json:"failed"`
	Depth     int  `json:"depth"`
	Ok        bool `json:"ok"`
}
