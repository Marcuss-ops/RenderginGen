package overlay

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/tacticalfill"
)

// semantic_map_modern_layers.go owns the individual ModernMap1 layer builders:
// territory shape, paper, graticule, context borders, halo, label, credit and
// logo. The composition order and the accessors live in
// semantic_map_modern.go.

// modernMapShapeLayer lowers one stroked path shape. Path points are authored
// inside [0,size] in the shape's own bbox, and the position is that bbox's
// top-left corner in canvas coordinates (the same convention Grid uses).
func modernMapShapeLayer(id string, ri resolvedItem, src *semanticPlan, duration int64, rings [][][2]float64, fill any, stroke *LayerStroke, opacity *float64) (Layer, bool) {
	path, size, position := modernMapPathGeometry(rings, src.Width, src.Height)
	if len(path) == 0 {
		return Layer{}, false
	}
	return Layer{
		ID:             id,
		Type:           "shape",
		Position:       position,
		Size:           size,
		StartFrame:     ri.Start,
		DurationFrames: duration,
		Opacity:        opacity,
		Shape:          &LayerShape{Type: "path", Path: path, Fill: fill, Stroke: stroke},
	}, true
}

// modernMapPaperLayer paints the sea: a warm paper rect under everything, so
// the plate never inherits the job's dark background.
func modernMapPaperLayer(ri resolvedItem, src *semanticPlan, duration int64) Layer {
	return Layer{
		ID:             ri.Item.ID + ":modernmap1:paper",
		Type:           "shape",
		Position:       []float64{0, 0},
		Size:           []float64{float64(src.Width), float64(src.Height)},
		StartFrame:     ri.Start,
		DurationFrames: duration,
		Shape:          &LayerShape{Type: "rect", Fill: modernMapPaper},
	}
}

// modernMapGraticuleLayer draws the meridian/parallel lattice used by the
// ChrononPlugin paper basemap. The step follows the fit zoom, and only lines
// that cross the canvas are emitted.
func modernMapGraticuleLayer(ri resolvedItem, src *semanticPlan, fit tacticalfill.Fit, duration int64) *Layer {
	step := 10.0
	switch {
	case fit.Zoom > 10:
		step = 1
	case fit.Zoom > 8:
		step = 2
	case fit.Zoom > 6:
		step = 5
	}
	commands := make([]LayerPathCommand, 0, 64)
	for lon := -180.0; lon <= 180.0; lon += step {
		point := fit.Project(lon, 0)
		if point[0] < -4 || point[0] > float64(src.Width)+4 {
			continue
		}
		commands = append(commands,
			LayerPathCommand{Type: "move_to", Point: []float64{point[0], 0}},
			LayerPathCommand{Type: "line_to", Point: []float64{point[0], float64(src.Height)}},
		)
	}
	for lat := -80.0; lat <= 80.0; lat += step {
		point := fit.Project(0, lat)
		if point[1] < -4 || point[1] > float64(src.Height)+4 {
			continue
		}
		commands = append(commands,
			LayerPathCommand{Type: "move_to", Point: []float64{0, point[1]}},
			LayerPathCommand{Type: "line_to", Point: []float64{float64(src.Width), point[1]}},
		)
	}
	if len(commands) == 0 {
		return nil
	}
	opacity := 0.6
	return &Layer{
		ID:             ri.Item.ID + ":modernmap1:graticule",
		Type:           "shape",
		Position:       []float64{-float64(src.Width) / 2, -float64(src.Height) / 2},
		Size:           []float64{float64(src.Width), float64(src.Height)},
		StartFrame:     ri.Start,
		DurationFrames: duration,
		Opacity:        &opacity,
		Shape:          &LayerShape{Type: "path", Path: commands, Stroke: &LayerStroke{Color: modernMapGridLine, Width: 1}},
	}
}

