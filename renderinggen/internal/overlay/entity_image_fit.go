package overlay

import "fmt"

// entity_image_fit.go owns the geometry a card settles on once its source
// photo has been materialized: the entity card's contain box, the standalone
// height-only card's derived width, the linked premium frame/mask and the
// caption anchored under the fitted image.
//
// The I/O stays on the processor side of the boundary on purpose: the
// processor opens the verified, content-addressed bytes and hands the decoded
// sizes in. Everything downstream of the numbers is geometry and belongs here,
// so the processor never reaches into transient Layer metadata (EntityImage,
// DeriveWidthFromAsset, Premium*, ChainPanOffset) to re-lay-out a compiled
// plan.

// ImageSize is one materialized source image's pixel size.
type ImageSize struct {
	Width  int
	Height int
}

// FitImageLayersToSizes resolves every image layer that awaits its source
// geometry against the probed sizes and reports whether it changed the plan.
//
// sizes is keyed by the layer's Asset path (the same logical path the plan
// carries). A layer that needs a size but has no entry is an internal error:
// the probe and the plan must agree, never silently render the guessed box.
func FitImageLayersToSizes(plan *Plan, sizes map[string]ImageSize) (bool, error) {
	if plan == nil {
		return false, nil
	}
	changed := false
	for i := range plan.Layers {
		layer := &plan.Layers[i]
		if layer.DeriveWidthFromAsset && layer.Type == "image" && !layer.EntityImage {
			size, err := probedImageSize(layer, sizes)
			if err != nil {
				return false, err
			}
			changed = true
			if err := deriveImageWidthFromAsset(layer, size); err != nil {
				return false, err
			}
			layer.DeriveWidthFromAsset = false
			continue
		}
		if !layer.EntityImage {
			continue
		}
		size, err := probedImageSize(layer, sizes)
		if err != nil {
			return false, err
		}
		changed = true
		if err := fitEntityImageGeometry(layer, size); err != nil {
			return false, err
		}
		if err := ResizePremiumImageMask(layer); err != nil {
			return false, fmt.Errorf("overlay: resize premium image mask for %q: %w", layer.ID, err)
		}
		if err := alignEntityCaptionToFittedImage(plan, layer); err != nil {
			return false, fmt.Errorf("overlay: align entity caption for %q: %w", layer.ID, err)
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
				SyncPremiumComponentTransform(layer, component)
			}
			if component.PremiumPathKind == "rounded_rect" && component.Shape != nil {
				radius := layer.Radius * component.PremiumRadiusScale
				if radius == 0 {
					radius = layer.Radius
				}
				component.Shape.Path = RoundedPremiumRectPath(component.Size[0], component.Size[1], radius)
				if component.PremiumShapeKind == "rounded_rect" {
					component.Shape.Radius = radius
				}
			}
		}
	}
	if err := ValidateEntityCaptionCollisions(plan.Layers, plan.Canvas.Width, plan.Canvas.Height); err != nil {
		return false, fmt.Errorf("overlay: fitted entity caption layout: %w", err)
	}
	return changed, nil
}

// applyProbedImageGeometry resolves a just-lowered image layer against the
// probed sizes, BEFORE premium components and captions are built from its
// geometry. nil sizes is the blind compile: the transient markers stay and a
// later FitImageLayersToSizes pass may finish them. A non-nil map is the sized
// compile: a missing entry is an internal error, never a guessed box.
func applyProbedImageGeometry(layer *Layer, sizes map[string]ImageSize) error {
	if layer == nil || layer.Type != "image" || sizes == nil {
		return nil
	}
	if layer.DeriveWidthFromAsset && !layer.EntityImage {
		size, err := probedImageSize(layer, sizes)
		if err != nil {
			return err
		}
		layer.DeriveWidthFromAsset = false
		if err := deriveImageWidthFromAsset(layer, size); err != nil {
			return err
		}
		layer.SourceFitted = true
		return nil
	}
	if !layer.EntityImage {
		return nil
	}
	size, err := probedImageSize(layer, sizes)
	if err != nil {
		return err
	}
	layer.EntityImage = false
	if err := fitEntityImageGeometry(layer, size); err != nil {
		return err
	}
	layer.SourceFitted = true
	return ResizePremiumImageMask(layer)
}

