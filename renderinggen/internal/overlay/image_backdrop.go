package overlay

import (
	"fmt"
)

const (
	defaultSingleImageBackdropZoom = 1.15
	defaultSingleImageBackdropBlur = 18.0
	maxSingleImageBackdropZoom     = 10.0
	maxSingleImageBackdropBlur     = 48.0
)

// addSingleImageBackdrop adds a restrained, full-canvas copy of the same
// source image behind a standalone image. It is deliberately unavailable for
// image_layers composites and groups: those need independent member frames,
// not one ambiguous shared background.
func addSingleImageBackdrop(ri resolvedItem, src *semanticPlan, registry *assetRegistry, layers []Layer) ([]Layer, error) {
	raw, requested := ri.Params["image_backdrop"]
	if !requested || raw == nil {
		return layers, nil
	}
	if len(ri.Item.ImageLayers) != 0 || len(ri.Item.Assets) != 1 {
		return nil, fmt.Errorf("overlay: image_backdrop is available only for a standalone image with exactly one asset")
	}
	if src == nil || src.Width <= 0 || src.Height <= 0 {
		return nil, fmt.Errorf("overlay: image_backdrop requires a positive canvas")
	}
	params, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q image_backdrop must be an object", ri.Item.ID)
	}
	for key := range params {
		if key != "zoom" && key != "blur_px" {
			return nil, fmt.Errorf("overlay: item %q image_backdrop has unsupported property %q", ri.Item.ID, key)
		}
	}
	zoom, blur := defaultSingleImageBackdropZoom, defaultSingleImageBackdropBlur
	if value, exists := params["zoom"]; exists {
		var valid bool
		zoom, valid = numericValue(value)
		if !valid || !finite(zoom) || zoom < 1 || zoom > maxSingleImageBackdropZoom {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.zoom must be within [1,%g]", ri.Item.ID, maxSingleImageBackdropZoom)
		}
	}
	if value, exists := params["blur_px"]; exists {
		var valid bool
		blur, valid = numericValue(value)
		if !valid || !finite(blur) || blur < 6 || blur > maxSingleImageBackdropBlur {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.blur_px must be within [6,%g]", ri.Item.ID, maxSingleImageBackdropBlur)
		}
	}
	assetPath := registry.Path(ri.Item.Assets[0].ID)
	backdrop := Layer{
		ID:             ri.Item.ID + ":image-backdrop",
		Type:           "image",
		Asset:          assetPath,
		BoxWidth:       src.Width,
		BoxHeight:      src.Height,
		Size:           []float64{float64(src.Width), float64(src.Height)},
		Fit:            FitCover,
		Position:       []float64{0, 0},
		Scale:          []float64{zoom, zoom},
		StartFrame:     ri.Start,
		DurationFrames: ri.End - ri.Start,
		Effects:        []LayerEffect{{Type: "gaussian_blur", Params: map[string]any{"radius": blur}}},
	}
	return append([]Layer{backdrop}, layers...), nil
}
