package overlay

import (
	"fmt"
	"math"
	"strings"
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

// imageBackdropSpec is the validated image_backdrop block shared by every
// lowering path (standalone image, composite entity portraits, chain
// segments). assetPath stays empty when the caller must supply the member's
// own asset, which is how a composite blurs each portrait behind itself
// instead of one ambiguous shared background.
type imageBackdropSpec struct {
	zoom      float64
	blur      float64
	assetPath string
	// explicitAsset records that asset_id was authored. Without it a
	// composite must use each member's own asset, never the item's first one.
	explicitAsset bool
	grid          *semanticClipGrid
}

func parseImageBackdrop(ri resolvedItem, src *semanticPlan, registry *assetRegistry) (*imageBackdropSpec, error) {
	raw, requested := ri.Params["image_backdrop"]
	if !requested || raw == nil {
		return nil, nil
	}
	if src == nil || src.Width <= 0 || src.Height <= 0 {
		return nil, fmt.Errorf("overlay: image_backdrop requires a positive canvas")
	}
	params, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q image_backdrop must be an object", ri.Item.ID)
	}
	for key := range params {
		if key != "zoom" && key != "blur_px" && key != "grid" && key != "asset_id" {
			return nil, fmt.Errorf("overlay: item %q image_backdrop has unsupported property %q", ri.Item.ID, key)
		}
	}
	spec := &imageBackdropSpec{
		zoom: defaultSingleImageBackdropZoom,
		blur: defaultSingleImageBackdropBlur,
	}
	backdropAssetID := ""
	if value, exists := params["asset_id"]; exists && value != nil {
		id, ok := value.(string)
		if !ok || id == "" {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.asset_id must be a non-empty string", ri.Item.ID)
		}
		backdropAssetID = id
	}
	if value, exists := params["zoom"]; exists {
		var valid bool
		spec.zoom, valid = numericValue(value)
		if !valid || !finite(spec.zoom) || spec.zoom < 1 || spec.zoom > maxSingleImageBackdropZoom {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.zoom must be within [1,%g]", ri.Item.ID, maxSingleImageBackdropZoom)
		}
	}
	if value, exists := params["blur_px"]; exists {
		var valid bool
		spec.blur, valid = numericValue(value)
		if !valid || !finite(spec.blur) || spec.blur < 6 || spec.blur > maxSingleImageBackdropBlur {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.blur_px must be within [6,%g]", ri.Item.ID, maxSingleImageBackdropBlur)
		}
	}
	if backdropAssetID != "" {
		found := false
		spec.explicitAsset = true
		for _, ref := range ri.Item.Assets {
			if ref.ID == backdropAssetID {
				spec.assetPath = registry.Path(ref.ID)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.asset_id %q references an undeclared asset", ri.Item.ID, backdropAssetID)
		}
	} else if len(ri.Item.Assets) > 0 {
		spec.assetPath = registry.Path(ri.Item.Assets[0].ID)
	}
	if gridRaw, hasGrid := params["grid"]; hasGrid && gridRaw != nil {
		grid, err := parseBackdropGrid(gridRaw, ri.Item.ID)
		if err != nil {
			return nil, err
		}
		spec.grid = grid
	}
	return spec, nil
}

// layer builds the full-canvas blurred copy for one asset and window. The
// layer id is explicit so composites and chain segments keep one backdrop per
// member without id collisions.
func (spec *imageBackdropSpec) layer(ri resolvedItem, src *semanticPlan, assetPath, id string, start, end int64) Layer {
	return Layer{
		ID:             id,
		Type:           "image",
		Asset:          assetPath,
		BoxWidth:       src.Width,
		BoxHeight:      src.Height,
		Size:           []float64{float64(src.Width), float64(src.Height)},
		Fit:            FitCover,
		Position:       []float64{0, 0},
		Scale:          []float64{spec.zoom, spec.zoom},
		StartFrame:     start,
		DurationFrames: end - start,
		Effects:        []LayerEffect{{Type: "gaussian_blur", Params: map[string]any{"radius": spec.blur}}},
	}
}

// addBackdropGrid appends the optional light canvas grid described by the same
// image_backdrop block. It is one screen-space layer regardless of how many
// member backdrops were emitted.
func addBackdropGrid(ri resolvedItem, src *semanticPlan, spec *imageBackdropSpec, layers []Layer) ([]Layer, error) {
	if spec == nil || spec.grid == nil {
		return layers, nil
	}
	gridLayer, err := compileBackdropGrid(spec.grid, src.Width, src.Height, ri.End-ri.Start)
	if err != nil {
		return nil, fmt.Errorf("overlay: item %q image_backdrop.grid: %w", ri.Item.ID, err)
	}
	gridLayer.ID = ri.Item.ID + ":image-grid"
	gridLayer.StartFrame = ri.Start
	gridLayer.DurationFrames = ri.End - ri.Start
	return append(layers, gridLayer), nil
}

// addSingleImageBackdrop adds a restrained, full-canvas copy of the same
// source image behind a standalone image.
func addSingleImageBackdrop(ri resolvedItem, src *semanticPlan, registry *assetRegistry, layers []Layer) ([]Layer, error) {
	spec, err := parseImageBackdrop(ri, src, registry)
	if err != nil || spec == nil {
		return layers, err
	}
	assetPath := spec.assetPath
	if assetPath == "" {
		return nil, fmt.Errorf("overlay: image_backdrop requires at least one asset")
	}
	out := append([]Layer{spec.layer(ri, src, assetPath, ri.Item.ID+":image-backdrop", ri.Start, ri.End)}, layers...)
	return addBackdropGrid(ri, src, spec, out)
}

// addCompositeImageBackdrops adds one backdrop per member image layer: entity
// portraits rendered as an image_layers composite blur their OWN photo behind
// the card, so a co-present group never borrows a sibling's background. An
// explicit image_backdrop.asset_id overrides the per-member choice and yields
// one shared backdrop for the whole item.
func addCompositeImageBackdrops(ri resolvedItem, src *semanticPlan, registry *assetRegistry, layers []Layer) ([]Layer, error) {
	spec, err := parseImageBackdrop(ri, src, registry)
	if err != nil || spec == nil {
		return layers, err
	}
	if spec.explicitAsset {
		out := append([]Layer{spec.layer(ri, src, spec.assetPath, ri.Item.ID+":image-backdrop", ri.Start, ri.End)}, layers...)
		return addBackdropGrid(ri, src, spec, out)
	}
	seen := make(map[string]struct{}, len(layers))
	out := make([]Layer, 0, len(layers)*2)
	for _, layer := range layers {
		if layer.Type != "image" || !strings.HasSuffix(layer.ID, ":image") || layer.Asset == "" {
			continue
		}
		if _, duplicate := seen[layer.Asset]; duplicate {
			continue
		}
		seen[layer.Asset] = struct{}{}
		out = append(out, spec.layer(ri, src, layer.Asset, layer.ID+"-backdrop",
			layer.StartFrame, layer.StartFrame+layer.DurationFrames))
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("overlay: item %q image_backdrop found no member image layer to blur", ri.Item.ID)
	}
	out = append(out, layers...)
	return addBackdropGrid(ri, src, spec, out)
}

// compileBackdropGrid lowers the light canvas grid. Dots keep the shared
// clip_grid path (filled dots render full-frame). Lines need a dedicated path:
// the renderer's stroker clips a generated centred path to its positive local
// quadrant, so a centred line grid renders only the top-left quarter. Building
// the same lines in 0..width / 0..height and anchoring the layer at the canvas
// top-left keeps every segment in the visible frame.
func compileBackdropGrid(grid *semanticClipGrid, width, height int, duration int64) (Layer, error) {
	return compileBackdropGridWithScale(grid, width, height, duration, 0)
}

func compileBackdropGridWithScale(grid *semanticClipGrid, width, height int, duration int64, foregroundScale int) (Layer, error) {
	if grid.Style == "dots" || grid.Style == "dot_grid" {
		// compileClipGrid applies the foreground scale itself and needs the
		// full canvas to centre the scaled box. Pre-scaling here scaled the
		// grid twice and centred it against its own smaller box.
		return compileClipGrid(grid, width, height, duration, foregroundScale)
	}
	w, h := width, height
	if grid != nil && grid.Scope == "foreground" && foregroundScale > 0 && foregroundScale < 100 {
		w = width * foregroundScale / 100
		h = height * foregroundScale / 100
	}
	return compileBackdropLineGrid(grid, w, h, duration)
}

// maxBackdropGridLines mirrors the renderer's generated-grid bound. A grid
// that cannot be represented within it fails closed instead of producing a
// partial treatment.
const maxBackdropGridLines = 4096

// maxBackdropGridDots mirrors the renderer's generated-grid bound for the dot
// style, which rasterises one primitive per cell instead of two path commands.
const maxBackdropGridDots = 4096

func compileBackdropLineGrid(grid *semanticClipGrid, width, height int, duration int64) (Layer, error) {
	if grid == nil || grid.LineWidthPX == nil {
		return Layer{}, fmt.Errorf("line_width_px is required for a line grid")
	}
	if width <= 0 || height <= 0 {
		return Layer{}, fmt.Errorf("line grid requires a positive canvas")
	}
	if !finite(grid.SpacingPX) || grid.SpacingPX <= 0 {
		return Layer{}, fmt.Errorf("line grid spacing must be positive")
	}
	commands, err := lineGridCommands(float64(width), float64(height), grid.SpacingPX, *grid.LineWidthPX)
	if err != nil {
		return Layer{}, err
	}
	opacity := grid.Opacity
	return Layer{
		ID:             "clip__grid",
		Type:           "shape",
		BoxWidth:       width,
		BoxHeight:      height,
		Size:           []float64{float64(width), float64(height)},
		Position:       []float64{-float64(width) / 2, -float64(height) / 2},
		ScreenSpace:    true,
		Opacity:        &opacity,
		StartFrame:     0,
		DurationFrames: duration,
		Shape: &LayerShape{
			Type:   "path",
			Path:   commands,
			Stroke: &LayerStroke{Color: grid.Color, Width: *grid.LineWidthPX},
		},
	}, nil
}

// lineGridCommands lays the mesh out on a whole number of cells so the frame
// closes on all four edges. The old layout stepped by spacing from 0 and added
// one extra line, so the remainder became a gap on one axis and a border line
// cut in half on the other: 72 px on 1920x1080 left 48 px open on the right and
// ran the bottom line through the frame edge. Rounding the spacing to the
// nearest whole cell keeps the requested density and closes the frame.
//
// The mesh is inset by half the stroke so every border line keeps its full
// width inside the box, and each stroke centre is snapped to the pixel grid: a
// 1 px line centred on an integer lands on two half-opacity pixels, which at
// grid opacity reads as dirt instead of as a line.
func lineGridCommands(width, height, spacing, lineWidth float64) ([]LayerPathCommand, error) {
	inset := lineWidth / 2
	innerWidth := width - lineWidth
	innerHeight := height - lineWidth
	if innerWidth <= 0 || innerHeight <= 0 {
		return nil, fmt.Errorf("line grid requires a canvas wider than its stroke")
	}
	columns := int(math.Round(innerWidth / spacing))
	if columns < 1 {
		columns = 1
	}
	rows := int(math.Round(innerHeight / spacing))
	if rows < 1 {
		rows = 1
	}
	if columns+rows+2 > maxBackdropGridLines {
		return nil, fmt.Errorf("line grid exceeds %d lines; increase spacing_px", maxBackdropGridLines)
	}
	top := snapStrokeCentre(inset, lineWidth)
	bottom := snapStrokeCentre(height-inset, lineWidth)
	left := snapStrokeCentre(inset, lineWidth)
	right := snapStrokeCentre(width-inset, lineWidth)
	commands := make([]LayerPathCommand, 0, (columns+rows+2)*2)
	for column := 0; column <= columns; column++ {
		x := snapStrokeCentre(inset+float64(column)*innerWidth/float64(columns), lineWidth)
		commands = append(commands,
			LayerPathCommand{Type: "move_to", Point: []float64{x, top}},
			LayerPathCommand{Type: "line_to", Point: []float64{x, bottom}},
		)
	}
	for row := 0; row <= rows; row++ {
		y := snapStrokeCentre(inset+float64(row)*innerHeight/float64(rows), lineWidth)
		commands = append(commands,
			LayerPathCommand{Type: "move_to", Point: []float64{left, y}},
			LayerPathCommand{Type: "line_to", Point: []float64{right, y}},
		)
	}
	return commands, nil
}

// snapStrokeCentre moves a stroke centre so the interval it covers lands on
// whole pixels: a 1 px stroke gets a .5 centre, a 2 px stroke an integer one.
func snapStrokeCentre(centre, lineWidth float64) float64 {
	return math.Round(centre-lineWidth/2) + lineWidth/2
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
		case "enabled", "scope", "style", "color", "spacing_px", "line_width_px", "dot_radius_px", "opacity":
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
	if value, exists := params["scope"]; exists {
		scope, ok := value.(string)
		if !ok || (scope != "full_canvas" && scope != "foreground") {
			return nil, fmt.Errorf("overlay: item %q image_backdrop.grid.scope must be full_canvas or foreground", itemID)
		}
		grid.Scope = scope
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
