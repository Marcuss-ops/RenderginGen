package processor

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// isSHA256Hash reports whether hash is a canonical content address: exactly 64
// hexadecimal characters. Content-addressed keys are the production contract;
// anything shorter or non-hex is a symbolic/legacy key.
func isSHA256Hash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

// isLegacyJob reports whether the job is an explicitly-marked legacy
// development fixture. BOTH signals are required: the job declares
// job_type "legacy" AND carries at least one symbolic (non-SHA-256) asset key.
//
// An omitted schema is deliberately NOT a legacy signal. It used to be, which
// let a malformed payload (schema dropped, asset hash replaced by "abc")
// disable the whole v1 envelope check and the content-hash verification that
// keys off it — a producer bug or a hostile submitter could reach Chronon on
// an unvalidated, unverified path. The opt-out is now single and auditable:
// a job that wants the legacy allowance must say so.
func isLegacyJob(job *queue.Job) bool {
	if job == nil || len(job.Assets) == 0 {
		return false
	}
	if job.JobType != queue.JobTypeLegacy {
		return false
	}
	for _, a := range job.Assets {
		if !isSHA256Hash(a.Hash) {
			return true
		}
	}
	return false
}

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
// The v1 schema/version are REQUIRED for content-addressed jobs: a producer
// bug (wrong envelope, missing field) must fail at claim time, not surface
// later as a render failure far from the cause. Legacy development fixtures —
// which must declare job_type "legacy" AND use symbolic (non-SHA-256) asset
// keys — keep the historical allowance: their envelope fields may be absent
// and their keys are never content-verified.
func validate(job *queue.Job) error {
	if job == nil {
		return fmt.Errorf("processor: nil job")
	}
	if job.ID == "" {
		return fmt.Errorf("processor: job id is required")
	}
	legacy := isLegacyJob(job)
	if !legacy {
		if job.Schema != queue.JobSchemaV1 {
			return fmt.Errorf("processor: unsupported job schema %q (want %q)", job.Schema, queue.JobSchemaV1)
		}
		if job.Version != queue.JobSchemaVersionV1 {
			return fmt.Errorf("processor: unsupported job schema version %d (want %d)", job.Version, queue.JobSchemaVersionV1)
		}
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