// fitEntityImageGeometry applies the certified entity-card geometry rule. Use
// the authored contain box, not the render canvas: the box is already in the
// canonical design space, so a 1280x720 job and a 1920x1080 job settle on the
// same 16:9 card. Scaling by canvas width made the card shrink with the
// preview/output size and broke the linked premium frame geometry. A portrait
// source keeps its aspect through the shared contain fit.
func fitEntityImageGeometry(layer *Layer, size ImageSize) error {
	if size.Width <= 0 || size.Height <= 0 {
		return fmt.Errorf("overlay: image layer %q has invalid source size %dx%d", layer.ID, size.Width, size.Height)
	}
	if float64(size.Width)/float64(size.Height) >= 1.2 {
		width := layer.BoxWidth
		if width <= 0 {
			return fmt.Errorf("overlay: invalid box width %d for entity image layer %q", layer.BoxWidth, layer.ID)
		}
		height := max(1, width*9/16)
		layer.BoxWidth, layer.BoxHeight = width, height
		layer.Size = []float64{float64(width), float64(height)}
		layer.Fit = FitCover
		return nil
	}
	if err := FitEntityImageLayerToAsset(layer, size.Width, size.Height); err != nil {
		return fmt.Errorf("overlay: fit entity image layer %q to source aspect: %w", layer.ID, err)
	}
	return nil
}

func probedImageSize(layer *Layer, sizes map[string]ImageSize) (ImageSize, error) {
	size, ok := sizes[layer.Asset]
	if !ok {
		return ImageSize{}, fmt.Errorf("overlay: image layer %q awaits source dimensions but none were probed for %q", layer.ID, layer.Asset)
	}
	if size.Width <= 0 || size.Height <= 0 {
		return ImageSize{}, fmt.Errorf("overlay: image layer %q has invalid probed source size %dx%d", layer.ID, size.Width, size.Height)
	}
	return size, nil
}

// deriveImageWidthFromAsset resolves the one part of a standalone card's
// geometry the author cannot know at payload time: when only the height is
// authored, the width that preserves the photo's aspect. Without it the plan
// carries a guessed box and a cover crop eats most of a portrait.
func deriveImageWidthFromAsset(layer *Layer, size ImageSize) error {
	if layer.BoxHeight <= 0 {
		return fmt.Errorf("overlay: image layer %q asks for an asset-derived width without a positive height", layer.ID)
	}
	if size.Width <= 0 || size.Height <= 0 {
		return fmt.Errorf("overlay: image layer %q has invalid probed source size %dx%d", layer.ID, size.Width, size.Height)
	}
	width := max(1, int(float64(layer.BoxHeight)*float64(size.Width)/float64(size.Height)+0.5))
	layer.BoxWidth = width
	layer.Size = []float64{float64(width), float64(layer.BoxHeight)}
	return nil
}

// alignEntityCaptionToFittedImage recomputes caption anchoring after the image
// box has been fitted to the verified source aspect ratio. Semantic lowering
// only knows the requested contain box; without this pass a wide source can
// shrink vertically while its name stays at the old box bottom, leaving an
// oversized gap between portrait and label.
func alignEntityCaptionToFittedImage(plan *Plan, image *Layer) error {
	if plan == nil || image == nil || len(image.Size) < 2 || len(image.Position) < 2 {
		return nil
	}
	for i := range plan.Layers {
		caption := &plan.Layers[i]
		if caption.CaptionForImageID != image.ID {
			continue
		}
		// A chain camera pan keeps every member on its strip slot; fit the pair
		// in the neutral centre pose, then restore the strip translation so the
		// group track stays the only owner of the horizontal (or vertical) move.
		offsetX, offsetY := 0.0, 0.0
		if len(image.ChainPanOffset) == 2 {
			offsetX, offsetY = image.ChainPanOffset[0], image.ChainPanOffset[1]
		}
		basePosition := []float64{image.Position[0] - offsetX, image.Position[1] - offsetY}
		bounds := EntityCardImageBoundsFromCenter(
			plan.Canvas.Width, plan.Canvas.Height, basePosition, image.Size[0], image.Size[1],
		)
		layout, err := ResolveEntityCardLayoutAt(plan.Canvas.Width, plan.Canvas.Height, bounds, caption.Text, caption.CaptionLayout)
		if err != nil {
			return err
		}
		image.Position[1] = layout.ImageBounds.Y + image.Size[1]/2 - float64(plan.Canvas.Height)/2 + offsetY
		caption.Position = []float64{layout.CaptionBounds.CenterX + offsetX, layout.CaptionBounds.CenterY + offsetY}
		caption.Size = []float64{layout.CaptionBounds.Width, layout.CaptionBounds.Height}
		caption.BoxWidth = int(layout.CaptionBounds.Width)
		caption.BoxHeight = int(layout.CaptionBounds.Height)
		if caption.Style != nil {
			caption.Style.FontSize = layout.CaptionBounds.FontSize
			pinCaptionFitSize(caption.Style)
		}
		return nil
	}
	return nil
}
