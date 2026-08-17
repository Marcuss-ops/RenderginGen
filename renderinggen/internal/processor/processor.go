// Package processor implements the RenderingGen job pipeline. A claimed job is
// validated as renderinggen.job.v1, its assets are materialized into a
// per-job workspace, the render plan is written to plan.json, Chronon renders
// it, and the output is hashed and published to the artifact store. The caller
// completes or fails the job on the queue using the returned artifact/error.
package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

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
	drive          drive.Publisher // nil = external publication disabled

	// phaseHook, when set, receives the wall-clock duration of each pipeline
	// phase so benchmarks can report asset_fetch/prepare/render/publish ms
	// without coupling the processor to a metrics backend.
	phaseHook func(phase string, d time.Duration)

	// report, when true, passes --report to chronon3d_cli so the execution
	// report + telemetry JSONL (render_ms/encode_ms/cache hits-misses) are
	// emitted. Disabled by default; enabled by the performance benchmark.
	report               bool
	nativeOutputProfiles bool
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

// SetNativeOutputProfiles enables passing output.profile_id to Chronon. Keep
// this disabled for legacy runtimes that reject unknown output properties; the
// worker still certifies the requested profile from the encoded MP4.
func (p *Processor) SetNativeOutputProfiles(enabled bool) { p.nativeOutputProfiles = enabled }

// SetPublisher installs the Google Drive publisher used by Publish. When nil
// (the default) publication is disabled and Publish is a no-op.
func (p *Processor) SetPublisher(pub drive.Publisher) {
	p.drive = pub
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
		if err := ws.Materialize(ctx, p.store.Get, job.Assets); err != nil {
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
	assets := append([]queue.AssetRef(nil), job.Assets...)
	for _, asset := range compiledAssets {
		assets = append(assets, queue.AssetRef{Hash: asset.Hash, LogicalPath: asset.LogicalPath})
	}
	ws, err := workspace.New(p.jobsRoot, job.ID+"-prepare")
	if err != nil {
		return queue.Artifact{}, err
	}
	defer ws.Cleanup()
	if err := ws.Materialize(ctx, p.store.Get, assets); err != nil {
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

// Render runs the render pipeline — validate, compile, materialize, plan.json,
// Chronon render, hash, and store to the object store — and returns the
// artifact metadata (without external publication fields). The artifact bytes
// are durably stored under artifact.StorageKey, so a publication retry can skip
// rendering entirely.
func (p *Processor) Render(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	totalStart := time.Now()
	phaseMetrics := make(map[string]float64, 5)
	record := func(phase string, start time.Time) {
		phaseMetrics[phase+"_ms"] = float64(time.Since(start).Microseconds()) / 1000
		p.recordPhase(phase, start)
	}
	if err := validate(job); err != nil {
		return queue.Artifact{}, err
	}
	// PipelineGen may submit the semantic overlay contract. RenderingGen is
	// the boundary that resolves it into a concrete Chronon plan; legacy jobs
	// already carrying chronon.render-plan.v1 pass through unchanged.
	plan, compiledAssets, _, err := overlay.CompileIfSemantic(job.RenderPlan)
	if err != nil {
		return queue.Artifact{}, err
	}
	assets := append([]queue.AssetRef(nil), job.Assets...)
	for _, asset := range compiledAssets {
		assets = append(assets, queue.AssetRef{Hash: asset.Hash, LogicalPath: asset.LogicalPath})
	}

	ws, err := workspace.New(p.jobsRoot, job.ID)
	if err != nil {
		return queue.Artifact{}, err
	}
	defer func() {
		if err := ws.Cleanup(); err != nil {
			log.Printf("job %s: workspace cleanup: %v", job.ID, err)
		}
	}()

	// Resolve assets through L1/L2/L3 and materialize them to their logical
	// paths inside the workspace, so the render_plan references resolve.
	phaseStart := time.Now()
	if err := ws.Materialize(ctx, p.store.Get, assets); err != nil {
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
		PlanPath:   ws.PlanPath(),
		AssetsRoot: ws.AssetsRoot(),
		OutputPath: outputPath,
		Backend:    p.backend,
		Report:     p.report,
	}); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: render: %w", err)
	}
	record("render", phaseStart)
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

	return p.storeArtifact(ctx, outputPath, plan, phaseMetrics, totalStart, probe)
}

// Publish uploads an already-rendered artifact to Google Drive and returns the
// artifact updated with its Drive file ID and link. It re-reads the rendered
// bytes from the object store, so it never touches the renderer. When no
// publisher is configured it returns the artifact unchanged.
func (p *Processor) Publish(ctx context.Context, jobID string, artifact queue.Artifact) (queue.Artifact, error) {
	if p.drive == nil {
		return artifact, nil
	}
	phaseStart := time.Now()
	data, err := p.store.Get(ctx, artifact.StorageKey)
	if err != nil {
		return artifact, fmt.Errorf("processor: fetch rendered artifact: %w", err)
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
	if res.FileID == "" || res.SizeBytes != int64(len(data)) || res.SHA256 != storage.Hash(data) {
		return artifact, fmt.Errorf("processor: drive publication identity mismatch (file_id=%q size=%d hash=%q)", res.FileID, res.SizeBytes, res.SHA256)
	}
	if artifact.Metrics == nil {
		artifact.Metrics = map[string]float64{}
	}
	artifact.Metrics["drive_publish_ms"] = float64(time.Since(phaseStart).Microseconds()) / 1000
	artifact.DriveFileID = res.FileID
	artifact.DriveLink = res.WebViewLink
	return artifact, nil
}

// storeArtifact reads the rendered output, hashes it and stores it in the
// artifact store (L3), returning the artifact metadata for queue completion.
func (p *Processor) storeArtifact(ctx context.Context, outputPath string, plan []byte, phaseMetrics map[string]float64, totalStart time.Time, probe *media.ProbeResult) (queue.Artifact, error) {
	phaseStart := time.Now()
	defer func() {
		phaseMetrics["publish_ms"] = float64(time.Since(phaseStart).Microseconds()) / 1000
		phaseMetrics["total_ms"] = float64(time.Since(totalStart).Microseconds()) / 1000
		p.recordPhase("publish", phaseStart)
	}()
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: read output %s: %w", outputPath, err)
	}
	hash := storage.Hash(data)
	if err := p.store.Put(ctx, hash, data); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: publish artifact: %w", err)
	}
	metadata := renderMetadataFromPlan(plan)
	artifact := queue.Artifact{
		Kind:           "segment",
		StorageKey:     hash,
		ArtifactURL:    p.artifactURL(hash),
		ArtifactHash:   hash,
		ContentType:    "video/mp4",
		SizeBytes:      int64(len(data)),
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
		if metadata.ProfileID != "" {
			artifact.CopyEligible = true
		}
	}
	return artifact, nil
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
