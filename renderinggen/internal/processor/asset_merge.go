package processor

import (
	"fmt"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"strings"
)

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
				// A legacy job manifest may name the same bytes with a path
				// different from the compiler's canonical semantic path. Keep
				// the legacy alias for callers, and add the canonical path too:
				// the concrete Chronon plan must always resolve to a materialized
				// file under its own assets/... reference.
				if previous, exists := byPath[asset.LogicalPath]; exists {
					if !strings.EqualFold(previous, asset.Hash) {
						return nil, fmt.Errorf("processor: logical asset path %q has conflicting hashes", asset.LogicalPath)
					}
					continue
				}
				result = append(result, queue.AssetRef{Hash: asset.Hash, LogicalPath: asset.LogicalPath})
				byPath[asset.LogicalPath] = asset.Hash
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
