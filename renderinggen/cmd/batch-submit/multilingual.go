// multilingual.go owns batch-submit's Strategy-A mode: submit the base job,
// wait for its artifact, then bind and submit the per-language overlay jobs.
package main

import (
	"context"
	"fmt"
	"time"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/batch"
)

// multilingualSummary is the CLI's JSON report for a Strategy-A run.
type multilingualSummary struct {
	Batch        string        `json:"batch"`
	BaseJobID    string        `json:"base_job_id"`
	BaseWave     batch.Result  `json:"base_wave"`
	BaseArtifact string        `json:"base_artifact_hash,omitempty"`
	OverlayWave  *batch.Result `json:"overlay_wave,omitempty"`
	OverlayJobs  []string      `json:"overlay_jobs,omitempty"`
	Aborted      bool          `json:"aborted"`
	AbortReason  string        `json:"abort_reason,omitempty"`
	DurationMS   int64         `json:"duration_ms"`
}

// runMultilingual executes the two-wave Strategy-A submission:
//
//	wave 1: submit the base render job (ordinary idempotent submit)
//	wave 2: after the base completes, patch each language overlay plan with
//	        the base artifact hash and submit the overlay jobs
//
// The base must reach COMPLETED before wave 2: the overlay jobs reference the
// base video through its artifact content hash. A failed/cancelled base
// aborts the batch without submitting any overlay job — the report still
// prints (with aborted=true) so operators see exactly where the pipeline
// stopped.
func runMultilingual(ctx context.Context, s batch.Submitter, jobs []queue.Job, ids manifestIdentifiers, poll time.Duration) (multilingualSummary, error) {
	start := time.Now()
	summary := multilingualSummary{Batch: string(ids.batchID)}

	var baseJob queue.Job
	var overlays []queue.Job
	for _, j := range jobs {
		if j.ParentJobID == "" {
			baseJob = j
		} else {
			overlays = append(overlays, j)
		}
	}

	// Wave 1: the base job.
	baseRes, err := batch.SubmitAll(ctx, s, []batchJob{baseJob})
	summary.BaseWave = baseRes
	if err != nil {
		summary.DurationMS = time.Since(start).Milliseconds()
		return summary, fmt.Errorf("base wave: %w", err)
	}

	// Between waves: wait for the base artifact.
	base, err := batch.AwaitBase(ctx, s, baseJob.ID, poll)
	if err != nil {
		summary.Aborted = true
		summary.AbortReason = err.Error()
		summary.DurationMS = time.Since(start).Milliseconds()
		return summary, nil // an aborted batch is an outcome, not a CLI error
	}
	summary.BaseArtifact = base.ArtifactHash

	// Wave 2: bind the language overlay jobs to the base artifact.
	bound, err := batch.BindOverlayJobs(ids.batchID, overlays, base)
	if err != nil {
		summary.DurationMS = time.Since(start).Milliseconds()
		return summary, fmt.Errorf("bind overlay wave: %w", err)
	}
	overlayRes, err := batch.SubmitAll(ctx, s, bound)
	summary.OverlayWave = &overlayRes
	for _, j := range bound {
		summary.OverlayJobs = append(summary.OverlayJobs, j.ID)
	}
	summary.DurationMS = time.Since(start).Milliseconds()
	return summary, err
}
