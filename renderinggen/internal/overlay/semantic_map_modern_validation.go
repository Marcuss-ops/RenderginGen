package overlay

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/mapgeom"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/tacticalfill"
)

// The ModernMap1 family's declared constants and palettes plus the
// request-side validation: which map_type and place are accepted, and which
// pins/routes/regions a plate may carry. Layer construction and geometry
// helpers live further down in this file.

const (
	// MapTypeClassic is the default raster map (operator-supplied PNG plate).
	MapTypeClassic = "classic"
	// MapTypeModernMap1 is the vector territory map.
	MapTypeModernMap1 = "ModernMap1"

	modernMapMaxPlaceBytes = 200
	// The territory ring is simplified to a point budget: one even-odd fill
	// path replaces the former per-triangle layers, while halo, outline and
	// fill all consume the same ring so the accent cannot drift off its own
	// border.
	modernMapTerritoryPointBudget  = 210
	modernMapMinRegionPoints       = 8
	modernMapContextRingPoints     = 300
	modernMapMaxContextLayers      = 72
	modernMapMaxBorderPoints       = 2200
	modernMapMaxBorderPointsPerRow = 900
	modernMapLogoMinSizePX         = 96.0
	modernMapLogoMaxSizePX         = 200.0
	modernMapLabelFontPX           = 88.0
	modernMapCreditFontPX          = 20.0
	modernMapMarginPX              = 84.0
)

// ModernMap1 presentation constants. They are worker-owned: the payload chooses
// the place and the logo, never the drawing palette.
var (
	// Paper sea and the highlighted territory: the ChrononPlugin editorial
	// plate reads as paper, never as a neon overlay.
	modernMapPaper         = []float64{0.973, 0.965, 0.945, 1.0} // #F8F6F1
	modernMapTerritory     = []float64{0.851, 0.239, 0.204, 1.0} // #D93D34
	modernMapLogoOpacity   = 0.95
	modernMapTerritoryLine = "#8E241D"
	modernMapHaloLine      = "#D93D34"
	modernMapBorderLine    = "#C2BEB2"
	modernMapGridLine      = "#E6E2D6"
	modernMapInk           = "#1F252A"
	modernMapCreditInk     = "#6E6A60"
)

