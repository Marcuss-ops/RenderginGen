// Package processor implements the RenderingGen job pipeline. A claimed job is
// validated as renderinggen.job.v1, its assets are materialized into a
// per-job workspace, the render plan is written to plan.json, Chronon renders
// it, and the output is hashed and published to the artifact store. The caller
// completes or fails the job on the queue using the returned artifact/error.
package processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/artifactdb"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/workspace"
)

// Processor orchestrates a single render job:
//
//	validate -> workspace -> resolve/materialize -> plan.json -> render
//	-> hash -> storage.Put -> (caller completes) -> cleanup
type Processor struct {
	jobsRoot       string
	backend        string
	chrononVersion string
	storeURL       string
	store          *storage.Client
	renderer       chronon.Renderer
	drive          drive.Publisher     // nil = external publication disabled
	recorder       artifactdb.Recorder // nil = artifact ledger disabled

	// phaseHook, when set, receives the wall-clock duration of each pipeline
	// phase so benchmarks can report asset_fetch/prepare/render/publish ms
	// without coupling the processor to a metrics backend.
	phaseHook func(phase string, d time.Duration)

	// report, when true, passes --report to chronon3d_cli so the execution
	// report + telemetry JSONL (render_ms/encode_ms/cache hits-misses) are
	// emitted. Disabled by default; enabled by the performance benchmark.
	report               bool
	hardwareEncoder      string
	nativeOutputProfiles bool
	strictNativeBackend  bool
}

// New creates a job processor.
func New(jobsRoot, backend, chrononVersion, storeURL string, store *storage.Client, renderer chronon.Renderer) *Processor {
	return &Processor{
		jobsRoot:       jobsRoot,
		backend:        backend,
		chrononVersion: chrononVersion,
		storeURL:       strings.TrimRight(storeURL, "/"),
		store:          store,
		renderer:       renderer,
	}
}

// SetPhaseHook installs an optional callback that receives each pipeline
// phase's wall-clock duration: "materialize", "plan", "render", "publish".
// Used by the performance benchmark; nil (default) disables the overhead.
func (p *Processor) SetPhaseHook(fn func(phase string, d time.Duration)) {
	p.phaseHook = fn
}

// SetReport enables the chronon3d_cli --report flag so the engine writes its
// execution report and telemetry JSONL (render_ms, encode_ms, cache hits and
// misses). Used by the performance benchmark; off by default.
func (p *Processor) SetReport(enabled bool) {
	p.report = enabled
}

// SetHardwareEncoder selects an explicit FFmpeg hardware encoder (for
// example, nvenc). Empty/none preserves the software encoder path.
func (p *Processor) SetHardwareEncoder(encoder string) {
	p.hardwareEncoder = encoder
}

// SetNativeOutputProfiles enables passing output.profile_id to Chronon. Keep
// this disabled for legacy runtimes that reject unknown output properties; the
// worker still certifies the requested profile from the encoded MP4.
func (p *Processor) SetNativeOutputProfiles(enabled bool) { p.nativeOutputProfiles = enabled }

// SetStrictNativeBackend makes the gpu-vulkan-native profile fail closed when
// Chronon reports a hybrid or software-fallback execution. The artifact is
// rejected before object-store publication, so a receipt cannot certify the
// wrong execution path.
func (p *Processor) SetStrictNativeBackend(enabled bool) { p.strictNativeBackend = enabled }

// SetPublisher installs the Google Drive publisher used by Publish. When nil
// (the default) publication is disabled and Publish is a no-op.
func (p *Processor) SetPublisher(pub drive.Publisher) {
	p.drive = pub
}

// SetArtifactRecorder installs the artifact ledger. When set, every rendered
// job writes one ArtifactRecord (hash, probe facts, semantic counters, per-
// phase metrics) after the object store accepted the bytes; a failed Record
// fails the job (the ledger is the source of truth). nil (default) disables
// the ledger.
func (p *Processor) SetArtifactRecorder(rec artifactdb.Recorder) {
	p.recorder = rec
}

func (p *Processor) recordPhase(phase string, start time.Time) {
	if p.phaseHook == nil {
		return
	}
	p.phaseHook(phase, time.Since(start))
}

// Process runs the full pipeline (render + external publication) and returns
// the published artifact. The worker normally calls Render and Publish
// separately so a failed publication can be retried without a re-render.
func (p *Processor) Process(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	artifact, err := p.Render(ctx, job)
	if err != nil {
		return queue.Artifact{}, err
	}
	return p.Publish(ctx, job.ID, artifact)
}

