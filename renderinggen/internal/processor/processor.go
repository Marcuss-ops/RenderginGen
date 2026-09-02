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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
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
	drive          drive.Publisher
	recorder       artifactdb.Recorder

	phaseHook func(phase string, d time.Duration)

	report               bool
	hardwareEncoder      string
	nativeOutputProfiles bool
	strictNativeBackend  bool
}

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

func (p *Processor) SetPhaseHook(fn func(phase string, d time.Duration)) { p.phaseHook = fn }
func (p *Processor) SetReport(enabled bool)                        { p.report = enabled }
func (p *Processor) SetHardwareEncoder(encoder string)             { p.hardwareEncoder = encoder }
func (p *Processor) SetNativeOutputProfiles(enabled bool)          { p.nativeOutputProfiles = enabled }
func (p *Processor) SetStrictNativeBackend(enabled bool)           { p.strictNativeBackend = enabled }
func (p *Processor) SetPublisher(pub drive.Publisher)              { p.drive = pub }
func (p *Processor) SetArtifactRecorder(rec artifactdb.Recorder)   { p.recorder = rec }

func (p *Processor) recordPhase(phase string, start time.Time) {
	if p.phaseHook == nil {
		return
	}
	p.phaseHook(phase, time.Since(start))
}

func (p *Processor) Process(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	artifact, err := p.Render(ctx, job)
	if err != nil {
		return queue.Artifact{}, err
	}
	return p.Publish(ctx, job.ID, artifact)
}

