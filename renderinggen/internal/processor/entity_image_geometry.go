package processor

import (
	"fmt"
	"image"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// probePlanImageSizes is the I/O half of the sized compile: it reads the
// decoded pixel size of every image layer that still awaits its source
// geometry and returns them keyed by the plan's logical asset path.
//
// The processor owns the file access because it has already verified and
// materialized the content-addressed bytes; overlay owns the geometry. The
// returned map is ALWAYS non-nil, so an empty plan still selects the sized
// compile and can never silently fall back to a guessed box.
func probePlanImageSizes(root string, plan *overlay.Plan) (map[string]overlay.ImageSize, error) {
	sizes := make(map[string]overlay.ImageSize)
	if plan == nil {
		return sizes, nil
	}
	for i := range plan.Layers {
		layer := &plan.Layers[i]
		if layer.Type != "image" || (!layer.DeriveWidthFromAsset && !layer.EntityImage) {
			continue
		}
		if layer.Asset == "" {
			return nil, fmt.Errorf("processor: image layer %q needs source dimensions but declares no asset", layer.ID)
		}
		if _, probed := sizes[layer.Asset]; probed {
			continue
		}
		sourceWidth, sourceHeight, err := entityImageDimensions(
			filepath.Join(root, filepath.FromSlash(layer.Asset)))
		if err != nil {
			return nil, fmt.Errorf("processor: read image layer %q dimensions: %w", layer.ID, err)
		}
		sizes[layer.Asset] = overlay.ImageSize{Width: sourceWidth, Height: sourceHeight}
	}
	return sizes, nil
}

// entityImageDimensions reads the verified, materialized image's pixel size.
// It lives on the processor side of the boundary: the overlay compiler receives
// the numbers, never a path it would have to open.
func entityImageDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}