// Prepare compiles and materializes an overlay plan without invoking Chronon.
// The compiled plan is stored content-addressably so a later overlay.render
// job can reuse the exact prepared surface. This is the real prepare phase:
// template resolution, asset fetch and workspace materialization happen here;
// no final audio or frozen timing is required.
func (p *Processor) Prepare(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	if err := validate(job); err != nil {
		return queue.Artifact{}, err
	}
	// PipelineGen's pre-timing contract is an OverlayIntent warm-up document,
	// not a render plan. It is submitted before audio timing exists; the later
	// overlay.render job carries the frozen Chronon plan.
	if isOverlayPrepare(job.RenderPlan) {
		if err := validateOverlayPrepare(job.RenderPlan); err != nil {
			return queue.Artifact{}, err
		}
		ws, err := workspace.New(p.jobsRoot, job.ID+"-prepare")
		if err != nil {
			return queue.Artifact{}, err
		}
		defer ws.Cleanup()
		if err := ws.Materialize(ctx, p.resolveAsset, job.Assets); err != nil {
			return queue.Artifact{}, err
		}
		hash := storage.Hash(job.RenderPlan)
		if err := p.store.Put(ctx, hash, job.RenderPlan); err != nil {
			return queue.Artifact{}, fmt.Errorf("processor: store prepared overlay intents: %w", err)
		}
		return queue.Artifact{
			Kind: "overlay_prepare", StorageKey: hash, ArtifactURL: p.artifactURL(hash),
			ArtifactHash: hash, ContentType: "application/json", SizeBytes: int64(len(job.RenderPlan)),
			Backend: p.backend, ChrononVersion: p.chrononVersion,
		}, nil
	}
	plan, compiledAssets, _, err := overlay.CompileIfSemantic(job.RenderPlan)
	if err != nil {
		return queue.Artifact{}, err
	}
	assets, err := mergeAssets(job.Assets, compiledAssets)
	if err != nil {
		return queue.Artifact{}, err
	}
	ws, err := workspace.New(p.jobsRoot, job.ID+"-prepare")
	if err != nil {
		return queue.Artifact{}, err
	}
	defer ws.Cleanup()
	if err := ws.Materialize(ctx, p.resolveAsset, assets); err != nil {
		return queue.Artifact{}, err
	}
	if err := ws.WritePlan(plan); err != nil {
		return queue.Artifact{}, err
	}
	hash := storage.Hash(plan)
	if err := p.store.Put(ctx, hash, plan); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: store prepared plan: %w", err)
	}
	return queue.Artifact{
		Kind: "overlay_prepare", StorageKey: hash, ArtifactURL: p.artifactURL(hash),
		ArtifactHash: hash, ContentType: "application/json", SizeBytes: int64(len(plan)),
		Backend: p.backend, ChrononVersion: p.chrononVersion,
	}, nil
}

// resolveAsset preserves the content-addressed invariant at the worker
// boundary. Legacy development fixtures may use symbolic keys; production
// SHA-256 keys (64 hexadecimal characters) are always checked against the
// bytes returned by the object store before Chronon sees them.
func (p *Processor) resolveAsset(ctx context.Context, hash string) ([]byte, error) {
	data, err := p.store.Get(ctx, hash)
	if err != nil {
		return nil, err
	}
	if len(hash) == 64 {
		if got := storage.Hash(data); !strings.EqualFold(got, hash) {
			return nil, fmt.Errorf("asset hash mismatch: requested %s, got %s", hash, got)
		}
	}
	return data, nil
}

func mergeAssets(jobAssets []queue.AssetRef, compiled []overlay.Asset) ([]queue.AssetRef, error) {
	result := append([]queue.AssetRef(nil), jobAssets...)
	byPath := make(map[string]string, len(result)+len(compiled))
	for _, asset := range result {
		if previous, ok := byPath[asset.LogicalPath]; ok && !strings.EqualFold(previous, asset.Hash) {
			return nil, fmt.Errorf("processor: logical asset path %q has conflicting hashes", asset.LogicalPath)
		}
		byPath[asset.LogicalPath] = asset.Hash
	}
	for _, asset := range compiled {
		if previous, ok := byPath[asset.LogicalPath]; ok {
			if !strings.EqualFold(previous, asset.Hash) {
				return nil, fmt.Errorf("processor: logical asset path %q has conflicting hashes", asset.LogicalPath)
			}
			continue
		}
		result = append(result, queue.AssetRef{Hash: asset.Hash, LogicalPath: asset.LogicalPath})
		byPath[asset.LogicalPath] = asset.Hash
	}
	return result, nil
}

