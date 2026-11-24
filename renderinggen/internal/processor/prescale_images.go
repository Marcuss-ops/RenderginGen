package processor

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// prescaleMaxDimension is the global ceiling for a still image on the
// certified overlay path. A layer's fitted display box can lower the bound
// through imageDisplayBound; no image above 2048 reaches Chronon.
const prescaleMaxDimension = 2048

// prescaledImage is one display-bound derivative created from a materialized
// source. The source file is never modified: Path is a new workspace file and
// Hash is the SHA-256 of the derivative bytes.
type prescaledImage struct {
	OriginalPath  string
	Path          string
	Hash          string
	OriginalBytes int64
	Bytes         int
	OriginalW     int
	OriginalH     int
	Width         int
	Height        int
	Data          []byte
	Cached        bool
}

// prescalePlanImages replaces every oversized still-image layer with a
// display-bound derivative and returns the files it created. The caller owns
// publishing those bytes under their new content address and updating the
// prepared-package manifest.
//
// cacheDir, when non-empty, stores the derivative under a key derived from the
// source content hash and the display bound. A later job with the same source
// links the cached bytes instead of decoding the original again.
//
// Map rasters are deliberately excluded: their declared georeferenced size is
// part of the map contract and validateMapRasterAssets hashes the exact PNG
// dimensions. A map raster that is also referenced by a normal image layer is
// excluded for the same reason.
func prescalePlanImages(root string, plan *overlay.Plan, cacheDir string, sourceHashes map[string]string) ([]prescaledImage, error) {
	if plan == nil {
		return nil, nil
	}
	mapAssets := make(map[string]struct{})
	for _, layer := range plan.Layers {
		if layer.MapRasterWidth <= 0 && layer.MapRasterHeight <= 0 {
			continue
		}
		if asset := layerImageAsset(layer); asset != "" {
			mapAssets[asset] = struct{}{}
		}
	}

	// Chronon bounds its runtime copy by 2x the layer's fitted display box
	// (clamped to [512, 2048]). Match that bound exactly so the derivative
	// is neither larger than the sampler can read nor smaller than the
	// renderer's own cap. An intrinsic-size layer has no declared box and
	// keeps the global 2048 cap.
	bounds := make(map[string]int)
	for _, layer := range plan.Layers {
		if layer.Type != "image" {
			continue
		}
		asset := layerImageAsset(layer)
		if asset == "" {
			continue
		}
		if _, isMap := mapAssets[asset]; isMap {
			continue
		}
		if bound := imageDisplayBound(layer); bound > bounds[asset] {
			bounds[asset] = bound
		}
	}

	done := make(map[string]prescaledImage)
	var out []prescaledImage
	for i := range plan.Layers {
		layer := &plan.Layers[i]
		if layer.Type != "image" {
			continue
		}
		field := &layer.Asset
		asset := layer.Asset
		if asset == "" {
			field = &layer.Source
			asset = layer.Source
		}
		if asset == "" {
			continue
		}
		if _, isMap := mapAssets[asset]; isMap {
			continue
		}
		if existing, ok := done[asset]; ok {
			*field = existing.Path
			continue
		}
		derived, ok, err := prescaleOneImage(root, asset, bounds[asset], cacheDir, sourceHashes[asset])
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		done[asset] = derived
		out = append(out, derived)
		*field = derived.Path
	}
	return out, nil
}

func layerImageAsset(layer overlay.Layer) string {
	if layer.Asset != "" {
		return layer.Asset
	}
	return layer.Source
}

// imageDisplayBound mirrors LayerBuilder::image: 2x the longest fitted box
// side, clamped to [512, 2048]. The global cap applies when the layer has no
// declared box (intrinsic sizing).
func imageDisplayBound(layer overlay.Layer) int {
	displayExtent := 0.0
	if len(layer.Size) >= 2 {
		displayExtent = math.Max(layer.Size[0], layer.Size[1])
	}
	if displayExtent <= 0 {
		return prescaleMaxDimension
	}
	bound := int(math.Round(displayExtent * 2))
	if bound < 512 {
		bound = 512
	}
	if bound > prescaleMaxDimension {
		bound = prescaleMaxDimension
	}
	return bound
}

