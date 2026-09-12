// staged_render.go owns the serial staged pipeline used outside the
// concurrent worker pools (single-job mode, tests).
//
// Scope contract: StagedRender is the RENDER half only (PrepareJob -> RunGPU ->
// FinalizeJob), exactly the stages the worker pools run before they call
// Publish. External delivery is a separate step on both paths: Process = Render
// + Publish is the serial equivalent of the pool's FinalizeJob + Publish, while
// Render deliberately stops before publication so a failed upload can be
// retried without a GPU re-render. Keeping that split identical on both paths
// is pinned by TestSerialProcessAppliesPublicationPolicy.
package processor

import (
	"context"
	"os"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// StagedRender runs the three render stages serially for one job; used when a
// caller wants the staged pipeline semantics without the concurrent worker
// pools (single-job mode, tests). It never publishes — callers that need the
// whole pipeline use Process (Render + Publish), like the worker pools do.
func (p *Processor) StagedRender(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	prepared, err := p.PrepareJob(ctx, job)
	if err != nil {
		return queue.Artifact{}, err
	}
	if os.Getenv("RENDERINGGEN_KEEP_WORKSPACE") != "1" {
		defer p.cleanupWorkspace(prepared.Workspace, job.ID)
	}
	if err := p.RunGPU(ctx, prepared); err != nil {
		return queue.Artifact{}, err
	}
	p.PutGPURenderEnd(time.Now())
	return p.FinalizeJob(ctx, prepared)
}