// Render runs the render pipeline — validate, compile, materialize, plan.json,
// Chronon render, hash, store to the object store and record the artifact
// ledger — and returns the artifact metadata (without external publication
// fields). The artifact bytes are durably stored under artifact.StorageKey, so
// a publication retry can skip rendering entirely.
func (p *Processor) Render(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	totalStart := time.Now()
	phaseMetrics := make(map[string]float64, 6)
	record := func(phase string, start time.Time) {
		us := float64(time.Since(start).Microseconds())
		phaseMetrics[phase+"_ms"] = us / 1000
		phaseMetrics[phase+"_us"] = us
		p.recordPhase(phase, start)
	}
	if err := validate(job); err != nil {
		return queue.Artifact{}, err
	}
	// Semantic counters are persisted with the artifact ledger; legacy plans
	// simply produce zero-valued counters.
	stats, err := overlay.SemanticStats(job.RenderPlan)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: semantic stats: %w", err)
	}
	// PipelineGen may submit the semantic overlay contract. RenderingGen is
	// the boundary that resolves it into a concrete Chronon plan; legacy jobs
	// already carrying chronon.render-plan.v1 pass through unchanged.
	compileStart := time.Now()
	plan, compiledAssets, _, err := overlay.CompileIfSemantic(job.RenderPlan)
	if err != nil {
		return queue.Artifact{}, err
	}
	phaseMetrics["overlay_compile_us"] = float64(time.Since(compileStart).Microseconds())
	assets, err := mergeAssets(job.Assets, compiledAssets)
	if err != nil {
		return queue.Artifact{}, err
	}

	ws, err := workspace.New(p.jobsRoot, job.ID)
	if err != nil {
		return queue.Artifact{}, err
	}
	// Profiling jobs may retain the workspace so Chronon's frame-timing
	// sidecar can be collected after the artifact is committed. Production
	// workers keep the historical cleanup behaviour unless explicitly opted in.
	if os.Getenv("RENDERINGGEN_KEEP_WORKSPACE") != "1" {
		defer func() {
			if err := ws.Cleanup(); err != nil {
				log.Printf("job %s: workspace cleanup: %v", job.ID, err)
			}
		}()
	}

	// Resolve assets through L1/L2/L3 and materialize them to their logical
	// paths inside the workspace, so the render_plan references resolve. The
	// input byte count is the plan's input_bytes metric.
	var inputBytes int64
	resolve := func(ctx context.Context, hash string) ([]byte, error) {
		data, err := p.resolveAsset(ctx, hash)
		if err != nil {
			return nil, err
		}
		inputBytes += int64(len(data))
		return data, nil
	}
	phaseStart := time.Now()
	if err := ws.Materialize(ctx, resolve, assets); err != nil {
		return queue.Artifact{}, err
	}
	record("materialize", phaseStart)

	// Write the render plan to plan.json; Chronon reads it from disk, not
	// from the command line.
	phaseStart = time.Now()
	metadata := renderMetadataFromPlan(plan)
	renderPlan := plan
	if !p.nativeOutputProfiles && metadata.ProfileID != "" {
		renderPlan = stripOutputProfile(plan)
	}
	if err := ws.WritePlan(renderPlan); err != nil {
		return queue.Artifact{}, err
	}
	record("plan", phaseStart)

	outputPath := ws.OutputPath("result.mp4")
	phaseStart = time.Now()
	if err := p.renderer.Render(ctx, chronon.RenderRequest{
		PlanPath: ws.PlanPath(),
		// Plans use the canonical assets/<file> namespace. The workspace
		// root (not root/assets) is therefore Chronon's mounted root.
		AssetsRoot:      ws.Root(),
		OutputPath:      outputPath,
		Backend:         p.backend,
		Report:          p.report,
		HardwareEncoder: p.hardwareEncoder,
	}); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: render: %w", err)
	}
	record("render", phaseStart)
	if p.strictNativeBackend {
		if err := requireNativeVulkan(outputPath); err != nil {
			return queue.Artifact{}, fmt.Errorf("processor: gpu-vulkan-native gate: %w", err)
		}
	}
	var probe *media.ProbeResult
	if job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "" {
		probed, err := media.ProbeFile(ctx, outputPath)
		if err != nil {
			return queue.Artifact{}, fmt.Errorf("processor: overlay ffprobe: %w", err)
		}
		if metadata.ProfileID != "" {
			profile, err := media.ResolveProfile(metadata.ProfileID)
			if err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: output profile: %w", err)
			}
			if err := profile.ValidateProbe(probed); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: output profile certification: %w", err)
			}
		} else if job.JobType == queue.JobTypeOverlayRender {
			if err := probed.ValidateOverlay(metadata.Width, metadata.Height, metadata.FPS); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: overlay media contract: %w", err)
			}
		}
		probe = &probed
	}

	return p.storeArtifact(ctx, job.ID, outputPath, plan, phaseMetrics, totalStart, probe, stats, inputBytes,
		job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "")
}

