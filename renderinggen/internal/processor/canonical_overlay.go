// canonical_overlay.go owns the final, deterministic encode of the
// canonicalized output profiles: VELOX_OVERLAY_VIDEO_ONLY_V3 (video-only
// overlays) and VELOX_ASSEMBLY_READY_V1 (assembly-ready clips with audio).
//
// A canonicalized profile does not merely declare the canonical stream facts:
// it must EMIT them. The native NVENC lane produces a different avcC than the
// canonical x264 lane (the live clip failure: avcC 111186fe…), and the
// historical overlay lane emitted 1/12288. This pass therefore re-encodes the
// artifact with the exact canonical x264 recipe, verifies the resulting bytes
// with ffprobe against the requested profile, and writes a receipt
// that describes the FINAL bytes (the renderer's original receipt describes the
// pre-canonicalization file and must never be used after this pass).
//
// The pass is scoped to the canonicalized profiles only. V2 keeps its existing
// bytes and cache identity; changing it in place without its own certified
// recipe would invalidate previously certified overlays.
//
// Placement: the pass is CPU-only and runs in the post pool (FinalizeJob),
// after the GPU lane has been released. Running it inside RunGPU held a render
// device while re-encoding an artifact that was already finished.
//
// The three decisions have separate owners: canonical_profile.go answers "is
// this eligible and what shape is certified", canonical_encode.go owns the
// ffmpeg recipe and atomic rewrite, and canonical_receipt.go owns the identity
// check and the written receipt. This file only schedules them.
package processor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// canonicalizePreparedOutputs canonicalizes the fused output and every P4
// segment of a prepared job. It serves both canonicalized lanes: the
// video-only V3 overlay and the assembly-ready V1 clip. It returns true when
// at least one artifact was re-encoded. The pass is CPU-only and runs in the
// post pool (FinalizeJob), not on the GPU lane.
//
// A segmented batch encodes the same timeline twice (mirror + per-item
// segments): both are separate deliverables and the canonical avcC is an x264
// identity the NVENC session cannot emit. Deriving the mirror by packet-copy
// concat does remove the second encode, but it breaks the uniform 48-frame GOP
// at every seam. Measured on the dev host: the concat still certifies V3, but
// its closed-GOP cadence is lost, and downstream copy gates derive
// copy-readiness from the packet table (closed_gop, keyframe interval). The
// duplicate is therefore a CONTRACT trade, not an implementation accident:
// removing it needs either the renderer to emit the canonical x264 stream in
// one session (Chronon) or a downstream policy that accepts a non-uniform
// cadence. Do not "fix" it with a concat without that decision. The Chronon
// side is tracked in
// Chronon3d/docs/tickets/TICKET-CANONICAL-X264-SINGLE-SESSION-20261116.md.
func (p *Processor) canonicalizePreparedOutputs(ctx context.Context, prepared *PreparedJob) (bool, error) {
	if prepared == nil || prepared.Plan == nil {
		return false, nil
	}
	policy := ResolveCanonicalProfile(planMetadataOf(prepared.Plan), false)
	if err := policy.Validate(); err != nil {
		return false, err
	}
	if !policy.Eligible {
		return false, nil
	}
	paths := make([]string, 0, 1+len(prepared.Segments))
	paths = append(paths, prepared.OutputPath)
	for _, segment := range prepared.Segments {
		paths = append(paths, segment.OutputPath)
	}
	return p.canonicalizeOutputPaths(ctx, prepared.Job.ID, paths, prepared.Metrics, policy.ProfileID)
}

// canonicalizeConcurrency bounds how many canonical encodes run at once inside
// ONE job. The pass is CPU-only x264 work; the post pool already runs several
// jobs concurrently, so an unbounded fan-out over a large batch would starve
// the probes, hashing and uploads that share the same CPUs. Two lanes shorten
// the per-job tail without turning one batch into a process storm.
const canonicalizeConcurrency = 2

// canonicalizeOutputPaths canonicalizes an explicit set of outputs. The P4
// single-segment retry uses it for the one segment it re-rendered. Paths are
// deduplicated, encoded on a bounded pool, and inspected in input order, so a
// batch's output set is deterministic regardless of which encode finished
// first.
func (p *Processor) canonicalizeOutputPaths(ctx context.Context, jobID string, paths []string, metrics map[string]float64, profileID string) (bool, error) {
	unique := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		unique = append(unique, path)
	}
	if len(unique) == 0 {
		return false, nil
	}

	type canonicalizeResult struct {
		encoded bool
		err     error
	}
	results := make([]canonicalizeResult, len(unique))
	workers := len(unique)
	if workers > canonicalizeConcurrency {
		workers = canonicalizeConcurrency
	}
	passStart := time.Now()
	work := make(chan int)
	var wg sync.WaitGroup
	for lane := 0; lane < workers; lane++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range work {
				if err := ctx.Err(); err != nil {
					results[index] = canonicalizeResult{err: err}
					continue
				}
				encoded, err := canonicalizeOutputMP4(ctx, unique[index], profileID, p.acceptedCodecConfigSHA256...)
				results[index] = canonicalizeResult{encoded: encoded, err: err}
			}
		}()
	}
	for index := range unique {
		work <- index
	}
	close(work)
	wg.Wait()
	wall := time.Since(passStart)

	changed := false
	encodedCount := 0
	var failures []error
	for index, result := range results {
		if result.err != nil {
			failures = append(failures, fmt.Errorf("canonicalize %s: %w", filepath.Base(unique[index]), result.err))
			continue
		}
		if result.encoded {
			changed = true
			encodedCount++
		}
	}
	if changed {
		if metrics != nil {
			metrics[metricnames.CanonicalizeOverlayUS] = float64(wall.Microseconds())
			metrics[metricnames.CanonicalizeOverlayMS] = float64(wall.Microseconds()) / 1000
		}
		workerlog.ByJobID(jobID).Infof("canonical output emitted: %d/%d artifact(s) re-encoded to the canonical avcC (profile %s, wall=%.0fms)",
			encodedCount, len(unique), profileID, float64(wall.Microseconds())/1000)
	}
	if len(failures) > 0 {
		return changed, errors.Join(failures...)
	}
	return changed, nil
}