// prescaleOneImage creates a separate derivative; it never overwrites the
// materialized source. The source may be a hard link into the content-addressed
// cache, so an in-place resize would corrupt the cache.
func prescaleOneImage(root, logicalPath string, maxDimension int, cacheDir, sourceHash string) (prescaledImage, bool, error) {
	sourcePath := filepath.Join(root, filepath.FromSlash(logicalPath))
	source, err := os.Open(sourcePath)
	if err != nil {
		return prescaledImage{}, false, fmt.Errorf("processor: prescale image %q: %w", logicalPath, err)
	}
	config, format, configErr := image.DecodeConfig(source)
	closeErr := source.Close()
	if configErr != nil {
		return prescaledImage{}, false, fmt.Errorf("processor: prescale image %q decode config: %w", logicalPath, configErr)
	}
	if closeErr != nil {
		return prescaledImage{}, false, fmt.Errorf("processor: prescale image %q close: %w", logicalPath, closeErr)
	}
	if config.Width <= maxDimension && config.Height <= maxDimension {
		return prescaledImage{}, false, nil
	}
	if format != "jpeg" && format != "png" {
		return prescaledImage{}, false, fmt.Errorf(
			"processor: prescale image %q is %s at %dx%d; only JPEG and PNG can be pre-scaled",
			logicalPath, format, config.Width, config.Height)
	}

	width, height := displayBoundSize(config.Width, config.Height, maxDimension)
	base := strings.TrimSuffix(path.Base(logicalPath), path.Ext(logicalPath))
	derivedPath := path.Join("assets", "prescaled", storage.Hash([]byte(logicalPath))[:16], base+prescaleExtension(format))
	destination := filepath.Join(root, filepath.FromSlash(derivedPath))
	info, err := os.Stat(sourcePath)
	if err != nil {
		return prescaledImage{}, false, fmt.Errorf("processor: prescale image %q source stat: %w", logicalPath, err)
	}

	if cacheDir != "" && sourceHash != "" {
		cachePath := prescaledCachePath(cacheDir, sourceHash, maxDimension, format)
		if data, ok := loadPrescaledCache(cachePath, width, height); ok {
			if err := writeAtomicFile(destination, data); err != nil {
				return prescaledImage{}, false, fmt.Errorf("processor: prescale image %q install cached: %w", logicalPath, err)
			}
			return prescaledImage{
				OriginalPath: logicalPath, Path: derivedPath, Hash: storage.Hash(data),
				OriginalBytes: info.Size(), Bytes: len(data),
				OriginalW: config.Width, OriginalH: config.Height, Width: width, Height: height,
				Data: data, Cached: true,
			}, true, nil
		}
	}

	source, err = os.Open(sourcePath)
	if err != nil {
		return prescaledImage{}, false, fmt.Errorf("processor: prescale image %q open: %w", logicalPath, err)
	}
	decoded, _, decodeErr := image.Decode(source)
	closeErr = source.Close()
	if decodeErr != nil {
		return prescaledImage{}, false, fmt.Errorf("processor: prescale image %q decode: %w", logicalPath, decodeErr)
	}
	if closeErr != nil {
		return prescaledImage{}, false, fmt.Errorf("processor: prescale image %q close: %w", logicalPath, closeErr)
	}

	scaled := resizeAreaAverage(decoded, width, height)
	data, _, err := encodePrescaled(scaled, format)
	if err != nil {
		return prescaledImage{}, false, fmt.Errorf("processor: prescale image %q encode: %w", logicalPath, err)
	}
	if cacheDir != "" && sourceHash != "" {
		cachePath := prescaledCachePath(cacheDir, sourceHash, maxDimension, format)
		// A cache write failure must not fail a render that already has the
		// bytes in memory; the next job simply pays the cold path again.
		_ = writeAtomicFile(cachePath, data)
	}
	if err := writeAtomicFile(destination, data); err != nil {
		return prescaledImage{}, false, fmt.Errorf("processor: prescale image %q install: %w", logicalPath, err)
	}
	return prescaledImage{
		OriginalPath: logicalPath, Path: derivedPath, Hash: storage.Hash(data),
		OriginalBytes: info.Size(), Bytes: len(data),
		OriginalW: config.Width, OriginalH: config.Height, Width: width, Height: height,
		Data: data,
	}, true, nil
}

func prescaleExtension(format string) string {
	if format == "jpeg" {
		return ".jpg"
	}
	return ".png"
}

func prescaledCachePath(cacheDir, sourceHash string, maxDimension int, format string) string {
	key := storage.Hash([]byte(sourceHash + "\x00" + strconv.Itoa(maxDimension) + "\x00" + format))
	return filepath.Join(cacheDir, key[:2], key+prescaleExtension(format))
}

func loadPrescaledCache(cachePath string, width, height int) ([]byte, bool) {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != width || config.Height != height {
		return nil, false
	}
	return data, true
}

