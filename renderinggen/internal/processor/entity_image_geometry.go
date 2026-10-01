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
		if err := overlay.ResizePremiumImageMask(layer); err != nil {
			return fmt.Errorf("processor: resize premium image mask for %q: %w", layer.ID, err)
		}
		layer.EntityImage = false
		for j := range plan.Layers {
			component := &plan.Layers[j]
			if component.PremiumParentImageID != layer.ID {
				continue
			}
			if component.PremiumCanvasSize {
				component.Size = []float64{float64(plan.Canvas.Width), float64(plan.Canvas.Height)}
				component.Position = []float64{0, 0}
				continue
			}
			if len(component.PremiumSizeScale) == 2 {
				component.Size = []float64{layer.Size[0] * component.PremiumSizeScale[0], layer.Size[1] * component.PremiumSizeScale[1]}
			} else if len(component.PremiumParentSize) == 2 {
				component.Size = append([]float64(nil), component.PremiumParentSize...)
			}
			if len(layer.Position) >= 2 {
				component.Position = []float64{layer.Position[0], layer.Position[1]}
				if len(component.PremiumPositionOffset) == 2 {
					component.Position[0] += component.PremiumPositionOffset[0]
					component.Position[1] += component.PremiumPositionOffset[1]
				}
			}
			if component.PremiumSyncTransform {
				overlay.SyncPremiumComponentTransform(layer, component)
			}
			if component.PremiumPathKind == "rounded_rect" && component.Shape != nil {
				radius := layer.Radius * component.PremiumRadiusScale
				if radius == 0 {
					radius = layer.Radius
				}
				component.Shape.Path = overlay.RoundedPremiumRectPath(component.Size[0], component.Size[1], radius)
				if component.PremiumShapeKind == "rounded_rect" {
					component.Shape.Radius = radius
				}
			}
		}
	}
	return nil
}
