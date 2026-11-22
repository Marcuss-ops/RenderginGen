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
		if isHTTPURL(asset.LogicalPath) {
			return nil, fmt.Errorf("processor: asset %s logical_path %q is a URL; set source_url and use a workspace-relative logical_path", asset.Hash, asset.LogicalPath)
		}
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
	// Merge the compiler's canonical semantic path into the job manifest by
	// content hash. URL-backed manifests are rejected above: the worker never
	// treats logical_path as a fetchable origin.
	for _, asset := range compiled {
		if idx, ok := byHash[strings.ToLower(asset.Hash)]; ok {
			merged := result[idx]
			// A legacy job manifest may name the same bytes with a path
			// different from the compiler's canonical semantic path. Keep the
			// legacy alias for callers, and add the canonical path too: the
			// concrete Chronon plan must always resolve to a materialized file
			// under its own assets/... reference. When the producer already
			// supplied a SourceURL, the canonical path replaces the alias.
			if merged.SourceURL == "" {
				next, _, err := appendAssetRef(result, byPath, queue.AssetRef{Hash: asset.Hash, LogicalPath: asset.LogicalPath})
				if err != nil {
					return nil, err
				}
				result = next
				continue
			}
			merged.LogicalPath = asset.LogicalPath
			if !strings.EqualFold(merged.Hash, asset.Hash) {
				return nil, fmt.Errorf("processor: asset hash conflict")
			}
			result[idx] = merged
			byPath[asset.LogicalPath] = asset.Hash
			continue
		}
		next, added, err := appendAssetRef(result, byPath, queue.AssetRef{Hash: asset.Hash, LogicalPath: asset.LogicalPath})
		if err != nil {
			return nil, err
		}
		result = next
		if !added {
			continue
		}
		byHash[strings.ToLower(asset.Hash)] = len(result) - 1
	}
	return result, nil
}

// appendAssetRef records a path-keyed ref unless the path already exists. It
// reports whether the ref was added; an existing path with a different hash is
// an error. Both branches that add a canonical path share this rule.
func appendAssetRef(result []queue.AssetRef, byPath map[string]string, asset queue.AssetRef) ([]queue.AssetRef, bool, error) {
	if previous, exists := byPath[asset.LogicalPath]; exists {
		if !strings.EqualFold(previous, asset.Hash) {
			return result, false, fmt.Errorf("processor: logical asset path %q has conflicting hashes", asset.LogicalPath)
		}
		return result, false, nil
	}
	result = append(result, asset)
	byPath[asset.LogicalPath] = asset.Hash
	return result, true, nil
}
