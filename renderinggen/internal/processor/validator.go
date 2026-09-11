package processor

import (
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// validateFrameRange enforces the chunk contract against the plan that will
// actually be rendered. The queue contract is half-open [Start, End); Chronon
// consumes an inclusive last frame. A range outside [0, plan.DurationFrames)
// cannot produce the frames the parent claimed, so it must fail at the
// prepare boundary — not minutes later inside Chronon with the GPU lane and
// the lease already spent.
//
// A plan with no certified duration (DurationFrames <= 0) is left to the
// compiler, which already rejects a zero-duration semantic plan; this check
// never invents a bound it cannot prove.
func validateFrameRange(job *queue.Job, plan *overlay.Plan) error {
	if job == nil || job.FrameRange == nil {
		return nil
	}
	if plan == nil {
		return fmt.Errorf("processor: frame range requires a compiled plan")
	}
	total := plan.Canvas.DurationFrames
	if total <= 0 {
		return nil
	}
	r := job.FrameRange
	if r.Start < 0 || r.End <= r.Start {
		return fmt.Errorf("processor: chunk frame_range [%d,%d) is empty or inverted (plan has %d frames)", r.Start, r.End, total)
	}
	if r.End > total {
		return fmt.Errorf("processor: chunk frame_range [%d,%d) exceeds the plan duration (%d frames)", r.Start, r.End, total)
	}
	return nil
}

// validate checks that the claimed job is a well-formed renderinggen.job.v1
// envelope: one render segment with a non-empty render plan and fully-resolved
// asset references.
//
// The v1 schema/version are REQUIRED on EVERY job. A producer bug (wrong
// envelope, missing field) must fail at claim time, not surface later as a
// render failure far from the cause.
//
// There used to be a "legacy fixture" opt-out (job_type "legacy" + a symbolic
// asset key) that skipped this check. It was removed because it could not
// actually render: PrepareJob still compiles the plan through
// overlay.CompileSemantic, which accepts only renderinggen.overlay-plan.v1, so
// the allowance only moved the failure from a precise envelope error to a
// confusing compile error. A concrete Chronon plan is not a supported input on
// any path — see CONFORMANCE.md.
func validate(job *queue.Job) error {
	if job == nil {
		return fmt.Errorf("processor: nil job")
	}
	if job.ID == "" {
		return fmt.Errorf("processor: job id is required")
	}
	if job.Schema != queue.JobSchemaV1 {
		return fmt.Errorf("processor: unsupported job schema %q (want %q)", job.Schema, queue.JobSchemaV1)
	}
	if job.Version != queue.JobSchemaVersionV1 {
		return fmt.Errorf("processor: unsupported job schema version %d (want %d)", job.Version, queue.JobSchemaVersionV1)
	}
	if len(job.RenderPlan) == 0 {
		return fmt.Errorf("processor: render_plan is required")
	}
	if !json.Valid(job.RenderPlan) {
		return fmt.Errorf("processor: render_plan is not valid JSON")
	}
	for _, a := range job.Assets {
		if a.Hash == "" {
			return fmt.Errorf("processor: asset hash is required")
		}
		if a.LogicalPath == "" {
			return fmt.Errorf("processor: asset logical_path is required")
		}
	}
	return nil
}
