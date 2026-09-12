// store_artifact.go owns the object-store publication phase: hashing the
// rendered output (preferring Chronon's receipt identity), storing the bytes
// under their content address and merging Chronon's numeric telemetry onto the
// queue artifact.
package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// storeArtifact reads the rendered output, hashes it (sha256), stores it in
// the artifact store (L3), and records the artifact ledger row — the plan's
// "DB artifact" step — returning the artifact metadata for queue completion.
// The pipeline invariant local_sha == objectstore_sha == db_sha is enforced
// here: the record is keyed by the same hash the object store accepted.
func (p *Processor) storeArtifact(ctx context.Context, jobID, outputPath string, plan *overlay.Plan, phaseMetrics map[string]float64, totalStart time.Time, probe *media.ProbeResult, stats overlay.Stats, inputBytes int64, copyEligible bool, nativeCertified bool) (queue.Artifact, error) {
	phaseStart := time.Now()
	defer func() {
		phaseMetrics["publish_ms"] = float64(time.Since(phaseStart).Microseconds()) / 1000
		phaseMetrics["total_ms"] = float64(time.Since(totalStart).Microseconds()) / 1000
		phaseMetrics[metricnames.TotalUS] = phaseMetrics[metricnames.TotalMS] * 1000
		p.recordPhase(metricnames.PublishStem, phaseStart)
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
		// No receipt (or an unreadable one): identity must be proven by
		// re-reading the output. This costs a full SHA-256 pass on the
		// critical path, so it is recorded — otherwise the spike is visible
		// only as an unexplained sha256_ms and the degradation stays
		// unattributable.
		phaseMetrics[metricnames.ChrononReceiptMissing] = 1
		log.Printf("job %s: chronon receipt unavailable (%v); hashing output directly", jobID, receiptErr)
		digest, _, verifyErr := hashio.File(outputPath)
		if verifyErr != nil {
			return queue.Artifact{}, fmt.Errorf("processor: hash output %s: %w", outputPath, verifyErr)
		}
		hash = digest
	}
	shaUS := float64(time.Since(shaStart).Microseconds())
	phaseMetrics[metricnames.SHA256US] = shaUS
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
	phaseMetrics[metricnames.ObjectStoreUploadUS] = putUS
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
		Backend:        publishedRenderBackend(p.backend, nativeCertified),
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
		if probe.ClosedGOPUncertifiable {
			// The probe never observed the sync-sample table (ffprobe missing
			// or unable to decode it): closed_gop=false here means "not
			// proven", not "open GOP". Surface it on the artifact metrics so a
			// broken probe cannot masquerade as a renderer defect forever.
			phaseMetrics["closed_gop_uncertifiable"] = 1
		}
		artifact.Width, artifact.Height = probe.Width, probe.Height
		artifact.FPSNum, artifact.FPSDen = probe.FPSNum, probe.FPSDen
		artifact.DurationUS = probe.DurationUS
		artifact.CopyEligible = copyEligible
	}
	// Total time must be set before the ledger row is written (the deferred
	// publish/total metrics above are for the artifact returned to the queue).
	phaseMetrics[metricnames.TotalUS] = float64(time.Since(totalStart).Microseconds())
	// Ingest Chronon's BOUNDED telemetry summary (observability ownership,
	// Phase 10): `<output>.telemetry-summary.json` is the only Chronon
	// telemetry surface the worker reads. It is recorded verbatim (Chronon
	// owns the schema) and only its documented numeric subset is projected.
	// A missing summary is non-fatal: the rendered bytes are still valid,
	// only the bounded telemetry blob is absent from the ledger.
	var chrononTelemetry json.RawMessage
	if raw, err := chronon.ReadTelemetrySummary(outputPath); err != nil {
		// Fail-open, but not invisible: the absence is a per-artifact metric so
		// a renderer that silently stops emitting its summary is visible in the
		// ledger and on GET /jobs/{id} instead of only in a log line.
		phaseMetrics[metricnames.ChrononTelemetryMissing] = 1
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
	hash, size, err := p.putTimingSidecar(ctx, outputPath+".timing.json")
	if err != nil {
		noteTimingSidecarMissing(artifact)
		log.Printf("job %s: raw timing sidecar unavailable for preservation: %v", jobID, err)
		return
	}
	if size == 0 {
		noteTimingSidecarMissing(artifact)
		log.Printf("job %s: raw timing sidecar empty; skipping preservation", jobID)
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

// noteTimingSidecarMissing records the fail-open absence of the raw timing
// sidecar on the artifact metrics. The bytes are already published at this
// point, so the render cannot fail; the counter is what turns "the sidecar
// silently stopped being produced" into an alertable fact.
func noteTimingSidecarMissing(artifact *queue.Artifact) {
	if artifact == nil {
		return
	}
	if artifact.Metrics == nil {
		artifact.Metrics = map[string]float64{}
	}
	artifact.Metrics[metricnames.ChrononTimingSidecarMissing] = 1
}

// timingSidecarInlineMaxBytes bounds the one-read fast path in
// putTimingSidecar. The raw sidecar carries an unbounded per-frame array, so a
// document larger than this keeps the historical constant-memory streaming path
// instead of becoming an unbounded worker allocation. Real sidecars are a few
// hundred KiB.
const timingSidecarInlineMaxBytes = 4 << 20

// putTimingSidecar stores the raw sidecar under its content address and returns
// (sha256, size); a zero size means there was nothing to store.
//
// PutReader needs the address before it can stream the body, so the historical
// shape was hash-then-reopen: two full reads of the same file for a single
// upload. A sidecar is a JSON document the renderer just wrote, so up to
// timingSidecarInlineMaxBytes the bytes are read ONCE and both the content
// address and the uploaded body come from that single read. Above the cap the
// constant-memory streaming path is kept unchanged, so an outsized document
// cannot turn a fixed-cost optimization into unbounded worker memory.
func (p *Processor) putTimingSidecar(ctx context.Context, path string) (string, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("stat: %w", err)
	}
	if info.Size() == 0 {
		return "", 0, nil
	}
	if info.Size() <= timingSidecarInlineMaxBytes {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", 0, fmt.Errorf("read: %w", err)
		}
		// len(data) rather than info.Size(): the bytes actually uploaded are the
		// authority for both the address and the recorded size, so the two can
		// never desync on a concurrent rewrite.
		size := int64(len(data))
		if size == 0 {
			return "", 0, nil
		}
		hash := storage.Hash(data)
		if err := p.store.PutReader(ctx, hash, bytes.NewReader(data), size); err != nil {
			return "", 0, fmt.Errorf("store: %w", err)
		}
		return hash, size, nil
	}
	hash, size, err := hashio.File(path)
	if err != nil {
		return "", 0, fmt.Errorf("hash: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	if err := p.store.PutReader(ctx, hash, f, size); err != nil {
		return "", 0, fmt.Errorf("store: %w", err)
	}
	return hash, size, nil
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
