package overlay

import (
	"fmt"
)

const (
	defaultSingleImageBackdropZoom = 1.15
	defaultSingleImageBackdropBlur = 18.0
	maxSingleImageBackdropZoom     = 10.0
	maxSingleImageBackdropBlur     = 48.0

	// The light canvas grid paired with the blurred backdrop mirrors the
	// verified editorial image_backdrop_grid recipe: a restrained cool line
	// grid over the whole frame.
	defaultBackdropGridColor     = "#7FA8C8"
	defaultBackdropGridSpacing   = 120.0
	defaultBackdropGridLineWidth = 1.0
	defaultBackdropGridDotRadius = 2.0
	defaultBackdropGridOpacity   = 0.1
)

// addSingleImageBackdrop adds a restrained, full-canvas copy of the same
// source image behind a standalone image. It is deliberately unavailable for
// image_layers composites and groups: those need independent member frames,
// not one ambiguous shared background.
//
// The optional image_backdrop.grid block draws one light full-canvas grid over
// the composed card. It is the same screen-space shape clip_grid uses, so the
// payload controls color, spacing, line width, dot radius and opacity with the
// one shape contract instead of a second grid implementation.
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
		if key != "zoom" && key != "blur_px" && key != "grid" {
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
	out := append([]Layer{backdrop}, layers...)
	gridRaw, hasGrid := params["grid"]
	if !hasGrid || gridRaw == nil {
		return out, nil
	}
	grid, err := parseBackdropGrid(gridRaw, ri.Item.ID)
	if err != nil {
		return nil, err
	}
	if grid == nil {
		return out, nil
	}
	gridLayer, err := compileClipGrid(grid, src.Width, src.Height, ri.End-ri.Start)
	if err != nil {
		return nil, fmt.Errorf("overlay: item %q image_backdrop.grid: %w", ri.Item.ID, err)
	}
	gridLayer.ID = ri.Item.ID + ":image-grid"
	gridLayer.StartFrame = ri.Start
	gridLayer.DurationFrames = ri.End - ri.Start
	return append(out, gridLayer), nil
}

// parseBackdropGrid decodes the optional light-grid declaration. A nil result
// means the block explicitly disabled the grid; malformed input is always an
// error so a producer never gets a silently missing treatment.
func parseBackdropGrid(raw any, itemID string) (*semanticClipGrid, error) {
	params, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q image_backdrop.grid must be an object", itemID)
	}
	for key := range params {
		switch key {
		case "enabled", "style", "color", "spacing_px", "line_width_px", "dot_radius_px", "opacity":
		default:
			return nil, fmt.Errorf("overlay: item %q image_backdrop.grid has unsupported property %q", itemID, key)
		}
	}
	if value, exists := params["enabled"]; exists {
		enabled, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.grid.enabled must be a boolean", itemID)
		}
		if !enabled {
			return nil, nil
		}
	}
	grid := semanticClipGrid{
		Style:     "lines",
		SpacingPX: defaultBackdropGridSpacing,
		Color:     defaultBackdropGridColor,
		Opacity:   defaultBackdropGridOpacity,
	}
	if value, exists := params["style"]; exists {
		style, ok := value.(string)
		if !ok || (style != "lines" && style != "line" && style != "grid" && style != "dots" && style != "dot_grid") {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.grid.style must be lines or dots", itemID)
		}
		grid.Style = style
	}
	if value, exists := params["color"]; exists {
		color, ok := value.(string)
		if !ok || !isHexColor(color) {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.grid.color must be #RRGGBB", itemID)
		}
		grid.Color = color
	}
	var err error
	if spacing, numberErr := backdropGridNumber(params, itemID, "spacing_px", 4, 512); numberErr != nil {
		return nil, numberErr
	} else if spacing != nil {
		grid.SpacingPX = *spacing
	}
	if grid.LineWidthPX, err = backdropGridNumber(params, itemID, "line_width_px", 0.25, 32); err != nil {
		return nil, err
	}
	if grid.DotRadiusPX, err = backdropGridNumber(params, itemID, "dot_radius_px", 0.5, 64); err != nil {
		return nil, err
	}
	if opacity, numberErr := backdropGridNumber(params, itemID, "opacity", 0, 1); numberErr != nil {
		return nil, numberErr
	} else if opacity != nil {
		grid.Opacity = *opacity
	}
	isDots := grid.Style == "dots" || grid.Style == "dot_grid"
	if isDots {
		if grid.LineWidthPX != nil {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.grid.line_width_px is valid only for line grids", itemID)
		}
		if grid.DotRadiusPX == nil {
			radius := defaultBackdropGridDotRadius
			grid.DotRadiusPX = &radius
		}
		return &grid, nil
	}
	if grid.DotRadiusPX != nil {
		return nil, fmt.Errorf("overlay: item %q image_backdrop.grid.dot_radius_px is valid only for dot grids", itemID)
	}
	if grid.LineWidthPX == nil {
		width := defaultBackdropGridLineWidth
		grid.LineWidthPX = &width
	}
	return &grid, nil
}

func backdropGridNumber(params map[string]any, itemID, key string, minimum, maximum float64) (*float64, error) {
	value, exists := params[key]
	if !exists {
		return nil, nil
	}
	number, ok := numericValue(value)
	if !ok || !finite(number) || number < minimum || number > maximum {
		return nil, fmt.Errorf("overlay: item %q image_backdrop.grid.%s must be within [%g,%g]", itemID, key, minimum, maximum)
	}
	return &number, nil
}
