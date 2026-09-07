// staged_render.go owns the serial staged pipeline used outside the
// concurrent worker pools (single-job mode, tests). Its metrics and behavior
// match the pool path exactly.
package processor

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
)

// StagedRender runs the three stages serially for one job; used when a caller
// wants the staged pipeline semantics without the concurrent worker pools
// (single-job mode, tests). Metrics and behavior match Render.
func (p *Processor) StagedRender(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	prepared, err := p.PrepareJob(ctx, job)
	if err != nil {
		return queue.Artifact{}, err
	}
	if os.Getenv("RENDERINGGEN_KEEP_WORKSPACE") != "1" {
		defer func() {
			if err := prepared.Workspace.Cleanup(); err != nil {
				log.Printf("job %s: workspace cleanup: %v", job.ID, err)
			}
		}()
	}
	if err := p.RunGPU(ctx, prepared); err != nil {
		return queue.Artifact{}, err
	}
	p.PutGPURenderEnd(time.Now())
	return p.FinalizeJob(ctx, prepared)
}