// modernMapContextBorderLayers strokes the coastlines and borders of every
// country that intersects the fitted viewport. Land fill is deliberately absent:
// a filled country needs triangles or a long fill path, and the hairline map is
// the editorial reference (and the only thing the renderer draws reliably at
// this scale).
func modernMapContextBorderLayers(ri resolvedItem, src *semanticPlan, features *geofeatures.Registry, target geofeatures.Feature, fit tacticalfill.Fit, duration int64) []Layer {
	west, north := fit.Unproject(0, 0)
	east, south := fit.Unproject(float64(src.Width), float64(src.Height))
	if west > east {
		west, east = east, west
	}
	if south > north {
		south, north = north, south
	}
	marginLon := (east - west) * 0.02
	marginLat := (north - south) * 0.02
	west, east = west-marginLon, east+marginLon
	south, north = south-marginLat, north+marginLat

	type candidate struct {
		rings [][][2]float64
		area  float64
	}
	candidates := make([]candidate, 0, 32)
	for _, country := range features.Features() {
		if country.Type != geofeatures.Country || country.ID == target.ID {
			continue
		}
		if country.Bounds.East < west || country.Bounds.West > east ||
			country.Bounds.North < south || country.Bounds.South > north {
			continue
		}
		rings, err := features.Rings(country.ID)
		if err != nil {
			continue
		}
		projected := make([][][2]float64, 0, len(rings))
		var area float64
		for _, ring := range rings {
			points := make([][2]float64, 0, len(ring))
			for _, point := range ring {
				projectedPoint := fit.Project(point[0], point[1])
				// Clamp to a small margin around the canvas: an off-canvas
				// ring must not inflate the layer surface to thousands of
				// pixels, which starves the native surface pool.
				projectedPoint[0] = math.Max(-40, math.Min(float64(src.Width)+40, projectedPoint[0]))
				projectedPoint[1] = math.Max(-40, math.Min(float64(src.Height)+40, projectedPoint[1]))
				points = append(points, projectedPoint)
			}
			points = decimateModernMapRing(points, modernMapContextRingPoints)
			minX, minY, maxX, maxY := modernMapRingBounds(points)
			if maxX < -8 || minX > float64(src.Width)+8 || maxY < -8 || minY > float64(src.Height)+8 {
				continue
			}
			if (maxX-minX)*(maxY-minY) < 24 {
				continue
			}
			projected = append(projected, points)
			area += (maxX - minX) * (maxY - minY)
		}
		if len(projected) == 0 {
			continue
		}
		candidates = append(candidates, candidate{rings: projected, area: area})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].area > candidates[j].area })
	if len(candidates) > modernMapMaxContextLayers {
		candidates = candidates[:modernMapMaxContextLayers]
	}
	// Split the rings across a few paths: one 10k-point stroked path is dropped
	// by the renderer, while a handful of ~1k-point paths draw fine.
	rows := make([][][][2]float64, 0, 4)
	row := make([][][2]float64, 0, 8)
	rowPoints := 0
	totalPoints := 0
	for _, candidate := range candidates {
		for _, ring := range candidate.rings {
			if totalPoints >= modernMapMaxBorderPoints {
				break
			}
			if rowPoints+len(ring) > modernMapMaxBorderPointsPerRow && len(row) > 0 {
				rows = append(rows, row)
				row = make([][][2]float64, 0, 8)
				rowPoints = 0
			}
			row = append(row, ring)
			rowPoints += len(ring)
			totalPoints += len(ring)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	layers := make([]Layer, 0, len(rows))
	for index, rings := range rows {
		if layer, ok := modernMapShapeLayer(
			fmt.Sprintf("%s:modernmap1:borders:%d", ri.Item.ID, index), ri, src, duration,
			rings, nil, &LayerStroke{Color: modernMapBorderLine, Width: 1.4}, nil,
		); ok {
			layers = append(layers, layer)
		}
	}
	return layers
}

// modernMapTerritoryHaloLayer is the soft edge that makes the accent territory
// sit on the paper instead of being stamped onto it.
func modernMapTerritoryHaloLayer(ri resolvedItem, src *semanticPlan, rings [][][2]float64, duration int64) *Layer {
	if len(rings) == 0 {
		return nil
	}
	opacity := 0.14
	layer, ok := modernMapShapeLayer(ri.Item.ID+":modernmap1:halo", ri, src, duration, rings, nil,
		&LayerStroke{Color: modernMapHaloLine, Width: 22}, &opacity)
	if !ok {
		return nil
	}
	return &layer
}

// modernMapTerritoryHaloLayers stacks a wide faint lift with a tighter core
// glow so the territory reads with depth instead of a single flat ring.
func modernMapTerritoryHaloLayers(ri resolvedItem, src *semanticPlan, rings [][][2]float64, duration int64) []Layer {
	out := make([]Layer, 0, 2)
	if halo := modernMapTerritoryHaloLayer(ri, src, rings, duration); halo != nil {
		out = append(out, *halo)
	}
	coreOpacity := 0.22
	if core, ok := modernMapShapeLayer(ri.Item.ID+":modernmap1:halo:core", ri, src, duration, rings, nil,
		&LayerStroke{Color: modernMapHaloLine, Width: 10}, &coreOpacity); ok {
		out = append(out, core)
	}
	return out
}

// modernMapLabelLayer writes the place name bottom-left in editorial ink. The
// map-label role supplies shrink-only fitting; its default dark drop shadow is
// disabled because the plate is paper, not footage.
func modernMapLabelLayer(ri resolvedItem, src *semanticPlan, feature geofeatures.Feature, duration int64) (Layer, error) {
	scale := mapTypographyScale(src.Width, src.Height)
	margin := modernMapMarginPX * scale
	width := math.Min(float64(src.Width)*0.6, 1100*scale)
	height := modernMapLabelFontPX * scale * 1.35
	boxX := margin
	boxY := float64(src.Height) - margin - height
	labelText := strings.ToUpper(feature.Name)
	// Long names ("UNITED STATES OF AMERICA") must fit without the shrink
	// floor; an oversized glyph atlas also exhausts the text upload pass.
	fontSize := math.Min(modernMapLabelFontPX*scale, width/float64(len(labelText))*0.58/0.62)
	fontSize = math.Max(fontSize, modernMapLabelFontPX*scale*0.42)
	spec := resolvedTextSpec{
		Text: labelText, Size: []float64{width, height}, OmitBox: true,
		Position:    canvasBoxPosition("text", boxX, boxY, width, height, src.Width, src.Height),
		CanvasWidth: src.Width, CanvasHeight: src.Height,
		Style:       &LayerStyle{Font: OfficialFontPathForLanguage(src.Language), TextAlign: "left"},
		Alignment:   "left",
		StyleParams: ri.Params,
		// Map label role: shrink-only fit plus a soft legibility shadow tuned
		// for paper (not the footage default).
		MotionTarget: TargetText, StylePolicy: textRoleMapLabel,
		FontSize: fontSize, MinFontSize: fontSize * 0.8, MaxFontSize: fontSize,
		OverrideFill:  modernMapInk,
		RoleOverrides: &LayerStyle{Shadow: &LayerShadow{Color: "#000000", Opacity: 0.25, Blur: 6, Offset: []float64{0, 2}}},
	}
	layer, err := compileResolvedText(ri.Item.ID+":modernmap1:label", ri.Start, ri.Start+duration, spec)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: ModernMap1 item %q label: %w", ri.Item.ID, err)
	}
	return layer, nil
}

