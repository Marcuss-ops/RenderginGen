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
	"os"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// storeArtifact reads the rendered output, hashes it (sha256), stores it in
// the artifact store (L3), and records the artifact ledger row — the plan's
// "DB artifact" step — returning the artifact metadata for queue completion.
// The pipeline invariant local_sha == objectstore_sha == db_sha is enforced
// here: the record is keyed by the same hash the object store accepted.
type storeArtifactInput struct {
	jobID            string
	outputPath       string
	outcome          receiptOutcome
	tail             *stagedUpload
	plan             *overlay.Plan
	phaseMetrics     map[string]float64
	totalStart       time.Time
	probe            *media.ProbeResult
	stats            overlay.Stats
	inputBytes       int64
	copyEligible     bool
	effectiveBackend string
	nativeCertified  bool
	segmentEntries   []SegmentArtifact
}

func (p *Processor) storeArtifact(ctx context.Context, in storeArtifactInput) (queue.Artifact, error) {
	phaseStart := time.Now()
	defer func() {
		in.phaseMetrics[metricnames.PublishMS] = float64(time.Since(phaseStart).Microseconds()) / 1000
		in.phaseMetrics[metricnames.TotalMS] = float64(time.Since(in.totalStart).Microseconds()) / 1000
		in.phaseMetrics[metricnames.TotalUS] = in.phaseMetrics[metricnames.TotalMS] * 1000
		p.recordPhase(metricnames.PublishStem, phaseStart)
	}()
	fileInfo, err := os.Stat(in.outputPath)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: stat output %s: %w", in.outputPath, err)
	}
	hash, staged, tail, err := p.resolveStoreIdentity(in, fileInfo)
	if err != nil {
		return queue.Artifact{}, err
	}
	recordReceiptMetrics(in)
	if err := p.publishArtifactBytes(ctx, in, hash, staged, tail, fileInfo); err != nil {
		return queue.Artifact{}, err
	}
	artifact := p.newStoredArtifact(in, hash, fileInfo)
	if err := attachBatchSegments(&artifact, in); err != nil {
		return queue.Artifact{}, err
	}
	// Total time must be set before the ledger row is written.
	in.phaseMetrics[metricnames.TotalUS] = float64(time.Since(in.totalStart).Microseconds())
	chrononTelemetry, err := p.ingestChrononTelemetry(in, &artifact)
	if err != nil {
		return queue.Artifact{}, err
	}
	logChrononItemMetrics(in.jobID, in.phaseMetrics)
	p.preserveRawTimingSidecar(ctx, &artifact, in.outputPath, in.jobID)
	if artifact, err = p.recordArtifact(ctx, in.jobID, artifact, in.probe, in.stats, in.inputBytes, chrononTelemetry); err != nil {
		return queue.Artifact{}, err
	}
	return artifact, nil
}

