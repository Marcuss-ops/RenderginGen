// store_artifact.go owns the object-store publication phase: hashing the
// rendered output (preferring Chronon's receipt identity), storing the bytes
// under their content address and merging Chronon's numeric telemetry onto the
// queue artifact.
package processor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
)

// storeArtifact reads the rendered output, hashes it (sha256), stores it in
// the artifact store (L3), and records the artifact ledger row — the plan's
// "DB artifact" step — returning the artifact metadata for queue completion.
// The pipeline invariant local_sha == objectstore_sha == db_sha is enforced
// here: the record is keyed by the same hash the object store accepted.
func (p *Processor) storeArtifact(ctx context.Context, jobID, outputPath string, plan *overlay.Plan, phaseMetrics map[string]float64, totalStart time.Time, probe *media.ProbeResult, stats overlay.Stats, inputBytes int64, copyEligible bool) (queue.Artifact, error) {
	phaseStart := time.Now()
	defer func() {
		phaseMetrics["publish_ms"] = float64(time.Since(phaseStart).Microseconds()) / 1000
		phaseMetrics["total_ms"] = float64(time.Since(totalStart).Microseconds()) / 1000
		phaseMetrics["total_us"] = phaseMetrics["total_ms"] * 1000
		p.recordPhase("publish", phaseStart)
	}()
	fileInfo, err := os.Stat(outputPath)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: stat output %s: %w", outputPath, err)
	}
	// Chronon computes the output SHA-256 while/after encoding and reports it
	// in its media receipt. Trusting that identity removes a full re-read of
	// the rendered file from the critical path; a size cross-check plus a
	// hash-verify fallback (when the receipt is missing or disagrees on size)
	// keeps the invariant local_sha == objectstore_sha == db_sha.
	var hash string
	shaStart := time.Now()
	receipt, receiptErr := chronon.ReadMediaReceipt(outputPath)
	switch {
	case receiptErr == nil && receipt.Output.Bytes == fileInfo.Size():
		hash = receipt.Output.SHA256
		log.Printf("job %s: artifact identity from chronon receipt (bytes=%d)", jobID, fileInfo.Size())
	case receiptErr == nil:
		// Receipt exists but disagrees on size: fall through to verification.
		log.Printf("job %s: chronon receipt size %d != file %d; verifying", jobID, receipt.Output.Bytes, fileInfo.Size())
		fallthrough
	default:
		digest, _, verifyErr := hashio.File(outputPath)
		if verifyErr != nil {
			return queue.Artifact{}, fmt.Errorf("processor: hash output %s: %w", outputPath, verifyErr)
		}
		hash = digest
	}
	shaUS := float64(time.Since(shaStart).Microseconds())
	phaseMetrics["sha256_us"] = shaUS
	phaseMetrics["sha256_ms"] = shaUS / 1000
	// Surface Chronon's own receipt-verification phases (probe / optional
	// decode / count_frames / sha256 / total, policy-controlled by the
	// resolved verification policy) on the artifact so per-clip reports can
	// attribute the post-render receipt cost separately from the render wall.
	// The resolved policy + aggregate status ride the same metrics namespace
	// so reports can label every run fast/normal/certify instead of inferring
	// the policy from whether receipt_decode_ms exists. Best-effort: a receipt
	// that carried no timing/verification block simply adds nothing.
	if receiptErr == nil {
		for key, value := range receipt.ReceiptTimingMetrics() {
			phaseMetrics[key] = value
		}
		for key, value := range receipt.VerificationMetrics() {
			phaseMetrics[key] = value
		}
	}
	putStart := time.Now()
	output, err := os.Open(outputPath)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: open output for upload %s: %w", outputPath, err)
	}
	if err := p.store.PutReader(ctx, hash, output, fileInfo.Size()); err != nil {
		output.Close()
		return queue.Artifact{}, fmt.Errorf("processor: publish artifact: %w", err)
	}
	if err := output.Close(); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: close output after upload %s: %w", outputPath, err)
	}
	putUS := float64(time.Since(putStart).Microseconds())
	phaseMetrics["objectstore_upload_us"] = putUS
	phaseMetrics["objectstore_upload_ms"] = putUS / 1000
	metadata := planMetadataOf(plan)
	artifact := queue.Artifact{
		Kind:           "segment",
		StorageKey:     hash,
		ArtifactURL:    p.artifactURL(hash),
		ArtifactHash:   hash,
		ContentType:    "video/mp4",
		SizeBytes:      fileInfo.Size(),
		Width:          metadata.Width,
		Height:         metadata.Height,
		FPSNum:         metadata.FPSNum,
		FPSDen:         metadata.FPSDen,
		FrameCount:     metadata.FrameCount,
		DurationUS:     metadata.DurationUS,
		Backend:        publishedRenderBackend(p.backend, p.strictNativeBackend),
		ChrononVersion: p.chrononVersion,
		Metrics:        phaseMetrics,
		ProfileID:      metadata.ProfileID,
	}
	if probe != nil {
		artifact.Container = probe.Container
		artifact.PixelFormat = probe.PixelFormat
		artifact.AudioStreams = probe.AudioStreams
		artifact.Codec = probe.VideoCodec
		artifact.CodecProfile = probe.CodecProfile
		artifact.FrameCount = probe.FrameCount
		artifact.FirstFrameKeyframe = probe.FirstFrameKeyframe
		// closed_gop is certified from the full keyframe cadence (see
		// media.ProbeResult.ClosedGOP) — it must never be a copy of
		// first_frame_keyframe: that conflates "starts cleanly" with "every
		// GOP boundary is closed and regular".
		artifact.ClosedGOP = probe.ClosedGOP
		artifact.Width, artifact.Height = probe.Width, probe.Height
		artifact.FPSNum, artifact.FPSDen = probe.FPSNum, probe.FPSDen
		artifact.DurationUS = probe.DurationUS
		artifact.CopyEligible = copyEligible
	}
	// Total time must be set before the ledger row is written (the deferred
	// publish/total metrics above are for the artifact returned to the queue).
	phaseMetrics["total_us"] = float64(time.Since(totalStart).Microseconds())
	// Ingest Chronon's BOUNDED telemetry summary (observability ownership,
	// Phase 10): `<output>.telemetry-summary.json` is the only Chronon
	// telemetry surface the worker reads. It is recorded verbatim (Chronon
	// owns the schema) and only its documented numeric subset is projected.
	// A missing summary is non-fatal: the rendered bytes are still valid,
	// only the bounded telemetry blob is absent from the ledger.
	var chrononTelemetry json.RawMessage
	if raw, err := chronon.ReadTelemetrySummary(outputPath); err != nil {
		log.Printf("job %s: chronon telemetry summary unavailable: %v", jobID, err)
	} else {
		chrononTelemetry = raw
		artifact.ChrononTelemetry = raw
		mergeTelemetrySummaryMetrics(phaseMetrics, raw)
	}
	// Preserve the RAW deep-profile sidecar verbatim (including the unbounded
	// per-frame frame_times_ms array) as an OPAQUE content-addressed artifact:
	// bytes → hash → object store → small reference. The worker never parses
	// or mutates its contents (it is Chronon-owned); the bounded summary above
	// is the ledger telemetry. Fail-open: a missing or unreadable sidecar only
	// logs.
	p.preserveRawTimingSidecar(ctx, &artifact, outputPath, jobID)
	if artifact, err = p.recordArtifact(ctx, jobID, artifact, probe, stats, inputBytes, chrononTelemetry); err != nil {
		return queue.Artifact{}, err
	}
	return artifact, nil
}

