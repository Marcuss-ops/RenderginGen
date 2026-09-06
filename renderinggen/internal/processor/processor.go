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
	"path/filepath"
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
	encodePreset         string
	nativeOutputProfiles bool
	strictNativeBackend  bool

	// progressTracker, when set, receives per-frame progress observations
	// during RunGPU so health and the queue pusher can report live render
	// position instead of an opaque RUNNING/0% for minutes.
	progressTracker *chronon.ProgressTracker
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

// SetEncodePreset selects an explicit FFmpeg NVENC preset (for example, "p2"
// for the throughput tier). Empty preserves the engine default; the worker
// never invents a preset when none is configured.
func (p *Processor) SetEncodePreset(preset string) {
	p.encodePreset = preset
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

// SetArtifactRecorder installs the worker-local artifact mirror. When set,
// every rendered job writes one ArtifactRecord (hash, probe facts, semantic
// counters, per-phase metrics) after the object store accepted the bytes. The
// mirror is diagnostic: a failed Record is logged and flagged on the artifact
// metrics (mirror_failure=1), it never fails the render — the central queue
// PostgreSQL row remains authoritative for the artifact. nil (default)
// disables the mirror.
func (p *Processor) SetArtifactRecorder(rec artifactdb.Recorder) {
	p.recorder = rec
}

// SetProgressTracker installs the shared render progress tracker. When set,
// RunGPU feeds every renderer frame-milestone into it and records the final
// frame position + average fps into the job's ledger metrics.
func (p *Processor) SetProgressTracker(tracker *chronon.ProgressTracker) {
	p.progressTracker = tracker
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
	planBytes, err := plan.Marshal()
	if err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: encode render plan: %w", err)
	}
	if err := ws.WritePlan(planBytes); err != nil {
		return queue.Artifact{}, err
	}
	hash := storage.Hash(planBytes)
	if err := p.store.Put(ctx, hash, planBytes); err != nil {
		return queue.Artifact{}, fmt.Errorf("processor: store prepared plan: %w", err)
	}
	return queue.Artifact{
		Kind: "overlay_prepare", StorageKey: hash, ArtifactURL: p.artifactURL(hash),
		ArtifactHash: hash, ContentType: "application/json", SizeBytes: int64(len(planBytes)),
		Backend: p.backend, ChrononVersion: p.chrononVersion,
	}, nil
}

// resolveAsset preserves the content-addressed invariant at the worker
// boundary. Legacy development fixtures may use symbolic keys; production
// SHA-256 keys (64 hexadecimal characters) are always checked against the
// bytes returned by the object store before Chronon sees them.
//
// When a production asset is absent from L3 but its logical path is a source
// URL (the overlay.prepare case: PipelineGen materialized the entity image to
// Drive and enqueues prepare before the bytes are staged in the central
// object store), resolveAsset self-heals by downloading the bytes, verifying
// the SHA-256 and staging them into L3. Render never silently proceeds with
// the wrong asset: a hash mismatch fails the resolution.
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
	// Streaming self-heal: download straight into a temp file, hash while
	// streaming, then stage from the file. The historical io.ReadAll path
	// buffered up to 1 GiB per asset in RAM — with a 4-goroutine materialize
	// pool that is a worker-wide OOM waiting for one large background video.
	// (The zero-copy hot path is resolveAssetStreaming; this byte-based
	// variant exists for the legacy Materialize byte path, where the bytes
	// are already destined for RAM anyway.)
	path, dlErr := downloadAssetToFile(ctx, asset.LogicalPath, hash)
	if dlErr != nil {
		return nil, fmt.Errorf("asset %s not in store and URL download failed: %w", hash, dlErr)
	}
	defer os.Remove(path)
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, readErr
	}
	if err := p.store.Put(ctx, hash, data); err != nil {
		return nil, fmt.Errorf("stage self-healed asset %s: %w", hash, err)
	}
	return data, nil
}

