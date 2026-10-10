package processor

import (
	"fmt"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// fitEntityImageLayersToAssets runs after workspace materialization so image
// metadata is read from the verified, content-addressed bytes rather than a
// logical path that does not exist during semantic compilation.
//
// It reports whether it changed the plan. A fitted image box changes the layer
// size and its premium components' sizes, and those sizes are part of the
// prepared package's asset/text preparation identity — so the caller rebuilds
// the package from the final plan exactly when this reports true, instead of
// guessing both ways (rebuilding on every job, or shipping a sidecar that
// describes the pre-fit geometry).
func fitEntityImageLayersToAssets(root string, plan *overlay.Plan) (bool, error) {
	if plan == nil {
		return false, nil
	}
	changed := false
	for i := range plan.Layers {
		layer := &plan.Layers[i]
		if !layer.EntityImage {
			continue
		}
		changed = true
		assetPath := filepath.Join(root, filepath.FromSlash(layer.Asset))
		if err := overlay.FitEntityImageLayerToAsset(layer, assetPath); err != nil {
			return false, fmt.Errorf("processor: fit entity image layer %q to source aspect: %w", layer.ID, err)
		}
		if err := overlay.ResizePremiumImageMask(layer); err != nil {
			return false, fmt.Errorf("processor: resize premium image mask for %q: %w", layer.ID, err)
		}
		if err := alignEntityCaptionToFittedImage(plan, layer); err != nil {
			return false, fmt.Errorf("processor: align entity caption for %q: %w", layer.ID, err)
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
	if err := overlay.ValidateEntityCaptionCollisions(plan.Layers); err != nil {
		return false, fmt.Errorf("processor: fitted entity caption layout: %w", err)
	}
	return changed, nil
}

// alignEntityCaptionToFittedImage recomputes caption anchoring after the image
// box has been fitted to the verified source aspect ratio. Semantic lowering
// only knows the requested contain box; without this pass a wide source can
// shrink vertically while its name stays at the old box bottom, leaving an
// oversized gap between portrait and label.
func alignEntityCaptionToFittedImage(plan *overlay.Plan, image *overlay.Layer) error {
	if plan == nil || image == nil || len(image.Size) < 2 || len(image.Position) < 2 {
		return nil
	}
	for i := range plan.Layers {
		caption := &plan.Layers[i]
		if caption.CaptionForImageID != image.ID {
			continue
		}
		bounds := overlay.EntityCardImageBoundsFromCenter(
			plan.Canvas.Width, plan.Canvas.Height, image.Position, image.Size[0], image.Size[1],
		)
		layout, err := overlay.ResolveEntityCardLayoutAt(plan.Canvas.Width, plan.Canvas.Height, bounds, caption.Text, caption.CaptionLayout)
		if err != nil {
			return err
		}
		image.Position[1] = layout.ImageBounds.Y + image.Size[1]/2 - float64(plan.Canvas.Height)/2
		caption.Position = []float64{layout.CaptionBounds.CenterX, layout.CaptionBounds.CenterY}
		caption.Size = []float64{layout.CaptionBounds.Width, layout.CaptionBounds.Height}
		caption.BoxWidth = int(layout.CaptionBounds.Width)
		caption.BoxHeight = int(layout.CaptionBounds.Height)
		if caption.Style != nil {
			caption.Style.FontSize = layout.CaptionBounds.FontSize
			caption.Style.MinFontSize = layout.CaptionBounds.FontSize
			caption.Style.MaxFontSize = layout.CaptionBounds.FontSize
		}
		return nil
	}
	return nil
}
