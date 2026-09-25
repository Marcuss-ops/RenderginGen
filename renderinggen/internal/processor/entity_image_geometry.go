package processor

import (
	"fmt"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// fitEntityImageLayersToAssets runs after workspace materialization so image
// metadata is read from the verified, content-addressed bytes rather than a
// logical path that does not exist during semantic compilation.
func fitEntityImageLayersToAssets(root string, plan *overlay.Plan) error {
	if plan == nil {
		return nil
	}
	for i := range plan.Layers {
		layer := &plan.Layers[i]
		if !layer.EntityImage {
			continue
		}
		assetPath := filepath.Join(root, filepath.FromSlash(layer.Asset))
		if err := overlay.FitEntityImageLayerToAsset(layer, assetPath); err != nil {
			return fmt.Errorf("processor: fit entity image layer %q to source aspect: %w", layer.ID, err)
		}
		layer.EntityImage = false
	}
	return nil
}