func modernMapType(item semanticItem) string {
	if item.Params == nil {
		return ""
	}
	raw, ok := item.Params["map_type"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}

func isModernMap1(item semanticItem) bool {
	return modernMapType(item) == MapTypeModernMap1
}

func modernMapPlace(item semanticItem) string {
	if item.Params == nil {
		return ""
	}
	raw, _ := item.Params["map_place"].(string)
	return strings.TrimSpace(raw)
}

func modernMapLogo(item semanticItem) string {
	if item.Params == nil {
		return ""
	}
	raw, _ := item.Params["map_logo"].(string)
	return strings.TrimSpace(raw)
}

// validateMapTypeParam is called before any family-specific rule so an unknown
// map_type is always reported as such, not as a missing raster. It also keeps
// map_place from silently decorating the classic family, where it would have no
// effect.
func validateMapTypeParam(item semanticItem) error {
	if item.Params == nil {
		return nil
	}
	raw, present := item.Params["map_type"]
	if present {
		mapType, ok := raw.(string)
		if !ok || (mapType != MapTypeClassic && mapType != MapTypeModernMap1) {
			return fmt.Errorf("overlay: map item %q declares unsupported map_type %v", item.ID, raw)
		}
	}
	if rawPlace, hasPlace := item.Params["map_place"]; hasPlace {
		place, ok := rawPlace.(string)
		if !ok || strings.TrimSpace(place) == "" || len(place) > modernMapMaxPlaceBytes || !isSingleLineVisibleText(place) {
			return fmt.Errorf("overlay: map item %q map_place must be non-blank single-line text of at most %d bytes", item.ID, modernMapMaxPlaceBytes)
		}
		if modernMapType(item) != MapTypeModernMap1 {
			return fmt.Errorf("overlay: map item %q declares map_place without map_type %s", item.ID, MapTypeModernMap1)
		}
	}
	return nil
}

// validateModernMap1Contract admits the vector contract. A ModernMap1 item
// carries provenance, a place and at most one logo asset, plus the unified
// overlay set: grounded pins, static routes projected through the vector fit,
// an optional centered map_view motion and the certified presentation
// passthrough (map_animation/map_camera_animation/map_backend/...). Raster
// camera moves and LODs stay raster-only: the vector fit owns the viewport.
func validateModernMap1Contract(item semanticItem, canvasWidth, canvasHeight int) error {
	m := item.Map
	if strings.ToLower(strings.TrimSpace(m.Provider)) != "local" {
		return fmt.Errorf("overlay: map item %q provider %q is not a locally supplied Natural Earth catalog", item.ID, m.Provider)
	}
	if strings.TrimSpace(m.SourceID) == "" || strings.TrimSpace(m.SourceLicense) == "" {
		return fmt.Errorf("overlay: map item %q requires source_id and source_license provenance", item.ID)
	}
	if err := validateMapAttribution(m.Attribution); err != nil {
		return fmt.Errorf("overlay: map item %q: %w", item.ID, err)
	}
	if m.Width != canvasWidth || m.Height != canvasHeight {
		return fmt.Errorf("overlay: ModernMap1 item %q viewport %dx%d must match the %dx%d canvas", item.ID, m.Width, m.Height, canvasWidth, canvasHeight)
	}
	if err := validateMapPoint(m.Center.Latitude, m.Center.Longitude); err != nil {
		return fmt.Errorf("overlay: map item %q center: %w", item.ID, err)
	}
	if m.Zoom < 0 || m.Zoom > maxMapZoom {
		return fmt.Errorf("overlay: map item %q zoom %d outside [0,%d]", item.ID, m.Zoom, maxMapZoom)
	}
	place := modernMapPlace(item)
	if place == "" {
		return fmt.Errorf("overlay: ModernMap1 item %q requires params.map_place", item.ID)
	}
	if len(place) > modernMapMaxPlaceBytes || !isSingleLineVisibleText(place) {
		return fmt.Errorf("overlay: ModernMap1 item %q map_place must be non-blank single-line text of at most %d bytes", item.ID, modernMapMaxPlaceBytes)
	}
	if m.CameraMove != nil || len(m.LODs) != 0 {
		return fmt.Errorf("overlay: ModernMap1 item %q cannot combine generated territory with camera LODs; the vector fit owns the viewport", item.ID)
	}
	if err := validateModernMap1PinsAndRoutes(item); err != nil {
		return err
	}
	if strings.TrimSpace(m.MotionID) != "" && !motionAdmitsTarget(m.MotionID, TargetMapView) {
		return fmt.Errorf("overlay: map item %q motion %q is not a registered centered image motion", item.ID, m.MotionID)
	}
	if err := validateMapPresentationParams(item); err != nil {
		return err
	}
	logo := modernMapLogo(item)
	if logo == "" {
		if len(item.Assets) != 0 {
			return fmt.Errorf("overlay: ModernMap1 item %q declares assets without params.map_logo", item.ID)
		}
		return nil
	}
	if len(item.Assets) != 1 || item.Assets[0].ID != logo || !validMapPNGAsset(item.Assets[0]) {
		return fmt.Errorf("overlay: ModernMap1 item %q map_logo %q must name its unique content-addressed local image/png asset", item.ID, logo)
	}
	return nil
}

// validateModernMap1PinsAndRoutes admits the unified overlay set on the vector
// plate: grounded pins validated like classic pins, and static routes with the
// same id/stop/color contract but projected through the vector fit (no
// camera_move/LOD window required).
func validateModernMap1PinsAndRoutes(item semanticItem) error {
	m := item.Map
	if len(m.Pins) > maxMapPins {
		return fmt.Errorf("overlay: map item %q declares %d pins; the contract allows at most %d", item.ID, len(m.Pins), maxMapPins)
	}
	seen := make(map[string]struct{}, len(m.Pins))
	for index, pin := range m.Pins {
		if err := validateMapPoint(pin.Latitude, pin.Longitude); err != nil {
			return fmt.Errorf("overlay: map item %q pin[%d]: %w", item.ID, index, err)
		}
		if pin.ID == "" || len(pin.ID) > maxMapPinIDBytes || !isSingleLineVisibleText(pin.ID) {
			return fmt.Errorf("overlay: map item %q pin[%d] id must be unique, non-empty single-line text of at most %d bytes", item.ID, index, maxMapPinIDBytes)
		}
		if _, duplicate := seen[pin.ID]; duplicate {
			return fmt.Errorf("overlay: map item %q pin[%d] duplicates id %q", item.ID, index, pin.ID)
		}
		seen[pin.ID] = struct{}{}
		if strings.TrimSpace(pin.Label) == "" || len(pin.Label) > maxMapPinLabelBytes || !isSingleLineVisibleText(pin.Label) {
			return fmt.Errorf("overlay: map item %q pin %q label must be non-empty single-line text of at most %d bytes", item.ID, pin.ID, maxMapPinLabelBytes)
		}
		if !isHexColor(pin.Color) {
			return fmt.Errorf("overlay: map item %q pin %q color must be #RRGGBB", item.ID, pin.ID)
		}
		if pin.RadiusPX <= 0 || pin.RadiusPX > 128 {
			return fmt.Errorf("overlay: map item %q pin %q radius_px must be in (0,128]", item.ID, pin.ID)
		}
	}
	if len(m.Routes) > maxMapRouteCount {
		return fmt.Errorf("overlay: map item %q declares %d routes; the contract allows at most %d", item.ID, len(m.Routes), maxMapRouteCount)
	}
	seenRoutes := make(map[string]bool, len(m.Routes))
	for routeIndex, route := range m.Routes {
		if route.ID == "" || len(route.ID) > 128 || !isSingleLineVisibleText(route.ID) || seenRoutes[route.ID] {
			return fmt.Errorf("overlay: map item %q route[%d] id must be unique, non-empty single-line text of at most 128 bytes", item.ID, routeIndex)
		}
		seenRoutes[route.ID] = true
		if len(route.Stops) < 2 || len(route.Stops) > maxMapRouteStops || !isHexColor(route.Color) || !finite(route.WidthPX) || route.WidthPX <= 0 || route.WidthPX > 64 {
			return fmt.Errorf("overlay: map item %q route[%d] requires 2..%d stops, #RRGGBB color, and width_px in (0,64]", item.ID, routeIndex, maxMapRouteStops)
		}
		for stopIndex, stop := range route.Stops {
			if err := validateMapPoint(stop.Latitude, stop.Longitude); err != nil {
				return fmt.Errorf("overlay: map item %q route[%d] stop[%d]: %w", item.ID, routeIndex, stopIndex, err)
			}
		}
	}
	return nil
}

// The pure ring and polyline math of the
// ModernMap1 plate: point budgets, simplification, bounds, area and the local
// path/bbox convention shared by fill, outline, halo and route. Nothing here
// touches a layer.

// modernMapTerritoryRings selects the country parts the plate can afford and
// sizes each silhouette by its share of the territory. Halo, outline and fill
// all consume these rings, because a second, coarser fill ring is exactly what
// made the accent cut inside its own border.
func modernMapTerritoryRings(regions []tacticalfill.Region) [][][2]float64 {
	type part struct {
		points [][2]float64
		area   float64
	}
	parts := make([]part, 0, len(regions))
	var totalArea float64
	for _, region := range regions {
		points := modernMapCleanRing(region.Points)
		if len(points) < 3 {
			continue
		}
		area := modernMapRingArea(points)
		if area <= 0 {
			continue
		}
		parts = append(parts, part{points: points, area: area})
		totalArea += area
	}
	if len(parts) == 0 {
		return nil
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].area > parts[j].area })
	kept := make([]part, 0, len(parts))
	kept = append(kept, parts[0])
	for _, candidate := range parts[1:] {
		// Specks below 0.4% of the country read as a dot of ink at plate
		// scale and would steal layers from the real coastline.
		if candidate.area/totalArea >= 0.004 {
			kept = append(kept, candidate)
		}
	}
	// Point budgets grow with coastline length, so a part's share follows the
	// square root of its area: Sardinia earns a third of Italy, not a twelfth.
	weights := make([]float64, len(kept))
	var totalWeight float64
	for index, candidate := range kept {
		weights[index] = math.Sqrt(candidate.area / kept[0].area)
		totalWeight += weights[index]
	}
	pointBudgets := make([]int, len(kept))
	totalPointBudget := 0
	for index := range kept {
		pointBudgets[index] = int(math.Round(float64(modernMapTerritoryPointBudget) * weights[index] / totalWeight))
		if pointBudgets[index] < modernMapMinRegionPoints {
			pointBudgets[index] = modernMapMinRegionPoints
		}
		totalPointBudget += pointBudgets[index]
	}
	for totalPointBudget > modernMapTerritoryPointBudget {
		largest := 0
		for index := range pointBudgets {
			if pointBudgets[index] > pointBudgets[largest] {
				largest = index
			}
		}
		if pointBudgets[largest] <= modernMapMinRegionPoints {
			break
		}
		pointBudgets[largest]--
		totalPointBudget--
	}
	rings := make([][][2]float64, 0, len(kept))
	for index, candidate := range kept {
		points := simplifyModernMapRing(candidate.points, pointBudgets[index]+2)
		if len(points) >= 3 {
			rings = append(rings, points)
		}
	}
	return rings
}