func requireNativeVulkan(outputPath string) error {
	raw, err := chronon.ReadTimingSidecar(outputPath)
	if err != nil {
		return fmt.Errorf("missing Chronon timing receipt: %w", err)
	}
	var doc struct {
		Job struct {
			GPU struct {
				EffectiveBackend string `json:"effective_backend"`
				FallbackNodes    *int64 `json:"software_fallback_nodes"`
			} `json:"gpu"`
		} `json:"job"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode Chronon timing receipt: %w", err)
	}
	if doc.Job.GPU.EffectiveBackend != "vulkan" {
		return fmt.Errorf("effective_backend=%q, want vulkan", doc.Job.GPU.EffectiveBackend)
	}
	if doc.Job.GPU.FallbackNodes == nil || *doc.Job.GPU.FallbackNodes != 0 {
		if doc.Job.GPU.FallbackNodes == nil {
			return fmt.Errorf("software_fallback_nodes missing, want 0")
		}
		return fmt.Errorf("software_fallback_nodes=%d, want 0", *doc.Job.GPU.FallbackNodes)
	}
	return nil
}

// Publish uploads an already-rendered artifact to Google Drive and returns the
// artifact updated with its Drive file ID and link. It re-reads the rendered
// bytes from the object store, so it never touches the renderer. When no
// publisher is configured it returns the artifact unchanged.
//
// The SHA-256 chain invariant (plan section "Drive") is enforced here:
//
//	local_sha == objectstore_sha == db_sha == drive_sha
//
// The bytes re-read from the object store must hash to the artifact hash the
// worker computed at render time and recorded in the ledger (store_sha ==
// db_sha) BEFORE they are uploaded; the Drive result must then report the
// same hash (drive_sha == db_sha). Any mismatch fails the publication, never
// a re-render.
func (p *Processor) Publish(ctx context.Context, jobID string, artifact queue.Artifact) (queue.Artifact, error) {
	if p.drive == nil {
		return artifact, nil
	}
	phaseStart := time.Now()
	data, err := p.store.Get(ctx, artifact.StorageKey)
	if err != nil {
		return artifact, fmt.Errorf("processor: fetch rendered artifact: %w", err)
	}
	// store_sha == db_sha: the object store must return the exact bytes the
	// worker hashed and recorded. A corrupted/mismatched object fails the
	// publication BEFORE anything is uploaded to Drive.
	storeSHA := storage.Hash(data)
	if !strings.EqualFold(storeSHA, artifact.ArtifactHash) {
		return artifact, fmt.Errorf("processor: sha invariant store/db mismatch: store_sha=%s db_sha=%s (job %s fails, re-render required)", storeSHA, artifact.ArtifactHash, jobID)
	}
	res, err := p.drive.Publish(ctx, drive.PublishRequest{
		Name:        jobID + ".mp4",
		ContentType: artifact.ContentType,
		Data:        data,
		Subfolder:   artifact.ArtifactHash,
	})
	if err != nil {
		return artifact, fmt.Errorf("processor: drive publish: %w", err)
	}
	// drive_sha == db_sha: the Drive result must report the exact same hash.
	if res.FileID == "" || res.SizeBytes != int64(len(data)) || !strings.EqualFold(res.SHA256, artifact.ArtifactHash) {
		return artifact, fmt.Errorf("processor: drive publication identity mismatch (file_id=%q size=%d sha=%q db_sha=%q)", res.FileID, res.SizeBytes, res.SHA256, artifact.ArtifactHash)
	}
	if artifact.Metrics == nil {
		artifact.Metrics = map[string]float64{}
	}
	driveUS := float64(time.Since(phaseStart).Microseconds())
	artifact.Metrics["drive_publish_ms"] = driveUS / 1000
	artifact.Metrics["drive_upload_us"] = driveUS
	artifact.DriveFileID = res.FileID
	artifact.DriveLink = res.WebViewLink
	// The ledger row already exists (written by Render); a publication retry
	// only updates the drive metric — it never touches the artifact identity.
	if updater, ok := p.recorder.(artifactdb.DriveUpdater); ok {
		if err := updater.UpdateDrive(ctx, jobID, int64(driveUS)); err != nil {
			return artifact, fmt.Errorf("processor: artifact ledger drive %s: %w", jobID, err)
		}
	}
	return artifact, nil
}

// storeArtifact reads the rendered output, hashes it (sha256), stores it in
// the artifact store (L3), and records the artifact ledger row — the plan's
// "DB artifact" step — returning the artifact metadata for queue completion.
// The pipeline invariant local_sha == objectstore_sha == db_sha is enforced
// here: the record is keyed by the same hash the object store accepted.
func (p *Processor) storeArtifact(ctx context.Context, jobID, outputPath string, plan []byte, phaseMetrics map[string]float64, totalStart time.Time, probe *media.ProbeResult, stats overlay.Stats, inputBytes int64, copyEligible bool) (queue.Artifact, error) {
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
	shaStart := time.Now()
	input, err := os.Open(outputPath)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: open output for hashing %s: %w", outputPath, err)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, input); err != nil {
		input.Close()
		return queue.Artifact{}, fmt.Errorf("processor: hash output %s: %w", outputPath, err)
	}
	if err := input.Close(); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: close output after hashing %s: %w", outputPath, err)
	}
	hash := hex.EncodeToString(digest.Sum(nil))
	phaseMetrics["sha256_us"] = float64(time.Since(shaStart).Microseconds())
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
	phaseMetrics["objectstore_upload_us"] = float64(time.Since(putStart).Microseconds())
	metadata := renderMetadataFromPlan(plan)
	artifact := queue.Artifact{
		Kind:           "segment",
		StorageKey:     hash,
		ArtifactURL:    p.artifactURL(hash),
		ArtifactHash:   hash,
		ContentType:    "video/mp4",
		SizeBytes:      fileInfo.Size(),
		Width:          metadata.Width,
		Height:         metadata.Height,
		FPSNum:         metadata.FPS,
		FPSDen:         1,
		FrameCount:     metadata.FrameCount,
		DurationUS:     metadata.DurationUS,
		Backend:        p.backend,
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
		artifact.ClosedGOP = probe.FirstFrameKeyframe
		artifact.Width, artifact.Height = probe.Width, probe.Height
		artifact.FPSNum, artifact.FPSDen = probe.FPSNum, probe.FPSDen
		artifact.DurationUS = probe.DurationUS
		artifact.CopyEligible = copyEligible
	}
	// Total time must be set before the ledger row is written (the deferred
	// publish/total metrics above are for the artifact returned to the queue).
	phaseMetrics["total_us"] = float64(time.Since(totalStart).Microseconds())
	// Ingest Chronon's timing sidecar as the source of truth for plan/graph/
	// GPU/encoder timing. A missing sidecar is non-fatal: the rendered bytes
	// are still valid, only the telemetry blob is absent from the ledger.
	var chrononTelemetry json.RawMessage
	if raw, err := chronon.ReadTimingSidecar(outputPath); err != nil {
		log.Printf("job %s: chronon timing sidecar unavailable: %v", jobID, err)
	} else {
		chrononTelemetry = raw
	}
	if err := p.recordArtifact(ctx, jobID, artifact, probe, stats, inputBytes, chrononTelemetry); err != nil {
		return queue.Artifact{}, err
	}
	return artifact, nil
}

// recordArtifact writes the artifact ledger row (the plan's "DB artifact"
// step). The record carries the content hash, the probe facts, the semantic
// counters from the compiled plan and the per-phase microsecond metrics. A
// configured recorder failing fails the job: the ledger is the source of
// truth for what the pipeline produced.
func (p *Processor) recordArtifact(ctx context.Context, jobID string, artifact queue.Artifact, probe *media.ProbeResult, stats overlay.Stats, inputBytes int64, chrononTelemetry json.RawMessage) error {
	if p.recorder == nil {
		return nil
	}
	rec := artifactdb.ArtifactRecord{
		JobID:              jobID,
		ArtifactHash:       artifact.ArtifactHash,
		StorageKey:         artifact.StorageKey,
		SizeBytes:          artifact.SizeBytes,
		ContentType:        artifact.ContentType,
		Backend:            artifact.Backend,
		ChrononVersion:     artifact.ChrononVersion,
		ProfileID:          artifact.ProfileID,
		EntityCount:        stats.EntityCount,
		ImportantPhraseCnt: stats.ImportantPhraseCnt,
		ImportantWordCnt:   stats.ImportantWordCnt,
		ImageCount:         stats.ImageCount,
		LightLeakCount:     stats.LightLeakCount,
		PresetID:           stats.PresetID,
		InputBytes:         inputBytes,
		OutputBytes:        artifact.SizeBytes,
		ChrononTelemetry:   chrononTelemetry,
		CreatedAt:          time.Now().UTC(),
	}
	if probe != nil {
		rec.Container = probe.Container
		rec.Codec = probe.VideoCodec
		rec.CodecProfile = probe.CodecProfile
		rec.PixelFormat = probe.PixelFormat
		rec.Width = probe.Width
		rec.Height = probe.Height
		rec.FPSNum = probe.FPSNum
		rec.FPSDen = probe.FPSDen
		rec.FrameCount = probe.FrameCount
		rec.DurationUS = probe.DurationUS
		rec.AudioStreams = probe.AudioStreams
		rec.FirstFrameKeyframe = probe.FirstFrameKeyframe
	}
	if m, ok := artifact.Metrics["overlay_compile_us"]; ok {
		rec.OverlayCompileUS = int64(m)
	}
	if m, ok := artifact.Metrics["materialize_us"]; ok {
		rec.AssetMaterializeUS = int64(m)
	}
	if m, ok := artifact.Metrics["render_us"]; ok {
		rec.ChrononRenderUS = int64(m)
	}
	if m, ok := artifact.Metrics["sha256_us"]; ok {
		rec.SHA256US = int64(m)
	}
	if m, ok := artifact.Metrics["objectstore_upload_us"]; ok {
		rec.ObjectStoreUploadUS = int64(m)
	}
	if m, ok := artifact.Metrics["drive_upload_us"]; ok {
		rec.DriveUploadUS = int64(m)
	}
	if m, ok := artifact.Metrics["total_us"]; ok {
		rec.TotalUS = int64(m)
	}
	if err := p.recorder.Record(ctx, rec); err != nil {
		return fmt.Errorf("processor: artifact ledger %s: %w", jobID, err)
	}
	return nil
}

type renderMetadata struct {
	Width      int
	Height     int
	FPS        int
	FrameCount int
	DurationUS int64
	ProfileID  string
}

// renderMetadataFromPlan extracts only the stable canvas facts needed by the
// artifact record. A malformed/legacy-minimal plan yields zero metadata but
// never prevents publishing the already-rendered bytes.
func renderMetadataFromPlan(raw []byte) renderMetadata {
	var doc struct {
		Canvas struct {
			Width          int   `json:"width"`
			Height         int   `json:"height"`
			FPS            int   `json:"fps"`
			DurationFrames int64 `json:"duration_frames"`
		} `json:"canvas"`
		Output struct {
			ProfileID string `json:"profile_id"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Canvas.FPS <= 0 {
		return renderMetadata{}
	}
	return renderMetadata{
		Width: doc.Canvas.Width, Height: doc.Canvas.Height, FPS: doc.Canvas.FPS,
		FrameCount: int(doc.Canvas.DurationFrames),
		DurationUS: doc.Canvas.DurationFrames * 1_000_000 / int64(doc.Canvas.FPS),
		ProfileID:  doc.Output.ProfileID,
	}
}

func stripOutputProfile(raw []byte) []byte {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return raw
	}
	if output, ok := doc["output"].(map[string]any); ok {
		delete(output, "profile_id")
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return b
}

// artifactURL returns the L3 object URL for an artifact hash.
func (p *Processor) artifactURL(hash string) string {
	if p.storeURL == "" {
		return ""
	}
	return p.storeURL + "/objects/" + hash
}
