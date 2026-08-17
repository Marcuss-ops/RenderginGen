package processor

import (
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
)

// validate checks that the claimed job is a well-formed renderinggen.job.v1
// envelope: one render segment with a non-empty render plan and fully-resolved
// asset references.
func validate(job *queue.Job) error {
	if job == nil {
		return fmt.Errorf("processor: nil job")
	}
	if job.ID == "" {
		return fmt.Errorf("processor: job id is required")
	}
	if job.Schema != "" && job.Schema != queue.JobSchemaV1 {
		return fmt.Errorf("processor: unsupported job schema %q", job.Schema)
	}
	if job.Version != 0 && job.Version != queue.JobSchemaVersionV1 {
		return fmt.Errorf("processor: unsupported job schema version %d", job.Version)
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
