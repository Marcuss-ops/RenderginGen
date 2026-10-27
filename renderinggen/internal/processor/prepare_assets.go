package processor

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
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
			// A retry re-materializes the producer's original name into the same
			// job workspace, where a previous attempt may already have produced
			// the normalized target from these same verified bytes (assets are
			// content-verified, so a legitimate re-materialization is
			// byte-identical). Adopt that target — drop the stale source and
			// record the rename — instead of failing the retry. A target with
			// DIFFERENT bytes is a genuine name collision and keeps failing
			// closed below.
			same, cmpErr := sameFileContent(path, newPath)
			if cmpErr != nil {
				return nil, fmt.Errorf("image asset %s compare rename target %s: %w", layer.Asset, newAsset, cmpErr)
			}
			if !same {
				return nil, fmt.Errorf("image asset %s rename target already exists: %s", layer.Asset, newAsset)
			}
			if err := os.Remove(path); err != nil {
				return nil, fmt.Errorf("image asset %s remove retry leftover source: %w", layer.Asset, err)
			}
			if renamed == nil {
				renamed = make(map[string]string)
			}
			renamed[newAsset] = layer.Asset
			layer.Asset = newAsset
			continue
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

// sameFileContent reports whether a and b hold the same bytes. It is the
// tie-breaker between a retry leftover (the same verified download already
// normalized by an earlier attempt — safe to adopt) and a genuine rename
// collision (different bytes claiming one name — must fail closed).
func sameFileContent(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if sa.Size() != sb.Size() {
		return false, nil
	}
	da, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	db, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(da, db), nil
}

// validateMapRasterAssets checks the actual staged PNG bytes for every compiled
// map raster. Producer metadata and SHA-256 establish provenance/integrity, but
// only DecodeConfig can confirm that the bytes have the georeferenced size.
// This runs after materialization and path normalization, before plan.json or
// Chronon, and never performs network access.
// rasterCheck is one deduplicated raster validation unit: the first layer
// that referenced the asset (for error attribution) plus its declared size.
type rasterCheck struct {
	layerID string
	asset   string
	width   int
	height  int
}

func validateMapRasterAssets(root string, plan *overlay.Plan) error {
	if plan == nil {
		return fmt.Errorf("processor: map raster validation requires a render plan")
	}
	// Metadata gate first, serially: incomplete verification metadata is a
	// producer bug that must fail before any file I/O is spent.
	seen := make(map[string]struct{})
	checks := make([]rasterCheck, 0, len(plan.Layers))
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
		checks = append(checks, rasterCheck{layerID: layer.ID, asset: layer.Asset, width: layer.MapRasterWidth, height: layer.MapRasterHeight})
	}
	if len(checks) == 0 {
		return nil
	}
	// DecodeConfig only reads the image header, but it still opens and reads
	// a file per raster: validate concurrently (I/O-bound) on an adaptive
	// worker pool instead of serially. The first failure wins via errOnce so
	// the caller sees the root cause, not the last racing worker's error.
	workers := len(checks)
	if workers > 4 {
		workers = 4
	}
	if ncpu := runtime.NumCPU(); workers > ncpu {
		workers = ncpu
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan rasterCheck, workers)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				if err := validateOneMapRaster(root, c); err != nil {
					errOnce.Do(func() { firstErr = err })
				}
			}
		}()
	}
	for _, c := range checks {
		jobs <- c
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

// validateOneMapRaster checks the staged bytes of a single compiled map
// raster against its georeferenced size. Only DecodeConfig runs (header,
// never a full decode) and no network access is performed.
func validateOneMapRaster(root string, c rasterCheck) error {
	path := filepath.Join(root, filepath.FromSlash(c.asset))
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("processor: map raster layer %q asset %q: %w", c.layerID, c.asset, err)
	}
	config, format, decodeErr := image.DecodeConfig(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return fmt.Errorf("processor: map raster layer %q asset %q is not a decodable PNG: %w", c.layerID, c.asset, decodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("processor: close map raster layer %q asset %q: %w", c.layerID, c.asset, closeErr)
	}
	fullResolution := config.Width == c.width && config.Height == c.height
	halfResolution := config.Width*2 == c.width && config.Height*2 == c.height
	if format != "png" || (!fullResolution && !halfResolution) {
		return fmt.Errorf("processor: map raster layer %q declares %dx%d but staged %s is %dx%d",
			c.layerID, c.width, c.height, format, config.Width, config.Height)
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