// resolveArtifactIdentity establishes the one address the object store, the
// ledger and the returned artifact all use. A staged upload supplies it from
// the stream; otherwise the verification gate's receipt identity is used, or
// the bytes are hashed directly when the gate had no usable claim.
func (p *Processor) resolveStoreIdentity(in storeArtifactInput, fileInfo os.FileInfo) (hash string, staged bool, tail *stagedUpload, err error) {
	tail = in.tail
	if tail != nil {
		tail.Finish()
		stagedSHA, streamedSize, waitErr := tail.Wait()
		if waitErr != nil {
			// Transport failure: the pending bytes were discarded and nothing
			// is installed, so the classic hash-then-PUT path can still publish
			// the same file. An optimization failing must not fail a render.
			workerlog.ByJobID(in.jobID).Warnf("staged upload failed; falling back to hash-then-PUT: %v", waitErr)
			tail = nil
		} else if streamedSize != fileInfo.Size() {
			return "", false, nil, fmt.Errorf("processor: staged upload streamed %d bytes but %s is %d bytes: the file changed under the upload", streamedSize, in.outputPath, fileInfo.Size())
		} else if in.outcome.Identity.SHA256 != "" && in.outcome.Identity.SHA256 != stagedSHA {
			return "", false, nil, fmt.Errorf("processor: staged upload digest %s does not match the %s identity %s: the stream and the receipt describe different bytes", stagedSHA, in.outcome.Identity.Source, in.outcome.Identity.SHA256)
		} else {
			hash, staged = stagedSHA, true
			workerlog.ByJobID(in.jobID).Infof("artifact identity from the staged upload stream (bytes=%d, upload=%.0fms)", streamedSize, tail.Duration().Seconds()*1000)
		}
	}
	if staged {
		return hash, true, tail, nil
	}
	shaStart := time.Now()
	if in.outcome.Identity.SHA256 != "" {
		hash = in.outcome.Identity.SHA256
		workerlog.ByJobID(in.jobID).Infof("artifact identity %s (proven=%t, bytes=%d)", in.outcome.Identity.Source, in.outcome.Identity.Proven, fileInfo.Size())
	} else {
		// No receipt (or an unreadable/unusable one): identity must be proven
		// by re-reading the output. This costs a full SHA-256 pass on the
		// critical path, so it is recorded and observed on the phase histogram.
		workerlog.ByJobID(in.jobID).Warnf("hashing output directly (%s)", in.outcome.Identity.Source)
		digest, _, verifyErr := hashio.File(in.outputPath)
		if verifyErr != nil {
			return "", false, nil, fmt.Errorf("processor: hash output %s: %w", in.outputPath, verifyErr)
		}
		hash = digest
	}
	shaUS := float64(time.Since(shaStart).Microseconds())
	in.phaseMetrics[metricnames.SHA256US] = shaUS
	in.phaseMetrics[metricnames.SHA256MS] = shaUS / 1000
	p.recordPhase(metricnames.SHA256Stem, shaStart)
	return hash, false, tail, nil
}

func recordReceiptMetrics(in storeArtifactInput) {
	if in.outcome.Err != nil {
		return
	}
	for key, value := range in.outcome.Receipt.ReceiptTimingMetrics() {
		in.phaseMetrics[key] = value
	}
	for key, value := range in.outcome.Receipt.VerificationMetrics() {
		in.phaseMetrics[key] = value
	}
}

func (p *Processor) publishArtifactBytes(ctx context.Context, in storeArtifactInput, hash string, staged bool, tail *stagedUpload, fileInfo os.FileInfo) error {
	putStart := time.Now()
	if staged {
		// The bytes are already in the store under hash; record the upload's
		// own wall time, which ran concurrently with the render.
		putUS := float64(tail.Duration().Microseconds())
		in.phaseMetrics[metricnames.ObjectStoreUploadUS] = putUS
		in.phaseMetrics[metricnames.ObjectStoreUploadMS] = putUS / 1000
		return nil
	}
	output, err := os.Open(in.outputPath)
	if err != nil {
		return fmt.Errorf("processor: open output for upload %s: %w", in.outputPath, err)
	}
	if err := p.store.PutReader(ctx, hash, output, fileInfo.Size()); err != nil {
		output.Close()
		return fmt.Errorf("processor: publish artifact: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("processor: close output after upload %s: %w", in.outputPath, err)
	}
	putUS := float64(time.Since(putStart).Microseconds())
	in.phaseMetrics[metricnames.ObjectStoreUploadUS] = putUS
	in.phaseMetrics[metricnames.ObjectStoreUploadMS] = putUS / 1000
	return nil
}

func (p *Processor) newStoredArtifact(in storeArtifactInput, hash string, fileInfo os.FileInfo) queue.Artifact {
	metadata := planMetadataOf(in.plan)
	// The configured backend is only a fallback for callers that never
	// resolved a policy (imports, prepare artifacts). A render publishes the
	// backend the policy actually executed.
	publishedBackend := in.effectiveBackend
	if publishedBackend == "" {
		publishedBackend = p.backend
	}
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
		Backend:        publishedRenderBackend(publishedBackend, in.nativeCertified),
		ChrononVersion: p.chrononVersion,
		Metrics:        in.phaseMetrics,
		ProfileID:      metadata.ProfileID,
	}
	if in.probe != nil {
		probe := in.probe
		artifact.Container = probe.Container
		artifact.PixelFormat = probe.PixelFormat
		artifact.AudioStreams = probe.AudioStreams
		artifact.Codec = probe.VideoCodec
		artifact.CodecProfile = probe.CodecProfile
		artifact.OutputFacts = outputFactsFromProbe(probe)
		artifact.FrameCount = probe.FrameCount
		artifact.FirstFrameKeyframe = probe.FirstFrameKeyframe
		// closed_gop is certified from the full keyframe cadence, never copied
		// from first_frame_keyframe.
		artifact.ClosedGOP = probe.ClosedGOP
		if probe.ClosedGOPUncertifiable {
			in.phaseMetrics[metricnames.ClosedGOPUncertifiable] = 1
		}
		artifact.Width, artifact.Height = probe.Width, probe.Height
		artifact.FPSNum, artifact.FPSDen = probe.FPSNum, probe.FPSDen
		artifact.DurationUS = probe.DurationUS
		artifact.CopyEligible = in.copyEligible
	}
	return artifact
}

