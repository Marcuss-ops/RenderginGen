// finalize_job.go owns the CPU-bound post half of the staged pipeline:
// validation probes, profile certification and delegation to the store phase.
package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// FinalizeJob runs the CPU-bound post half of the render pipeline: validation
// probes, output hashing (Chronon receipt first), object-store upload and the
// artifact ledger row.
func (p *Processor) FinalizeJob(ctx context.Context, prepared *PreparedJob) (queue.Artifact, error) {
	job := prepared.Job
	metrics := prepared.Metrics
	outputPath := prepared.OutputPath
	plan := prepared.Plan
	metadata := planMetadataOf(plan)
	var probe *media.ProbeResult
	if job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "" {
		probeStart := time.Now()
		probed, err := media.ProbeFile(ctx, outputPath)
		if err != nil {
			return queue.Artifact{}, fmt.Errorf("processor: overlay ffprobe: %w", err)
		}
		probeUS := float64(time.Since(probeStart).Microseconds())
		metrics["probe_us"] = probeUS
		metrics["probe_ms"] = probeUS / 1000

		if metadata.ProfileID != "" {
			profile, err := media.ResolveProfile(metadata.ProfileID)
			if err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: output profile: %w", err)
			}
			if err := profile.ValidateProbe(probed); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: output profile certification: %w", err)
			}
		} else if job.JobType == queue.JobTypeOverlayRender {
			if err := probed.ValidateOverlay(metadata.Width, metadata.Height, metadata.FPSNum, metadata.FPSDen); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: overlay media contract: %w", err)
			}
		}
		if planHasVisualOverlay(plan) && deepVisualValidationEnabled() {
			if err := probed.ValidateVisible(ctx, outputPath); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: visual output gate: %w", err)
			}
		}
		probe = &probed
	}
	// Media decodability is verified by Chronon — the canonical verifier of
	// the artifact Chronon itself produced — inside its receipt
	// (normal/certify run the full decode passes there; fast skips re-decode
	// by design). RenderingGen requests the policy and enforces the receipt
	// result; it never re-decodes the output a second time. This runs for
	// every finalized job, independent of overlay/profile probing.
	if err := p.enforceReceiptVerification(outputPath, metrics); err != nil {
		return queue.Artifact{}, err
	}
	return p.storeArtifact(ctx, job.ID, outputPath, plan, metrics, prepared.totalStart, probe, prepared.Stats, prepared.InputBytes,
		job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "", prepared.NativeCertified)
}