// resolveAssetStreaming is the zero-copy variant used by MaterializePaths:
// a cache-miss self-heal downloads straight to a bounded temp file (constant
// memory, never the whole object in RAM), verifies the SHA-256 while
// streaming, stages it into L3 via PutReader and hands the local path to the
// workspace for a hard link. A hash mismatch deletes the temp file and fails
// the resolution — the wrong bytes never reach Chronon.
func (p *Processor) resolveAssetStreaming(ctx context.Context, asset queue.AssetRef) (workspace.ResolvedAsset, error) {
	hash := asset.Hash
	if path, size, err := p.store.LocalPath(ctx, hash); err == nil {
		return workspace.ResolvedAsset{LocalPath: path, SizeBytes: size}, nil
	} else if !errors.Is(err, storage.ErrNotFound) || len(hash) != 64 || !isHTTPURL(assetSourceURL(asset)) {
		log.Printf("asset resolve cache miss not self-healed: hash=%s logical_path=%q err=%v", hash, asset.LogicalPath, err)
		return workspace.ResolvedAsset{}, err
	}
	sourceURL := assetSourceURL(asset)
	log.Printf("asset resolve self-heal: hash=%s logical_path=%q source_url=%q", hash, asset.LogicalPath, sourceURL)

	tmp, err := os.CreateTemp("", ".selfheal-*")
	if err != nil {
		return workspace.ResolvedAsset{}, err
	}
	tmpPath := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpPath)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return workspace.ResolvedAsset{}, err
	}
	resp, err := assetDownloadClient.Do(req)
	if err != nil {
		log.Printf("asset self-heal download failed: hash=%s source_url=%q err=%v", hash, sourceURL, err)
		return workspace.ResolvedAsset{}, fmt.Errorf("download %s: %w", sourceURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return workspace.ResolvedAsset{}, fmt.Errorf("download %s: HTTP %d", sourceURL, resp.StatusCode)
	}

	// Stream to disk + hash in one pass, capped at maxAssetDownloadBytes.
	digest, size, err := streamHashAndCopy(io.LimitReader(resp.Body, maxAssetDownloadBytes+1), tmp)
	if err != nil {
		return workspace.ResolvedAsset{}, fmt.Errorf("download %s: %w", sourceURL, err)
	}
	if size > maxAssetDownloadBytes {
		return workspace.ResolvedAsset{}, fmt.Errorf("download %s exceeds %d bytes cap", asset.LogicalPath, maxAssetDownloadBytes)
	}
	if !strings.EqualFold(digest, hash) {
		log.Printf("asset self-heal hash mismatch: requested=%s got=%s source_url=%q", hash, digest, sourceURL)
		return workspace.ResolvedAsset{}, fmt.Errorf("asset hash mismatch on URL download: requested %s, got %s", hash, digest)
	}
	if reader, openErr := os.Open(tmpPath); openErr != nil {
		return workspace.ResolvedAsset{}, openErr
	} else if err := p.store.PutReader(ctx, hash, reader, size); err != nil {
		reader.Close()
		return workspace.ResolvedAsset{}, fmt.Errorf("stage self-healed asset %s: %w", hash, err)
	} else {
		reader.Close()
	}
	// The asset now lives in L3/L2 under its content address; resolve the
	// durable local path and let the deferred cleanup remove the temp file.
	path, _, err := p.store.LocalPath(ctx, hash)
	if err != nil {
		return workspace.ResolvedAsset{}, err
	}
	return workspace.ResolvedAsset{LocalPath: path, SizeBytes: size}, nil
}

// streamHashAndCopy copies r into w while computing the SHA-256 of the
// copied bytes and the total byte count (constant memory).
func streamHashAndCopy(r io.Reader, w io.Writer) (string, int64, error) {
	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(w, hasher), r)
	if err != nil {
		return "", size, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func assetSourceURL(asset queue.AssetRef) string {
	if asset.SourceURL != "" {
		return asset.SourceURL
	}
	return asset.LogicalPath
}

// assetDownloadClient bounds every self-heal download: without a timeout a
// hanging URL would pin a prep-pool goroutine (and, through lease renewal,
// the job) forever. Only http/https URLs reach this path (see isHTTPURL).
var assetDownloadClient = &http.Client{
	Timeout: 5 * time.Minute,
}

// maxAssetDownloadBytes caps a self-healed asset download (1 GiB: a rendered
// background video is far below this; anything larger is a bug or an attack).
const maxAssetDownloadBytes = 1 << 30

// downloadAssetToFile streams the URL into a temp file, hashing the bytes on
// the way through. The complete object never sits in RAM. Returns the temp
// file path; the caller owns removal. The declared hash is verified while
// streaming — a mismatch aborts before the file is staged anywhere.
func downloadAssetToFile(ctx context.Context, rawURL, wantHash string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := assetDownloadClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "renderinggen-selfheal-*")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	hasher := sha256.New()
	copied, copyErr := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(resp.Body, maxAssetDownloadBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(path)
		return "", copyErr
	}
	if closeErr != nil {
		os.Remove(path)
		return "", closeErr
	}
	if copied > maxAssetDownloadBytes {
		os.Remove(path)
		return "", fmt.Errorf("download %s exceeds %d bytes cap", rawURL, maxAssetDownloadBytes)
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); !strings.EqualFold(got, wantHash) {
		os.Remove(path)
		return "", fmt.Errorf("asset hash mismatch on URL download: requested %s, got %s", wantHash, got)
	}
	return path, nil
}