func attachBatchSegments(artifact *queue.Artifact, in storeArtifactInput) error {
	if len(in.segmentEntries) == 0 {
		return nil
	}
	if artifact.OutputFacts == nil {
		return fmt.Errorf("processor: segmented batch %s has no certified output facts to carry its %d per-segment entries", in.jobID, len(in.segmentEntries))
	}
	artifact.OutputFacts.BatchSegments = batchSegmentFacts(in.segmentEntries)
	return nil
}

func (p *Processor) ingestChrononTelemetry(in storeArtifactInput, artifact *queue.Artifact) (json.RawMessage, error) {
	var chrononTelemetry json.RawMessage
	raw, schema, err := chronon.ReadTelemetrySummaryWithSchema(in.outputPath)
	if err != nil {
		in.phaseMetrics[metricnames.ChrononTelemetryMissing] = 1
		workerlog.ByJobID(in.jobID).Warnf("chronon telemetry sidecar unavailable: %v", err)
		return nil, nil
	}
	chrononTelemetry = raw
	artifact.ChrononTelemetry = raw
	in.phaseMetrics[metricnames.ChrononTelemetrySourceTiming] = 1
	in.phaseMetrics[metricnames.ChrononTelemetrySchemaRead] = float64(chronon.TelemetrySchemaCode(schema))
	if schema == chronon.TimingSidecarSchemaV2 {
		workerlog.ByJobID(in.jobID).Warnf("reading previous Chronon telemetry schema %s; support window ends with the next release", schema)
	}
	mergeTelemetrySummaryMetrics(in.phaseMetrics, raw)
	if err := verifyCompositionPrediction(in.plan, raw, in.phaseMetrics, p.receiptVerifyLevel(), in.jobID); err != nil {
		return nil, err
	}
	return chrononTelemetry, nil
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
		workerlog.ByJobID(jobID).Warnf("raw timing sidecar unavailable for preservation: %v", err)
		return
	}
	if size == 0 {
		noteTimingSidecarMissing(artifact)
		workerlog.ByJobID(jobID).Warnf("raw timing sidecar empty; skipping preservation")
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
	artifact.Metrics[metricnames.ChrononTimingPreserved] = 1
	artifact.Metrics[metricnames.ChrononTimingBytes] = float64(size)
	p.preserveTimingSummarySidecar(ctx, artifact, outputPath+".timing.json", hash, jobID)
	workerlog.ByJobID(jobID).Infof("raw timing sidecar preserved (sha256=%s bytes=%d)", hash, size)
}

// timingSummarySuffix names the compact companion stored next to the raw
// sidecar. It is derived from the raw content address, so a consumer that has
// the raw reference can fetch the summary without a second queue field.
const timingSummarySuffix = ".summary.json"

// timingSummaryMaxSourceBytes bounds the one read needed to build the summary.
// The raw document is normally a few MiB; a larger file is left raw-only rather
// than turning a diagnostic optimization into an unbounded allocation.
const timingSummaryMaxSourceBytes = 64 << 20