// Prepare compiles and materializes an overlay plan without invoking Chronon.
// Asset materialization is path-first: L2/CAS files are hardlinked into the
// workspace instead of being loaded into []byte and rewritten.
func (p *Processor) Prepare(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	if err := validate(job); err != nil {
		return queue.Artifact{}, err
	}
	if isOverlayPrepare(job.RenderPlan) {
		if err := validateOverlayPrepare(job.RenderPlan); err != nil {
			return queue.Artifact{}, err
		}
		ws, err := workspace.New(p.jobsRoot, job.ID+"-prepare")
		if err != nil {
			return queue.Artifact{}, err
		}
		defer ws.Cleanup()
		if err := ws.MaterializePaths(ctx, p.resolveAssetPath, job.Assets); err != nil {
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
	if err := ws.MaterializePaths(ctx, p.resolveAssetPath, assets); err != nil {
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

// resolveAsset retains the compatibility byte API for tests/small fixtures.
// Production media materialization uses resolveAssetPath below.
func (p *Processor) resolveAsset(ctx context.Context, asset queue.AssetRef) ([]byte, error) {
	hash := asset.Hash
	data, err := p.store.Get(ctx, hash)
	if err == nil {
		if len(hash) == 64 {
			if got := storage.Hash(data); !strings.EqualFold(got, hash) {
				return nil, fmt.Errorf("asset hash mismatch: requested %s, got %s", hash, got)
			}
		}
		return data, nil
	}
	if !errors.Is(err, storage.ErrNotFound) || len(hash) != 64 || !isHTTPURL(asset.LogicalPath) {
		return nil, err
	}
	downloaded, err := downloadAsset(ctx, asset.LogicalPath)
	if err != nil {
		return nil, fmt.Errorf("asset %s not in store and URL download failed: %w", hash, err)
	}
	if got := storage.Hash(downloaded); !strings.EqualFold(got, hash) {
		return nil, fmt.Errorf("asset hash mismatch on URL download: requested %s, got %s", hash, got)
	}
	if err := p.store.Put(ctx, hash, downloaded); err != nil {
		return nil, fmt.Errorf("stage self-healed asset %s: %w", hash, err)
	}
	return downloaded, nil
}

// resolveAssetPath is the canonical production resolver. It returns a local
// content-addressed path and size, allowing workspace materialization to use
// hardlinks/reflink-like filesystem semantics instead of NVMe -> RAM -> NVMe.
func (p *Processor) resolveAssetPath(ctx context.Context, asset queue.AssetRef) (workspace.ResolvedAsset, error) {
	hash := asset.Hash
	path, size, err := p.store.LocalPath(ctx, hash)
	if err == nil {
		return workspace.ResolvedAsset{LocalPath: path, Size: size}, nil
	}
	if !errors.Is(err, storage.ErrNotFound) || len(hash) != 64 || !isHTTPURL(asset.LogicalPath) {
		return workspace.ResolvedAsset{}, err
	}

	// overlay.prepare self-heal: remote-only assets are downloaded once,
	// hash-verified, staged to the content store, then resolved as a path.
	downloaded, err := downloadAsset(ctx, asset.LogicalPath)
	if err != nil {
		return workspace.ResolvedAsset{}, fmt.Errorf("asset %s not in store and URL download failed: %w", hash, err)
	}
	if got := storage.Hash(downloaded); !strings.EqualFold(got, hash) {
		return workspace.ResolvedAsset{}, fmt.Errorf("asset hash mismatch on URL download: requested %s, got %s", hash, got)
	}
	if err := p.store.Put(ctx, hash, downloaded); err != nil {
		return workspace.ResolvedAsset{}, fmt.Errorf("stage self-healed asset %s: %w", hash, err)
	}
	path, size, err = p.store.LocalPath(ctx, hash)
	if err != nil {
		return workspace.ResolvedAsset{}, err
	}
	return workspace.ResolvedAsset{LocalPath: path, Size: size}, nil
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func downloadAsset(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
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

// Render runs the render pipeline. Large assets remain file-backed through
// resolve/materialize, so GPU work is not preceded by whole-file heap copies.
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
	stats, err := overlay.SemanticStats(job.RenderPlan)
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: semantic stats: %w", err)
	}
	compileStart := time.Now()
	plan, compiledAssets, _, err := overlay.CompileIfSemantic(job.RenderPlan)
	if err != nil {
		return queue.Artifact{}, err
	}
	compileUS := float64(time.Since(compileStart).Microseconds())
	phaseMetrics["overlay_compile_us"] = compileUS
	phaseMetrics["overlay_compile_ms"] = compileUS / 1000
	assets, err := mergeAssets(job.Assets, compiledAssets)
	if err != nil {
		return queue.Artifact{}, err
	}

	ws, err := workspace.New(p.jobsRoot, job.ID)
	if err != nil {
		return queue.Artifact{}, err
	}
	if os.Getenv("RENDERINGGEN_KEEP_WORKSPACE") != "1" {
		defer func() {
			if err := ws.Cleanup(); err != nil {
				log.Printf("job %s: workspace cleanup: %v", job.ID, err)
			}
		}()
	}

	var inputBytes atomic.Int64
	resolvePath := func(ctx context.Context, asset queue.AssetRef) (workspace.ResolvedAsset, error) {
		resolved, err := p.resolveAssetPath(ctx, asset)
		if err != nil {
			return workspace.ResolvedAsset{}, err
		}
		inputBytes.Add(resolved.Size)
		return resolved, nil
	}
	phaseStart := time.Now()
	if err := ws.MaterializePaths(ctx, resolvePath, assets); err != nil {
		return queue.Artifact{}, err
	}
	record("materialize", phaseStart)

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
	gpuRequired := p.strictNativeBackend ||
		(p.backend == "vulkan" && p.hardwareEncoder != "" && p.hardwareEncoder != "none")
	if err := p.renderer.Render(ctx, chronon.RenderRequest{
		PlanPath: ws.PlanPath(),
		AssetsRoot: ws.Root(),
		OutputPath: outputPath,
		Report:     p.report,
		Requirements: chronon.ExecutionRequirements{
			GPURequired:         gpuRequired,
			CPUFallbackAllowed:  !p.strictNativeBackend,
			CompositionRequired: true,
			PacketCopyAllowed:   true,
		},
		FirstFrame: frameStart(job),
		LastFrame:  frameEndInclusive(job),
		Output:     chronon.OutputSpec{Codec: "h264"},
	}); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: render: %w", err)
	}
	record("render", phaseStart)
	if p.strictNativeBackend {
		if err := requireNativeVulkan(outputPath, metadata.FrameCount); err != nil {
			return queue.Artifact{}, fmt.Errorf("processor: gpu-vulkan-native gate: %w", err)
		}
	}
	var probe *media.ProbeResult
	if job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "" {
		probeStart := time.Now()
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
			if err := probed.ValidateOverlay(metadata.Width, metadata.Height, metadata.FPSNum, metadata.FPSDen); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: overlay media contract: %w", err)
			}
		}
		if hasVisualOverlay(plan) {
			if err := probed.ValidateVisible(ctx, outputPath); err != nil {
				return queue.Artifact{}, fmt.Errorf("processor: visual output gate: %w", err)
			}
		}
		probe = &probed
		probeUS := float64(time.Since(probeStart).Microseconds())
		phaseMetrics["probe_us"] = probeUS
		phaseMetrics["probe_ms"] = probeUS / 1000
	}

	return p.storeArtifact(ctx, job.ID, outputPath, plan, phaseMetrics, totalStart, probe, stats, inputBytes.Load(),
		job.JobType == queue.JobTypeOverlayRender || metadata.ProfileID != "")
}

func frameStart(job *queue.Job) int64 {
	if job != nil && job.FrameRange != nil {
		return job.FrameRange.Start
	}
	return 0
}

func frameEndInclusive(job *queue.Job) int64 {
	if job != nil && job.FrameRange != nil {
		return job.FrameRange.End - 1
	}
	return 0
}

func hasVisualOverlay(plan []byte) bool {
	var doc struct {
		Layers []json.RawMessage `json:"layers"`
	}
	if json.Unmarshal(plan, &doc) != nil {
		return false
	}
	return len(doc.Layers) > 1
}

func requireNativeVulkan(outputPath string, expectedFrames int) error {
	raw, err := chronon.ReadTimingSidecar(outputPath)
	if err != nil {
		return fmt.Errorf("missing Chronon timing receipt: %w", err)
	}
	var doc struct {
		Job struct {
			SurfaceHandoffPath string `json:"surface_handoff_path"`
			GPU                struct {
				EffectiveBackend     string `json:"effective_backend"`
				EncoderBackend       string `json:"encoder_backend"`
				FallbackNodes        *int64 `json:"software_fallback_nodes"`
				CPUReadbackFrames    *int64 `json:"cpu_readback_frames"`
				SoftwareEncodeFrames *int64 `json:"software_encode_frames"`
				NVENCFrames          *int64 `json:"nvenc_frames"`
				VulkanFrames         *int64 `json:"vulkan_frames"`
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
	if doc.Job.GPU.EncoderBackend != "nvenc" {
		return fmt.Errorf("encoder_backend=%q, want nvenc", doc.Job.GPU.EncoderBackend)
	}
	if doc.Job.GPU.CPUReadbackFrames != nil && *doc.Job.GPU.CPUReadbackFrames > 0 {
		return fmt.Errorf("cpu_readback_frames=%d, want 0", *doc.Job.GPU.CPUReadbackFrames)
	}
	if doc.Job.GPU.SoftwareEncodeFrames != nil && *doc.Job.GPU.SoftwareEncodeFrames > 0 {
		return fmt.Errorf("software_encode_frames=%d, want 0", *doc.Job.GPU.SoftwareEncodeFrames)
	}
	if doc.Job.SurfaceHandoffPath != "vulkan_copy" && doc.Job.SurfaceHandoffPath != "direct" {
		return fmt.Errorf("surface_handoff_path=%q, want vulkan_copy or direct", doc.Job.SurfaceHandoffPath)
	}
	if expectedFrames > 0 {
		if doc.Job.GPU.NVENCFrames == nil || *doc.Job.GPU.NVENCFrames != int64(expectedFrames) {
			if doc.Job.GPU.NVENCFrames == nil {
				return fmt.Errorf("nvenc_frames missing, want %d", expectedFrames)
			}
			return fmt.Errorf("nvenc_frames=%d, want %d", *doc.Job.GPU.NVENCFrames, expectedFrames)
		}
		if doc.Job.GPU.VulkanFrames == nil || *doc.Job.GPU.VulkanFrames != int64(expectedFrames) {
			if doc.Job.GPU.VulkanFrames == nil {
				return fmt.Errorf("vulkan_frames missing, want %d", expectedFrames)
			}
			return fmt.Errorf("vulkan_frames=%d, want %d", *doc.Job.GPU.VulkanFrames, expectedFrames)
		}
	}
	receiptRaw, err := os.ReadFile(outputPath + ".receipt.json")
	if err != nil {
		return fmt.Errorf("missing Chronon media receipt: %w", err)
	}
	var receipt map[string]any
	if err := json.Unmarshal(receiptRaw, &receipt); err != nil {
		return fmt.Errorf("decode Chronon media receipt: %w", err)
	}
	return nil
}

func (p *Processor) Publish(ctx context.Context, jobID string, artifact queue.Artifact) (queue.Artifact, error) {
	if p.drive == nil {
		return artifact, nil
	}
	phaseStart := time.Now()
	path, size, err := p.store.LocalPath(ctx, artifact.StorageKey)
	if err != nil {
		return artifact, fmt.Errorf("processor: resolve rendered artifact locally: %w", err)
	}
	res, err := p.drive.Publish(ctx, drive.PublishRequest{
		Name: jobID + ".mp4", ContentType: artifact.ContentType, Path: path,
		Subfolder: artifact.ArtifactHash,
	})
	if err != nil {
		return artifact, fmt.Errorf("processor: drive publish: %w", err)
	}
	if res.FileID == "" || res.SizeBytes != size {
		return artifact, fmt.Errorf("processor: drive publication identity mismatch (file_id=%q size=%d expected_size=%d)", res.FileID, res.SizeBytes, size)
	}
	if artifact.Metrics == nil {
		artifact.Metrics = map[string]float64{}
	}
	driveUS := float64(time.Since(phaseStart).Microseconds())
	artifact.Metrics["drive_publish_ms"] = driveUS / 1000
	artifact.Metrics["drive_upload_us"] = driveUS
	artifact.DriveFileID = res.FileID
	artifact.DriveLink = res.WebViewLink
	if updater, ok := p.recorder.(artifactdb.DriveUpdater); ok {
		if err := updater.UpdateDrive(ctx, jobID, int64(driveUS)); err != nil {
			return artifact, fmt.Errorf("processor: artifact ledger drive %s: %w", jobID, err)
		}
	}
	return artifact, nil
}

// storeArtifact hashes the rendered output once and publishes it from its file
// path. PutFile then streams to L3 and hardlinks into L2 when possible; there
// is no second whole-file Go buffer or PutReader io.ReadAll pass.
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
	shaUS := float64(time.Since(shaStart).Microseconds())
	phaseMetrics["sha256_us"] = shaUS
	phaseMetrics["sha256_ms"] = shaUS / 1000

	putStart := time.Now()
	if err := p.store.PutFile(ctx, hash, outputPath); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: publish artifact: %w", err)
	}
	putUS := float64(time.Since(putStart).Microseconds())
	phaseMetrics["objectstore_upload_us"] = putUS
	phaseMetrics["objectstore_upload_ms"] = putUS / 1000
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
		FPSNum:         metadata.FPSNum,
		FPSDen:         metadata.FPSDen,
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
	phaseMetrics["total_us"] = float64(time.Since(totalStart).Microseconds())
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
	FPSNum     int
	FPSDen     int
	FrameCount int
	DurationUS int64
	ProfileID  string
}

func renderMetadataFromPlan(raw []byte) renderMetadata {
	var doc struct {
		Canvas struct {
			Width          int   `json:"width"`
			Height         int   `json:"height"`
			FPSNum         int   `json:"fps_num"`
			FPSDen         int   `json:"fps_den"`
			DurationFrames int64 `json:"duration_frames"`
		} `json:"canvas"`
		Output struct {
			ProfileID string `json:"profile_id"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Canvas.FPSNum <= 0 || doc.Canvas.FPSDen <= 0 {
		return renderMetadata{}
	}
	return renderMetadata{
		Width: doc.Canvas.Width, Height: doc.Canvas.Height,
		FPSNum: doc.Canvas.FPSNum, FPSDen: doc.Canvas.FPSDen,
		FrameCount: int(doc.Canvas.DurationFrames),
		DurationUS: doc.Canvas.DurationFrames * 1_000_000 * int64(doc.Canvas.FPSDen) / int64(doc.Canvas.FPSNum),
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

func (p *Processor) artifactURL(hash string) string {
	if p.storeURL == "" {
		return ""
	}
	return p.storeURL + "/objects/" + hash
}
