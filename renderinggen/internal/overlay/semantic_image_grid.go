package overlay

import "fmt"

// addStandaloneImageGrid lowers an item-level params.grid block as a
// full-canvas screen-space grid bound to the item lifetime. It is the image
// counterpart of plan-level clip_grid: same geometry contract (spacing, color,
// line/dot styling, opacity) plus the same scope switch
// (full_canvas/foreground). Unlike image_backdrop.grid it needs no blurred
// backdrop and works for standalone images, composites and entity cards.
func addStandaloneImageGrid(ri resolvedItem, src *semanticPlan, layers []Layer) ([]Layer, error) {
	raw, requested := ri.Params["grid"]
	if !requested || raw == nil {
		return layers, nil
	}
	if src == nil || src.Width <= 0 || src.Height <= 0 {
		return nil, fmt.Errorf("overlay: item %q grid requires a positive canvas", ri.Item.ID)
	}
	grid, err := parseStandaloneImageGrid(raw, ri.Item.ID)
	if err != nil {
		return nil, err
	}
	if grid == nil {
		return layers, nil
	}
	gridLayer, err := compileBackdropGridWithScale(grid, src.Width, src.Height, ri.End-ri.Start, src.ForegroundScale)
	if err != nil {
		return nil, fmt.Errorf("overlay: item %q grid: %w", ri.Item.ID, err)
	}
	gridLayer.ID = ri.Item.ID + ":image-grid"
	gridLayer.StartFrame = ri.Start
	gridLayer.DurationFrames = ri.End - ri.Start
	return append(layers, gridLayer), nil
}

// parseStandaloneImageGrid decodes params.grid with the same bounds as
// image_backdrop.grid plus the clip_grid scope switch. A nil result means the
// block explicitly disabled the grid.
func parseStandaloneImageGrid(raw any, itemID string) (*semanticClipGrid, error) {
	params, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q grid must be an object", itemID)
	}
	// Reuse the backdrop grid parser so bounds cannot drift; rewrite the item
	// id in errors from image_backdrop.grid to grid.
	grid, err := parseBackdropGrid(params, itemID)
	if err != nil {
		return nil, err
	}
	return grid, nil
}