// preserveTimingSummarySidecar writes the compact projection of the raw
// timing sidecar next to it, under `<raw-key>.summary.json`. The summary is the
// same JSON object minus the unbounded per-frame array; it keeps every other
// section (schema, phases, memory, video) so the producer can project metrics
// without downloading the raw document. Fail-open: the raw artifact is already
// published, and a missing summary only means the consumer falls back to raw.
func (p *Processor) preserveTimingSummarySidecar(ctx context.Context, artifact *queue.Artifact, path, rawHash, jobID string) {
	if p == nil || artifact == nil || rawHash == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return
	}
	if info.Size() > timingSummaryMaxSourceBytes {
		workerlog.ByJobID(jobID).Warnf("timing summary skipped: raw sidecar is %d bytes (cap %d)", info.Size(), int64(timingSummaryMaxSourceBytes))
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		workerlog.ByJobID(jobID).Warnf("timing summary read failed: %v", err)
		return
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		workerlog.ByJobID(jobID).Warnf("timing summary parse failed: %v", err)
		return
	}
	if _, present := document["frame_times_ms"]; !present {
		// Nothing to omit: do not publish a duplicate of the raw document.
		return
	}
	delete(document, "frame_times_ms")
	document["frame_times_omitted"] = json.RawMessage("true")
	summary, err := json.Marshal(document)
	if err != nil {
		workerlog.ByJobID(jobID).Warnf("timing summary marshal failed: %v", err)
		return
	}
	key := rawHash + timingSummarySuffix
	if err := p.store.PutReader(ctx, key, bytes.NewReader(summary), int64(len(summary))); err != nil {
		workerlog.ByJobID(jobID).Warnf("timing summary store failed: %v", err)
		return
	}
	if artifact.Metrics == nil {
		artifact.Metrics = map[string]float64{}
	}
	artifact.Metrics[metricnames.ChrononTimingSummaryPreserved] = 1
	artifact.Metrics[metricnames.ChrononTimingSummaryBytes] = float64(len(summary))
	workerlog.ByJobID(jobID).Infof("timing summary preserved (key=%s bytes=%d)", key, len(summary))
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

// logChrononItemMetrics makes the per-item cost readable even when the parent
// job dies after the overlay stage (the r17b case: the final job returned 422,
// so the run result never exposed overlay_render.items). The values are the
// same documented Chronon projection already merged onto the artifact; this is
// the worker-log carrier, not a second calculation.
func logChrononItemMetrics(jobID string, metrics map[string]float64) {
	upload, ok := metrics[metricnames.ChrononGPUUploadBytes]
	if !ok {
		return
	}
	renderMS := metrics[metricnames.RenderMS]
	meanMS, hasMean := metrics[metricnames.ChrononSummaryMeanFrameMS]
	// PERFLIVE-01: the cumulative upload is device-scoped on the persistent
	// daemon. Log the per-JOB delta next to it (and the fallback delta) so one
	// item's texture cost is distinguishable from a canvas/effect-stack
	// fallback without re-deriving anything from the monotonic total.
	delta := "not_reported"
	if value, present := metrics[metricnames.ChrononGPUUploadBytesDelta]; present {
		delta = fmt.Sprintf("%.0f", value)
	}
	fallback := "-"
	if value, present := metrics[metricnames.ChrononSoftwareFallbackFramesDelta]; present {
		fallback = fmt.Sprintf("%.0f", value)
	}
	if hasMean {
		workerlog.ByJobID(jobID).Infof("item_metrics render_ms=%.1f gpu_upload_bytes=%.0f gpu_upload_bytes_delta=%s software_fallback_frames_delta=%s mean_frame_ms=%.3f", renderMS, upload, delta, fallback, meanMS)
		return
	}
	workerlog.ByJobID(jobID).Infof("item_metrics render_ms=%.1f gpu_upload_bytes=%.0f gpu_upload_bytes_delta=%s software_fallback_frames_delta=%s mean_frame_ms=not_reported", renderMS, upload, delta, fallback)
}
