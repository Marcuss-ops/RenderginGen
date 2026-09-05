// Package model defines the domain types shared by the queue, its HTTP server
// and every storage backend (in-memory and PostgreSQL).
package model

import (
	"encoding/json"
	"time"
)

// State is the lifecycle state of a job.
type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateFailed    State = "failed"

	// StateRendered marks a job whose render is finished and durably stored in
	// the artifact store, but whose external publication (e.g. Google Drive)
	// failed. A worker re-claiming it must skip rendering and only retry the
	// publication step.
	StateRendered   State = "rendered"
	StateFinalizing State = "finalizing"
)

const (
	JobTypeRenderSegment  = "render_segment"
	JobTypeOverlayPrepare = "overlay.prepare"
	JobTypeOverlayRender  = "overlay.render"
)

// JobSchemaV1 identifies the renderinggen.job.v1 envelope.
const JobSchemaV1 = "renderinggen.job"

// JobSchemaVersionV1 is the version of the renderinggen.job.v1 envelope.
const JobSchemaVersionV1 = 1

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

// Job is a unit of work in the queue: one render SEGMENT. The renderable
// content carried in RenderPlan is the semantic OverlaySpec
// (renderinggen.overlay-plan.v1, accepted by the worker's
// overlay.CompileIfSemantic) for prepared jobs, or the concrete Chronon
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