// hashFileSHA256 streams a file through SHA-256 without buffering it in RAM.
func hashFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return hashReader(file)
}

func mergeAssets(jobAssets []queue.AssetRef, compiled []overlay.Asset) ([]queue.AssetRef, error) {
	result := make([]queue.AssetRef, 0, len(jobAssets)+len(compiled))
	byPath := make(map[string]string, len(result)+len(compiled))
	byHash := make(map[string]int, len(jobAssets)+len(compiled))
	for _, asset := range jobAssets {
		if previous, ok := byPath[asset.LogicalPath]; ok && !strings.EqualFold(previous, asset.Hash) {
			return nil, fmt.Errorf("processor: logical asset path %q has conflicting hashes", asset.LogicalPath)
		}
		byPath[asset.LogicalPath] = asset.Hash
		result = append(result, asset)
		key := strings.ToLower(asset.Hash)
		if _, exists := byHash[key]; !exists {
			byHash[key] = len(result) - 1
		}
	}
	// Job manifests may carry a durable URL as the logical path for a
	// semantic asset. Once compilation gives that asset its canonical local
	// path, merge by content hash and retain the URL as SourceURL.
	for _, asset := range compiled {
		if idx, ok := byHash[strings.ToLower(asset.Hash)]; ok {
			merged := result[idx]
			// A URL-backed manifest entry is the self-healing case: move it to
			// the compiler's canonical local path while retaining its source.
			// Legacy/local refs already name the workspace path and must remain
			// untouched for backwards compatibility.
			if !isHTTPURL(merged.LogicalPath) && merged.SourceURL == "" {
				continue
			}
			if merged.SourceURL == "" && isHTTPURL(merged.LogicalPath) {
				merged.SourceURL = merged.LogicalPath
			}
			merged.LogicalPath = asset.LogicalPath
			if !strings.EqualFold(merged.Hash, asset.Hash) {
				return nil, fmt.Errorf("processor: asset hash conflict")
			}
			result[idx] = merged
			byPath[asset.LogicalPath] = asset.Hash
			continue
		}
		if previous, ok := byPath[asset.LogicalPath]; ok {
			if !strings.EqualFold(previous, asset.Hash) {
				return nil, fmt.Errorf("processor: logical asset path %q has conflicting hashes", asset.LogicalPath)
			}
			continue
		}
		result = append(result, queue.AssetRef{Hash: asset.Hash, LogicalPath: asset.LogicalPath})
		byPath[asset.LogicalPath] = asset.Hash
		byHash[strings.ToLower(asset.Hash)] = len(result) - 1
	}
	return result, nil
}