// preserveRawTimingSidecar stores the raw `<output>.timing.json` document
// (verbatim — including the per-frame frame_times_ms array that the bounded
// ledger copy deliberately drops) in the artifact store under its content
// address and records the small reference on the queue artifact:
// chronon_timing_storage_key / chronon_timing_url / chronon_timing_sha256 /
// chronon_timing_size_bytes. Best-effort by design: a missing or unreadable
// sidecar is a logged no-op (the rendered bytes and bounded telemetry remain
// valid), matching the fail-open contract of the timing-sidecar ingest.
func (p *Processor) preserveRawTimingSidecar(ctx context.Context, artifact *queue.Artifact, outputPath, jobID string) {
	if p == nil || artifact == nil {
		return
	}
	timingPath := outputPath + ".timing.json"
	data, err := os.ReadFile(timingPath)
	if err != nil {
		log.Printf("job %s: raw timing sidecar unavailable for preservation: %v", jobID, err)
		return
	}
	if len(data) == 0 {
		log.Printf("job %s: raw timing sidecar empty; skipping preservation", jobID)
		return
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	size := int64(len(data))
	if err := p.store.PutReader(ctx, hash, bytes.NewReader(data), size); err != nil {
		log.Printf("job %s: preserve raw timing sidecar: %v", jobID, err)
		return
	}
	artifact.ChrononTimingStorageKey = hash
	artifact.ChrononTimingURL = p.artifactURL(hash)
	artifact.ChrononTimingSHA256 = hash
	artifact.ChrononTimingSizeBytes = size
	artifact.ChrononTimingContentType = chronon.RawTimingSidecarContentType
	if artifact.Metrics == nil {
		artifact.Metrics = map[string]float64{}
	}
	artifact.Metrics["chronon_timing_preserved"] = 1
	artifact.Metrics["chronon_timing_bytes"] = float64(size)
	log.Printf("job %s: raw timing sidecar preserved (sha256=%s bytes=%d)", jobID, hash, size)
}

// mergeTelemetrySummaryMetrics merges the DOCUMENTED numeric subset of
// Chronon's bounded telemetry summary onto the queue artifact metrics. The
// complete summary JSON is retained in the artifact ledger; this explicit
// projection (never a generic scrape of every numeric leaf) is what callers
// such as PipelineGen receive from GET /jobs/{id}.
func mergeTelemetrySummaryMetrics(dst map[string]float64, raw json.RawMessage) {
	if dst == nil || len(raw) == 0 {
		return
	}
	for key, value := range chronon.TelemetryMetrics(raw) {
		dst[key] = value
	}
}
