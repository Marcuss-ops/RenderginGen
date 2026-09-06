package processor

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
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

// isLegacyJob reports whether the job is a legacy development fixture. A
// legacy job carries at least one symbolic (non-SHA-256) asset key — the same
// allowance the asset resolvers use to skip content verification — and
// predates the renderinggen.job.v1 envelope, so its schema/version fields may
// be absent. Every content-addressed job (all 64-hex asset keys, or none) is
// the production contract and must declare the envelope exactly.
func isLegacyJob(job *queue.Job) bool {
	for _, a := range job.Assets {
		if !isSHA256Hash(a.Hash) {
			return true
		}
	}
	return false
}

// validate checks that the claimed job is a well-formed renderinggen.job.v1
// envelope: one render segment with a non-empty render plan and fully-resolved
// asset references.
//
// The v1 schema/version are REQUIRED for content-addressed jobs: a producer
// bug (wrong envelope, missing field) must fail at claim time, not surface
// later as a render failure far from the cause. Legacy development fixtures —
// identified by symbolic (non-SHA-256) asset keys — keep the historical
// allowance: their envelope fields may be absent and their keys are never
// content-verified.
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