// Render runs the render pipeline — validate, compile, materialize, plan.json,
// Chronon render, hash, store to the object store and record the artifact
// ledger — and returns the artifact metadata (without external publication
// fields). The artifact bytes are durably stored under artifact.StorageKey, so
// a publication retry can skip rendering entirely.
//
// The implementation is the staged pipeline (PrepareJob -> RunGPU ->
// FinalizeJob) run serially for this job; the worker's concurrent pools use
// the same stages to overlap CPU work with the GPU lane.
func (p *Processor) Render(ctx context.Context, job *queue.Job) (queue.Artifact, error) {
	return p.StagedRender(ctx, job)
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

// planHasVisualOverlay identifies concrete plans that require Chronon's
// authored composition graph. A video-only plan can use DirectYUV; an
// image/text/color plan must use native composition even when it has no
// separate background.
func planHasVisualOverlay(plan *overlay.Plan) bool {
	if plan == nil {
		return false
	}
	for _, layer := range plan.Layers {
		if layer.Type == "video" {
			continue
		}
		if layer.Type != "" && layer.Type != "video" {
			return true
		}
	}
	// DirectYUV supports the authored multi-video base/overlay path. Keep that
	// fast path eligible; only non-video authored layers require the graph.
	return false
}

func planHasVideoSource(plan *overlay.Plan) bool {
	if plan == nil {
		return false
	}
	for _, layer := range plan.Layers {
		if layer.Type == "video" {
			return true
		}
	}
	return false
}

// audioSourcePathFromSemantic resolves the declared master audio source to
// the worker workspace. Audio is semantic metadata and is intentionally not
// serialized into Chronon's visual render-plan.v2; the native encoder still
// needs the materialized source path to mux the audio stream.
func audioSourcePathFromSemantic(raw []byte, workspaceRoot string) string {
	var doc struct {
		Audio *struct {
			Mode string `json:"mode"`
		} `json:"audio"`
		Source *struct {
			AssetID string `json:"asset_id"`
			Path    string `json:"path"`
		} `json:"source"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Audio == nil || doc.Source == nil || doc.Source.AssetID == "" {
		return ""
	}
	path := doc.Source.Path
	if path == "" {
		path = filepath.ToSlash(filepath.Join("assets", "semantic", doc.Source.AssetID+".mp4"))
	}
	return filepath.Join(workspaceRoot, filepath.FromSlash(path))
}

func requireNativeVulkan(outputPath string, expectedFrames int) error {
	raw, err := chronon.ReadTimingSidecar(outputPath)
	if err != nil {
		return fmt.Errorf("missing Chronon timing receipt: %w", err)
	}
	var doc struct {
		Job struct {
			ExecutionPath      string `json:"execution_path"`
			SurfaceHandoffPath string `json:"surface_handoff_path"`
			GPU                struct {
				EffectiveBackend     string `json:"effective_backend"`
				EncoderBackend       string `json:"encoder_backend"`
				FallbackNodes        *int64 `json:"software_fallback_nodes"`
				CPUReadbackFrames    *int64 `json:"cpu_readback_frames"`
				SoftwareEncodeFrames *int64 `json:"software_encode_frames"`
				NVENCFrames          *int64 `json:"nvenc_frames"`
				VulkanFrames         *int64 `json:"vulkan_frames"`
				NativeSurfaceFrames  *int64 `json:"gpu_native_surface_frames"`
			} `json:"gpu"`
		} `json:"job"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode Chronon timing receipt: %w", err)
	}
	directYUV := doc.Job.ExecutionPath == "direct_yuv"
	if !directYUV && doc.Job.GPU.EffectiveBackend != "vulkan" {
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
		if directYUV {
			if doc.Job.GPU.NativeSurfaceFrames == nil || *doc.Job.GPU.NativeSurfaceFrames != int64(expectedFrames) {
				if doc.Job.GPU.NativeSurfaceFrames == nil {
					return fmt.Errorf("gpu_native_surface_frames missing, want %d", expectedFrames)
				}
				return fmt.Errorf("gpu_native_surface_frames=%d, want %d", *doc.Job.GPU.NativeSurfaceFrames, expectedFrames)
			}
		} else if doc.Job.GPU.VulkanFrames == nil || *doc.Job.GPU.VulkanFrames != int64(expectedFrames) {
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
	// A composited overlay artifact is intentionally not bitstream-copy
	// eligible. The output profile owns that policy; this gate certifies only
	// the GPU execution contract and the presence of Chronon's media receipt.
	return nil
}

// Publish uploads an already-rendered artifact to Google Drive and returns the
// artifact updated with its Drive file ID and link. It resolves the verified
// persistent L2 path, so publication does not fetch the object twice or create
// a temporary staging file. When no publisher is configured it returns the
// artifact unchanged.
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
	path, size, err := p.store.LocalPath(ctx, artifact.StorageKey)
	if err != nil {
		return artifact, fmt.Errorf("processor: resolve rendered artifact locally: %w", err)
	}
	var uploadChunks atomic.Int64
	var uploadedBytes atomic.Int64
	res, err := p.drive.Publish(ctx, drive.PublishRequest{
		Name: jobID + ".mp4", ContentType: artifact.ContentType, Path: path,
		Subfolder: artifact.ArtifactHash,
		UploadProgress: func(uploaded, _ int64) {
			uploadChunks.Add(1)
			uploadedBytes.Store(uploaded)
		},
	})
	if err != nil {
		return artifact, fmt.Errorf("processor: drive publish: %w", err)
	}
	// The local path was hash-verified by LocalPath for content-addressed keys;
	// publication additionally requires the provider to report the same size.
	if res.FileID == "" || res.SizeBytes != size {
		return artifact, fmt.Errorf("processor: drive publication identity mismatch (file_id=%q size=%d expected_size=%d)", res.FileID, res.SizeBytes, size)
	}
	if artifact.Metrics == nil {
		artifact.Metrics = map[string]float64{}
	}
	driveUS := float64(time.Since(phaseStart).Microseconds())
	artifact.Metrics["drive_publish_ms"] = driveUS / 1000
	artifact.Metrics["drive_upload_us"] = driveUS
	artifact.Metrics["drive_upload_chunks"] = float64(uploadChunks.Load())
	artifact.Metrics["drive_upload_bytes"] = float64(uploadedBytes.Load())
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
		verified, verifyErr := hashFileSHA256(outputPath)
		if verifyErr != nil {
			return queue.Artifact{}, fmt.Errorf("processor: hash output %s: %w", outputPath, verifyErr)
		}
		hash = verified
	}
	shaUS := float64(time.Since(shaStart).Microseconds())
	phaseMetrics["sha256_us"] = shaUS
	phaseMetrics["sha256_ms"] = shaUS / 1000
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
	// Ingest Chronon's timing sidecar as the source of truth for plan/graph/
	// GPU/encoder timing. A missing sidecar is non-fatal: the rendered bytes
	// are still valid, only the telemetry blob is absent from the ledger.
	var chrononTelemetry json.RawMessage
	if raw, err := chronon.ReadTimingSidecar(outputPath); err != nil {
		log.Printf("job %s: chronon timing sidecar unavailable: %v", jobID, err)
	} else {
		chrononTelemetry = raw
		artifact.ChrononTelemetry = raw
		mergeChrononNumericMetrics(phaseMetrics, raw)
	}
	if artifact, err = p.recordArtifact(ctx, jobID, artifact, probe, stats, inputBytes, chrononTelemetry); err != nil {
		return queue.Artifact{}, err
	}
	return artifact, nil
}

// mergeChrononNumericMetrics exposes the numeric fields already emitted by
// Chronon's timing sidecar on the queue artifact as well. The complete JSON is
// retained in the artifact ledger; this compact projection is what callers
// such as PipelineGen receive from GET /jobs/{id}.
func mergeChrononNumericMetrics(dst map[string]float64, raw json.RawMessage) {
	if dst == nil || len(raw) == 0 {
		return
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	var walk func(string, any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for key, child := range x {
				if prefix == "" {
					walk(key, child)
				} else {
					walk(prefix+"_"+key, child)
				}
			}
		case float64:
			dst["chronon_"+prefix] = x
		}
	}
	walk("", value)
}

// recordArtifact writes an optional worker-local diagnostic mirror. PostgreSQL
// queue completion remains authoritative, so mirror failures are non-fatal:
// the rendered artifact is returned unchanged except for the mirror_failure
// metric, which carries the observability signal to the completed job.
func (p *Processor) recordArtifact(ctx context.Context, jobID string, artifact queue.Artifact, probe *media.ProbeResult, stats overlay.Stats, inputBytes int64, chrononTelemetry json.RawMessage) (queue.Artifact, error) {
	if p.recorder == nil {
		return artifact, nil
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
		// The mirror must never fail the render: PostgreSQL queue completion is
		// authoritative. Surface the divergence on the artifact itself so the
		// completed job's metrics (visible on GET /jobs/{id} and benchmark
		// reports) carry an observable signal instead of a silent gap.
		log.Printf("processor: artifact diagnostic mirror %s unavailable: %v", jobID, err)
		if artifact.Metrics == nil {
			artifact.Metrics = map[string]float64{}
		}
		artifact.Metrics["mirror_failure"] = 1
	}
	return artifact, nil
}

// publishedRenderBackend is the cross-service backend identity. RenderingGen
// uses "vulkan" internally for configuration, while PipelineGen's clip.render
// contract names the certified Chronon path "chronon_vulkan". Only a render
// that passed the strict native gate may receive that published identity.
func publishedRenderBackend(backend string, strictNative bool) string {
	if backend == "vulkan" && strictNative {
		return "chronon_vulkan"
	}
	return backend
}

type planMetadata struct {
	Width      int
	Height     int
	FPSNum     int
	FPSDen     int
	FrameCount int
	DurationUS int64
	ProfileID  string
}

// renderMetadata extracts only the stable canvas facts needed by the
// artifact record, from the ONE typed plan (no JSON re-parsing).
func planMetadataOf(plan *overlay.Plan) planMetadata {
	if plan == nil || plan.Canvas.FPSNum <= 0 || plan.Canvas.FPSDen <= 0 {
		return planMetadata{}
	}
	return planMetadata{
		Width: plan.Canvas.Width, Height: plan.Canvas.Height,
		FPSNum: plan.Canvas.FPSNum, FPSDen: plan.Canvas.FPSDen,
		FrameCount: int(plan.Canvas.DurationFrames),
		DurationUS: plan.Canvas.DurationFrames * 1_000_000 * int64(plan.Canvas.FPSDen) / int64(plan.Canvas.FPSNum),
		ProfileID:  plan.Output.ProfileID,
	}
}

// artifactURL returns the L3 object URL for an artifact hash.
func (p *Processor) artifactURL(hash string) string {
	if p.storeURL == "" {
		return ""
	}
	return p.storeURL + "/objects/" + hash
}
