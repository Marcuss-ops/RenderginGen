// materialize.go owns the prepare/materialize phases: compiling a semantic
// plan without invoking Chronon, then resolving and materializing every asset
// through the worker's single zero-copy resolver.
package processor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/workspace"
)

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
		// Prepare materializes through the same zero-copy path resolver as the
		// render pipeline (self-heal, hash verification, L3 staging): there is
		// exactly one asset-resolution implementation on the worker.
		if err := ws.MaterializePaths(ctx, p.resolveAssetStreaming, job.Assets); err != nil {
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
	if err := ws.MaterializePaths(ctx, p.resolveAssetStreaming, assets); err != nil {
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

// resolveAssetStreaming is the single asset resolver on the worker. It
// preserves the content-addressed invariant at the worker boundary: a cached
// local path (L2/L1) is returned directly for the workspace to hard-link; a
// cache-miss self-heal downloads straight to a bounded temp file (constant
// memory, never the whole object in RAM), verifies the SHA-256 while
// streaming, stages it into L3 via PutReader and hands the local path to the
// workspace for a hard link. A hash mismatch deletes the temp file and fails
// the resolution — the wrong bytes never reach Chronon. Legacy development
// fixtures may carry symbolic keys; production SHA-256 keys (64 hexadecimal
// characters) are always verified against the bytes before Chronon sees them.
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

	// Stream to disk + hash in one pass via the shared hashio primitive,
	// capped at maxAssetDownloadBytes (the single size-cap policy for network
	// downloads; see hashio's package doc).
	digest, size, err := hashio.Copy(io.LimitReader(resp.Body, maxAssetDownloadBytes+1), tmp)
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
