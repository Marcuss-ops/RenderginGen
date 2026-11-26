package processor

import (
	"fmt"
	"image"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// probePendingImageSizes is the I/O half of the sized compile: it reads the
// decoded pixel size of every image the resolved plan will lower into a layer
// whose geometry derives from its source, and returns them keyed by the plan's
// logical asset path.
//
// The probe answers from the RESOLVE, not from a compiled plan: the whole point
// of the two-phase compile is that no layer exists yet when the bytes are
// materialized. PendingImageAssets is the overlay-owned predicate for "this
// image's size matters"; it lists exactly the assets the lower will consume.
//
// The processor owns the file access because it has already verified and
// materialized the content-addressed bytes; overlay owns the geometry. The
// returned map is ALWAYS non-nil, so an empty plan still selects the sized
// lower and can never silently fall back to a guessed box.
func (p *Processor) probePendingImageSizes(root string, resolved *overlay.PreparedResolve) (map[string]overlay.ImageSize, error) {
	sizes := make(map[string]overlay.ImageSize)
	for _, asset := range resolved.PendingImageAssets() {
		if _, probed := sizes[asset]; probed {
			continue
		}
		sourceWidth, sourceHeight, err := entityImageDimensions(
			filepath.Join(root, filepath.FromSlash(asset)))
		if err != nil {
			return nil, fmt.Errorf("processor: read pending image %q dimensions: %w", asset, err)
		}
		sizes[asset] = overlay.ImageSize{Width: sourceWidth, Height: sourceHeight}
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