// modernMapPathGeometry turns canvas-space rings into a local path with a
// closed subpath per ring, plus the layer size and top-left position that place
// that bbox back on the canvas. The duplicated closing vertex is dropped:
// `close` owns it.
func modernMapPathGeometry(rings [][][2]float64, canvasWidth, canvasHeight int) ([]LayerPathCommand, []float64, []float64) {
	minX, minY, maxX, maxY := modernMapRingsBounds(rings)
	if math.IsInf(minX, 1) || maxX-minX < 1 || maxY-minY < 1 {
		return nil, nil, nil
	}
	commands := make([]LayerPathCommand, 0, 64)
	for _, ring := range rings {
		points := ring
		if len(points) > 1 && points[0] == points[len(points)-1] {
			points = points[:len(points)-1]
		}
		if len(points) < 3 {
			continue
		}
		commands = append(commands, LayerPathCommand{Type: "move_to", Point: []float64{points[0][0] - minX, points[0][1] - minY}})
		for _, point := range points[1:] {
			commands = append(commands, LayerPathCommand{Type: "line_to", Point: []float64{point[0] - minX, point[1] - minY}})
		}
		commands = append(commands, LayerPathCommand{Type: "close"})
	}
	if len(commands) == 0 {
		return nil, nil, nil
	}
	return commands, []float64{maxX - minX, maxY - minY}, []float64{
		minX - float64(canvasWidth)/2,
		minY - float64(canvasHeight)/2,
	}
}

func modernMapRingsBounds(rings [][][2]float64) (minX, minY, maxX, maxY float64) {
	bounds, ok := mapgeom.BoundsOfRings(rings)
	if !ok {
		return math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	}
	return bounds.MinX, bounds.MinY, bounds.MaxX, bounds.MaxY
}

func modernMapRingArea(points [][2]float64) float64 {
	return mapgeom.RingArea(points)
}

func modernMapRingBounds(points [][2]float64) (minX, minY, maxX, maxY float64) {
	return mapgeom.RingBounds(points)
}

func modernMapCleanRing(points [][2]float64) [][2]float64 {
	return mapgeom.CleanRing(points)
}

func simplifyModernMapRing(points [][2]float64, limit int) [][2]float64 {
	return mapgeom.SimplifyRing(points, limit)
}

func decimateModernMapRing(points [][2]float64, limit int) [][2]float64 {
	return mapgeom.DecimateRing(points, limit)
}

// The individual ModernMap1 layer builders: territory shape, paper,
// graticule, context borders, halo, label, credit and logo. The composition
// order and the accessors live in compileModernMap1Layers.

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
