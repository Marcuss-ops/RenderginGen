package processor

import (
	"fmt"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"image"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const imageSniffBytes = 512

// normalizeMaterializedImagePaths makes the concrete Chronon path agree with
// the bytes that were actually downloaded. Some providers publish a JPEG
// behind a URL/metadata ending in .png; Chronon's image loader uses the file
// extension, so leaving that mismatch produces a valid but black render.
//
// It returns a new-path → old-path map for every rename. PrepareJob folds the
// new paths into the set of paths this stage materialized and uses the mapping
// to keep the prepared-package asset manifest aligned with the final plan.
//
// The extension vocabulary lives in internal/media (media.ImageExtensionMatches),
// not here: it is media-format knowledge, and the processor's job is only to
// apply the decision to the plan and report the renames. A format this pass does
// not recognize is left exactly as the producer declared it — renaming it would
// hand the engine an extension whose decoder cannot read the bytes.
func normalizeMaterializedImagePaths(root string, plan *overlay.Plan) (map[string]string, error) {
	if plan == nil {
		return nil, nil
	}
	var renamed map[string]string
	for i := range plan.Layers {
		layer := &plan.Layers[i]
		if layer.Type != "image" || layer.Asset == "" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(layer.Asset))
		head, err := readFileHead(path, imageSniffBytes)
		if err != nil {
			return nil, fmt.Errorf("image asset %s: %w", layer.Asset, err)
		}
		// Already correct, or a format this package cannot name: leave the
		// producer's path untouched.
		if media.ImageExtensionMatches(layer.Asset, head) {
			continue
		}
		ext, ok := media.ImageExtensionForData(head)
		if !ok {
			continue
		}
		newAsset := strings.TrimSuffix(layer.Asset, filepath.Ext(layer.Asset)) + ext
		newPath := filepath.Join(root, filepath.FromSlash(newAsset))
		if _, err := os.Lstat(newPath); err == nil {
			return nil, fmt.Errorf("image asset %s rename target already exists: %s", layer.Asset, newAsset)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("image asset %s inspect rename target %s: %w", layer.Asset, newAsset, err)
		}
		if err := os.Rename(path, newPath); err != nil {
			return nil, fmt.Errorf("image asset %s rename: %w", layer.Asset, err)
		}
		if renamed == nil {
			renamed = make(map[string]string)
		}
		renamed[newAsset] = layer.Asset
		layer.Asset = newAsset
	}
	return renamed, nil
}

// validateMapRasterAssets checks the actual staged PNG bytes for every compiled
// map raster. Producer metadata and SHA-256 establish provenance/integrity, but
// only DecodeConfig can confirm that the bytes have the georeferenced size.
// This runs after materialization and path normalization, before plan.json or
// Chronon, and never performs network access.
func validateMapRasterAssets(root string, plan *overlay.Plan) error {
	if plan == nil {
		return fmt.Errorf("processor: map raster validation requires a render plan")
	}
	seen := make(map[string]struct{})
	for _, layer := range plan.Layers {
		if layer.MapRasterWidth <= 0 && layer.MapRasterHeight <= 0 {
			continue
		}
		if layer.MapRasterWidth <= 0 || layer.MapRasterHeight <= 0 || layer.Type != "image" || strings.TrimSpace(layer.Asset) == "" {
			return fmt.Errorf("processor: map raster layer %q has incomplete verification metadata", layer.ID)
		}
		if _, duplicate := seen[layer.Asset]; duplicate {
			continue
		}
		seen[layer.Asset] = struct{}{}
		path := filepath.Join(root, filepath.FromSlash(layer.Asset))
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("processor: map raster layer %q asset %q: %w", layer.ID, layer.Asset, err)
		}
		config, format, decodeErr := image.DecodeConfig(file)
		closeErr := file.Close()
		if decodeErr != nil {
			return fmt.Errorf("processor: map raster layer %q asset %q is not a decodable PNG: %w", layer.ID, layer.Asset, decodeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("processor: close map raster layer %q asset %q: %w", layer.ID, layer.Asset, closeErr)
		}
		fullResolution := config.Width == layer.MapRasterWidth && config.Height == layer.MapRasterHeight
		halfResolution := config.Width*2 == layer.MapRasterWidth && config.Height*2 == layer.MapRasterHeight
		if format != "png" || (!fullResolution && !halfResolution) {
			return fmt.Errorf("processor: map raster layer %q declares %dx%d but staged %s is %dx%d",
				layer.ID, layer.MapRasterWidth, layer.MapRasterHeight, format, config.Width, config.Height)
		}
	}
	return nil
}

// readFileHead reads up to n leading bytes of path.
//
// A short read at EOF is not an error: image signatures are at the start of the
// file, and a file smaller than the window is exactly the case where reading
// what exists is the whole point. Only "no bytes at all" fails (an empty file
// is not an image), which is what the caller reports against the asset name.
func readFileHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	nn, readErr := io.ReadFull(f, buf)
	if nn == 0 && readErr != nil {
		return nil, readErr
	}
	return buf[:nn], nil
}

// finalPreparedAssets projects the semantic asset manifest onto the concrete
// paths after image-extension normalization. The bytes do not change during a
// rename, so the original content hash remains authoritative.
func finalPreparedAssets(compiled []overlay.Asset, renamed map[string]string) []overlay.Asset {
	if len(renamed) == 0 {
		return compiled
	}
	assets := append([]overlay.Asset(nil), compiled...)
	for newPath, oldPath := range renamed {
		for _, asset := range compiled {
			if asset.LogicalPath == oldPath {
				assets = append(assets, overlay.Asset{Hash: asset.Hash, LogicalPath: newPath})
				break
			}
		}
	}
	return assets
}

// validateMaterializedPlanAssets is the fail-closed boundary immediately
// before Chronon. MaterializePaths validates the queue manifest, but the
// compiler and image-extension normalization can change the concrete paths
// referenced by the typed plan. Check the plan itself so Chronon can never be
// the first component to report a missing asset.
//
// materialized is the set of workspace-relative paths this prepare stage itself
// created (every resolved manifest asset plus every normalize rename target).
// MaterializePaths returns nil only when all of them landed and the renames
// succeeded, so a plan path in that set existed moments ago and re-statting it
// is one syscall per unique asset of pure duplicate I/O. A plan path NOT in the
// set was not produced here, so it is still verified on the filesystem: the
// guarantee that Chronon is never the first to see a missing asset is
// unchanged for exactly the cases that can actually be missing.
func validateMaterializedPlanAssets(root string, plan *overlay.Plan, materialized map[string]struct{}) error {
	if plan == nil {
		return fmt.Errorf("processor: render plan is nil before Chronon")
	}
	seen := make(map[string]struct{})
	for _, layer := range plan.Layers {
		asset := strings.TrimSpace(layer.Asset)
		if asset == "" {
			continue
		}
		if _, ok := seen[asset]; ok {
			continue
		}
		seen[asset] = struct{}{}
		if filepath.IsAbs(asset) || filepath.Clean(asset) != asset || strings.HasPrefix(asset, "../") || asset == ".." {
			return fmt.Errorf("processor: Chronon asset path %q is not workspace-relative", asset)
		}
		if _, proven := materialized[asset]; proven {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(asset))
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("processor: asset %q missing before Chronon: %w", asset, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("processor: asset %q is not a regular file before Chronon", asset)
		}
	}
	return nil
}