// Temp cleanup below is best-effort: the write/chmod/rename error is the
// failure that explains why the file was not installed, and the temp name is
// inside our own destination directory.
func writeAtomicFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".prescale-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func displayBoundSize(width, height, maxDimension int) (int, int) {
	if width <= maxDimension && height <= maxDimension {
		return width, height
	}
	if width >= height {
		target := maxDimension
		scaled := (height*maxDimension + width/2) / width
		if scaled < 1 {
			scaled = 1
		}
		return target, scaled
	}
	target := maxDimension
	scaled := (width*maxDimension + height/2) / height
	if scaled < 1 {
		scaled = 1
	}
	return scaled, target
}

// resizeAreaAverage is a deterministic box filter over premultiplied RGBA.
// Averaging premultiplied channels avoids the dark fringe that averaging
// straight-alpha channels produces on transparent edges.
func resizeAreaAverage(source image.Image, width, height int) *image.NRGBA {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	destination := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		y0 := bounds.Min.Y + y*sourceHeight/height
		y1 := bounds.Min.Y + (y+1)*sourceHeight/height
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < width; x++ {
			x0 := bounds.Min.X + x*sourceWidth/width
			x1 := bounds.Min.X + (x+1)*sourceWidth/width
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var red, green, blue, alpha uint64
			for sourceY := y0; sourceY < y1; sourceY++ {
				for sourceX := x0; sourceX < x1; sourceX++ {
					r, g, b, a := source.At(sourceX, sourceY).RGBA()
					red += uint64(r)
					green += uint64(g)
					blue += uint64(b)
					alpha += uint64(a)
				}
			}
			count := uint64((x1 - x0) * (y1 - y0))
			red /= count
			green /= count
			blue /= count
			alpha /= count
			out := destination.PixOffset(x, y)
			if alpha == 0 {
				destination.Pix[out+3] = 0
				continue
			}
			destination.Pix[out] = uint8((red * 0xffff / alpha) >> 8)
			destination.Pix[out+1] = uint8((green * 0xffff / alpha) >> 8)
			destination.Pix[out+2] = uint8((blue * 0xffff / alpha) >> 8)
			destination.Pix[out+3] = uint8(alpha >> 8)
		}
	}
	return destination
}

func encodePrescaled(source image.Image, format string) ([]byte, string, error) {
	var buffer bytes.Buffer
	switch format {
	case "jpeg":
		if err := jpeg.Encode(&buffer, source, &jpeg.Options{Quality: 90}); err != nil {
			return nil, "", err
		}
		return buffer.Bytes(), ".jpg", nil
	case "png":
		encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := encoder.Encode(&buffer, source); err != nil {
			return nil, "", err
		}
		return buffer.Bytes(), ".png", nil
	default:
		return nil, "", fmt.Errorf("unsupported image format %q", format)
	}
}

// applyPrescaledImages publishes each derivative under its own content address
// and swaps the compiled overlay manifest from the source path to the
// derivative. The materialized source stays in ResolvedAssets so a retry or a
// prepared-package consumer can still verify where the derivative came from.
func (p *Processor) applyPrescaledImages(ctx context.Context, st *prepareState, images []prescaledImage) error {
	if len(images) == 0 {
		return nil
	}
	if p.store == nil {
		return fmt.Errorf("processor: prescaled images require a content store")
	}
	for _, image := range images {
		if err := p.store.Put(ctx, image.Hash, image.Data); err != nil {
			return fmt.Errorf("processor: publish prescaled image %q: %w", image.Path, err)
		}
		aliases := map[string]struct{}{image.OriginalPath: {}}
		if original, ok := st.renamed[image.OriginalPath]; ok {
			aliases[original] = struct{}{}
		}
		st.compiledAssets = replaceCompiledAsset(st.compiledAssets, aliases, overlay.Asset{
			Hash: image.Hash, LogicalPath: image.Path,
		})
		st.assets = appendAssetRefIfMissing(st.assets, queue.AssetRef{
			Hash: image.Hash, LogicalPath: image.Path,
		})
	}
	st.packageStale = true
	return nil
}

func replaceCompiledAsset(assets []overlay.Asset, aliases map[string]struct{}, replacement overlay.Asset) []overlay.Asset {
	out := make([]overlay.Asset, 0, len(assets)+1)
	replaced := false
	for _, asset := range assets {
		if _, ok := aliases[asset.LogicalPath]; ok {
			if !replaced {
				out = append(out, replacement)
				replaced = true
			}
			continue
		}
		out = append(out, asset)
	}
	if !replaced {
		out = append(out, replacement)
	}
	return out
}

func appendAssetRefIfMissing(assets []queue.AssetRef, add queue.AssetRef) []queue.AssetRef {
	for _, asset := range assets {
		if asset.LogicalPath == add.LogicalPath {
			return assets
		}
	}
	return append(assets, add)
}