// modernMapCreditLayer keeps the Natural Earth credit on the plate, in muted
// ink, bottom-right.
func modernMapCreditLayer(ri resolvedItem, src *semanticPlan, duration int64) (Layer, error) {
	scale := mapTypographyScale(src.Width, src.Height)
	fontSize := modernMapCreditFontPX * scale
	width, height := 460*scale, fontSize*1.6
	margin := modernMapMarginPX * scale
	boxX := float64(src.Width) - margin - width
	boxY := float64(src.Height) - margin - height
	credit := strings.TrimSpace(ri.Item.Map.Attribution)
	if credit == "" {
		credit = "Natural Earth"
	}
	spec := resolvedTextSpec{
		Text: credit, Size: []float64{width, height}, OmitBox: true,
		Position:    canvasBoxPosition("text", boxX, boxY, width, height, src.Width, src.Height),
		CanvasWidth: src.Width, CanvasHeight: src.Height,
		Style:        &LayerStyle{Font: OfficialFontPathForLanguage(src.Language), TextAlign: "right"},
		Alignment:    "right",
		StyleParams:  ri.Params,
		MotionTarget: TargetText, StylePolicy: textRoleMapAttribution,
		FontSize: fontSize, MinFontSize: fontSize * 0.7, MaxFontSize: fontSize,
		OverrideFill:  modernMapCreditInk,
		RoleOverrides: &LayerStyle{Shadow: &LayerShadow{Color: "#000000", Opacity: 0, Blur: 0, Offset: []float64{0, 0}}},
	}
	layer, err := compileResolvedText(ri.Item.ID+":modernmap1:credit", ri.Start, ri.Start+duration, spec)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: ModernMap1 item %q credit: %w", ri.Item.ID, err)
	}
	return layer, nil
}

// modernMapLogoLayer centres the emblem inside the largest territory and scales
// it relative to that territory, kept small enough to read as a badge instead
// of a flag pasted over the map. It sits above the fill.
func modernMapLogoLayer(ri resolvedItem, registry *assetRegistry, assetID string, ring [][2]float64, duration int64, canvasWidth, canvasHeight int) Layer {
	minX, minY, maxX, maxY := modernMapRingBounds(ring)
	size := math.Min(maxX-minX, maxY-minY) * 0.18
	size = math.Max(modernMapLogoMinSizePX, math.Min(modernMapLogoMaxSizePX, size))
	opacity := modernMapLogoOpacity
	return Layer{
		ID:             ri.Item.ID + ":modernmap1:logo",
		Type:           "image",
		Asset:          registry.Path(assetID),
		BoxWidth:       int(size),
		BoxHeight:      int(size),
		Size:           []float64{size, size},
		Fit:            FitContain,
		Position:       []float64{(minX+maxX)/2 - float64(canvasWidth)/2, (minY+maxY)/2 - float64(canvasHeight)/2},
		StartFrame:     ri.Start,
		DurationFrames: duration,
		Opacity:        &opacity,
	}
}
