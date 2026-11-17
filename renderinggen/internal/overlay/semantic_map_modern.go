// semantic_map_modern.go owns the second map presentation family, ModernMap1:
// an editorial vector plate that shows the requested territory inside its
// neighbouring geography. It follows the same paper-map language as the
// ChrononPlugin `chronon_editorial` basemap — warm paper sea, hairline borders,
// a light graticule, one highlighted territory, its name and a credit — instead
// of a lone silhouette on a dark canvas.
//
// Geometry constraints are the renderer's, not a style choice:
//   - a fill path with a dense ring is dropped by the native tessellator, so
//     the territory is ear-clipped into triangles anchored to the region bbox
//     (per-triangle surfaces are culled);
//   - a stroked path can carry the whole ring, so the territory keeps a smooth
//     outline and halo that hide the triangle seams;
//   - every path is authored in its own bbox ([0,size]) with a canvas position,
//     because canvas-sized coordinates push the path tessellator onto the slow
//     software fill.
//
// The geographic decision is resolved locally from the Natural Earth catalog
// through tacticalfill. No network fetch and no per-pixel work.
//
// An optional logo asset may be placed inside the territory. It is a declared,
// content-addressed job asset like every other image: RenderingGen never
// downloads one, so "Wikipedia when available" is a producer decision, and a
// missing logo simply means no logo layer.
package overlay

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/tacticalfill"
)

const (
	// MapTypeClassic is the default raster map (operator-supplied PNG plate).
	MapTypeClassic = "classic"
	// MapTypeModernMap1 is the vector territory map.
	MapTypeModernMap1 = "ModernMap1"

	modernMapMaxPlaceBytes = 200
	// The territory is budgeted in triangles because the renderer draws each
	// one as its own layer. Halo, outline and fill all consume the same ring,
	// so the accent cannot drift off its own border.
	modernMapTerritoryTriangleBudget = 210
	modernMapMinRegionTriangles      = 8
	modernMapContextRingPoints       = 300
	modernMapMaxContextLayers        = 72
	modernMapMaxBorderPoints         = 2200
	modernMapMaxBorderPointsPerRow   = 900
	modernMapLogoMinSizePX           = 96.0
	modernMapLogoMaxSizePX           = 200.0
	modernMapLabelFontPX             = 88.0
	modernMapCreditFontPX            = 20.0
	modernMapMarginPX                = 84.0
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

// compileModernMap1Layers draws the editorial plate in back-to-front order:
// paper, graticule, neighbouring borders, territory halo, territory fill and
// outline, name, credit, and finally the optional logo.
func compileModernMap1Layers(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	place := modernMapPlace(ri.Item)
	if place == "" {
		return nil, fmt.Errorf("overlay: ModernMap1 item %q requires params.map_place", ri.Item.ID)
	}
	features, err := geofeatures.DefaultRegistry()
	if err != nil {
		return nil, fmt.Errorf("overlay: ModernMap1 item %q: %w", ri.Item.ID, err)
	}
	sample, feature, fit, err := tacticalfill.BuildWithFit(features, place, tacticalfill.Options{
		Width: src.Width, Height: src.Height, Padding: 0.02,
		Box: tacticalfill.Box{MinX: 0.06, MinY: 0.06, MaxX: 0.94, MaxY: 0.94},
	})
	if err != nil {
		return nil, fmt.Errorf("overlay: ModernMap1 item %q: %w", ri.Item.ID, err)
	}
	duration := ri.End - ri.Start
	layers := make([]Layer, 0, 16+len(sample.Regions))
	layers = append(layers, modernMapPaperLayer(ri, src, duration))
	if grid := modernMapGraticuleLayer(ri, src, fit, duration); grid != nil {
		layers = append(layers, *grid)
	}
	borderLayers := modernMapContextBorderLayers(ri, src, features, feature, fit, duration)
	layers = append(layers, borderLayers...)
	// One ring per part drives the halo, the outline and the fill: the red can
	// only end exactly on the border that ring strokes.
	rings := modernMapTerritoryRings(sample.Regions)
	if len(rings) == 0 {
		return nil, fmt.Errorf("overlay: ModernMap1 item %q resolved no drawable territory for %q", ri.Item.ID, place)
	}
	// The halo is a stacked wide lift plus a tighter core glow: depth without
	// a raster blur pass.
	layers = append(layers, modernMapTerritoryHaloLayers(ri, src, rings, duration)...)
	// One fill owns the whole plate: every territory ring — mainland, islands,
	// or an island cut-out — is a subpath of a single closed path, drawn by one
	// even-odd dispatch with analytic anti-aliased edges. The previous plan
	// ear-clipped each ring into dozens of triangles and folded them with
	// Hertel-Mehlhorn, which cost one layer (and one composite pass) per piece
	// and still left visible interior seams and popping edges.
	if fill, ok := modernMapShapeLayer(
		ri.Item.ID+":modernmap1:territory", ri, src, duration, rings,
		modernMapTerritory, nil, nil,
	); ok {
		layers = append(layers, fill)
	} else {
		return nil, fmt.Errorf("overlay: ModernMap1 item %q resolved no fillable territory for %q", ri.Item.ID, place)
	}
	// The reveal is one animated paper cover whose soft leading edge sweeps
	// west to east over the static fill. Animating a single layer keeps the
	// raster tail cheap: the fill stays cacheable and the territory floods in
	// as a wave instead of a per-triangle fade.
	if cover := modernMapFillRevealLayer(ri, src, rings, duration); cover != nil {
		layers = append(layers, *cover)
	}
	// Outlines sit above the fill so the coast keeps a crisp inner border.
	for index, ring := range rings {
		outerOpacity := 0.28
		if outer, ok := modernMapShapeLayer(
			fmt.Sprintf("%s:modernmap1:outline:outer:%d", ri.Item.ID, index), ri, src, duration,
			[][][2]float64{ring}, nil,
			&LayerStroke{Color: modernMapTerritoryLine, Width: 6}, &outerOpacity,
		); ok {
			layers = append(layers, outer)
		}
		if outline, ok := modernMapShapeLayer(
			fmt.Sprintf("%s:modernmap1:outline:%d", ri.Item.ID, index), ri, src, duration,
			[][][2]float64{ring}, nil,
			&LayerStroke{Color: modernMapTerritoryLine, Width: 2.5}, nil,
		); ok {
			layers = append(layers, outline)
		}
	}
	largest := rings[0]
	label, err := modernMapLabelLayer(ri, src, feature, duration)
	if err != nil {
		return nil, err
	}
	layers = append(layers, label)
	credit, err := modernMapCreditLayer(ri, src, duration)
	if err != nil {
		return nil, err
	}
	layers = append(layers, credit)
	if logo := modernMapLogo(ri.Item); logo != "" {
		layers = append(layers, modernMapLogoLayer(ri, registry, logo, largest, duration, src.Width, src.Height))
	}
	// Unified overlays: grounded pins, static routes and the centered map_view
	// motion ride on top of the vector plate through the same builders as the
	// raster lane (same pin dots, same collision-free labels, same trim-path
	// routes). Coordinates come from the vector fit, not the Mercator window.
	vectorOverlays, err := modernMapOverlayLayers(ri, src, fit, duration)
	if err != nil {
		return nil, err
	}
	layers = append(layers, vectorOverlays...)
	return layers, nil
}

// modernMapOverlayLayers lowers pins, static routes, map_fx and the optional
// centered map_view motion on top of the vector plate.
func modernMapOverlayLayers(ri resolvedItem, src *semanticPlan, fit tacticalfill.Fit, duration int64) ([]Layer, error) {
	m := ri.Item.Map
	if m == nil {
		return nil, nil
	}
	if len(m.Pins) == 0 && len(m.Routes) == 0 && strings.TrimSpace(m.MotionID) == "" {
		if _, present := ri.Params["map_fx"]; !present {
			return nil, nil
		}
	}
	var mapMotion *LayerAnimation
	if strings.TrimSpace(m.MotionID) != "" {
		animation, err := imageMotionAnimation(m.MotionID, nil, duration, 0, "map", TargetMapView)
		if err != nil {
			return nil, fmt.Errorf("overlay: ModernMap1 item %q map motion: %w", ri.Item.ID, err)
		}
		mapMotion = animation
	}
	out := make([]Layer, 0, 2*len(m.Pins)+len(m.Routes)+2)
	// Resolve pins through the vector fit so dots sit exactly on the drawn
	// territory, then place labels collision-free in canvas space.
	type placedPin struct {
		pin     SemanticMapPin
		x, y    float64
		topLeft [2]float64
		width   float64
		height  float64
		motion  *LayerAnimation
	}
	placed := make([]placedPin, 0, len(m.Pins))
	occupied := make([][4]float64, 0, len(m.Pins))
	for _, pin := range m.Pins {
		point := fit.Project(pin.Longitude, pin.Latitude)
		if point[0] < -40 || point[0] > float64(src.Width)+40 || point[1] < -40 || point[1] > float64(src.Height)+40 {
			return nil, fmt.Errorf("overlay: ModernMap1 item %q pin %q lies outside the vector viewport", ri.Item.ID, pin.ID)
		}
		width, height := mapPinLabelDimensions(pin, src.Width, src.Height)
		candidates := mapPinLabelCandidates(pin, point[0], point[1], width, height)
		var topLeft [2]float64
		found := false
		for _, candidate := range candidates {
			left := clampMapBox(candidate[0], float64(src.Width)-width)
			top := clampMapBox(candidate[1], float64(src.Height)-height)
			if math.Abs(left-candidate[0]) >= 0.001 || math.Abs(top-candidate[1]) >= 0.001 {
				continue
			}
			rect := [4]float64{left, top, left + width, top + height}
			collision := false
			for _, previous := range occupied {
				if mapLabelRectsOverlap(rect, previous) {
					collision = true
					break
				}
			}
			if !collision {
				occupied = append(occupied, rect)
				topLeft = [2]float64{left, top}
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("overlay: ModernMap1 item %q pin %q label cannot be placed without clipping or colliding", ri.Item.ID, pin.ID)
		}
		var pinMotion *LayerAnimation
		if mapMotion != nil {
			pinMotion = mapMotion
		} else if strings.TrimSpace(m.MotionID) != "" {
			animation, err := imageMotionAnimation(m.MotionID, nil, duration, 0, pin.ID, TargetMapView)
			if err != nil {
				return nil, fmt.Errorf("overlay: ModernMap1 item %q pin %q motion: %w", ri.Item.ID, pin.ID, err)
			}
			pinMotion = animation
		}
		placed = append(placed, placedPin{pin: pin, x: point[0], y: point[1], topLeft: topLeft, width: width, height: height, motion: pinMotion})
	}
	for _, entry := range placed {
		marker, err := mapPinLayer(ri, src, entry.pin, entry.x, entry.y, duration)
		if err != nil {
			return nil, err
		}
		label, err := resolveMapPinLabel(ri, src, resolvedMapPin{
			Contract: entry.pin, RasterX: entry.x, RasterY: entry.y,
			LabelTopLeft: entry.topLeft, LabelWidth: entry.width, LabelHeight: entry.height,
			Motion: entry.motion,
		}, duration)
		if err != nil {
			return nil, err
		}
		timing, err := resolvePinTiming(ri.Params, entry.pin.ID, int64(src.FPSNum), int64(src.FPSDen), duration)
		if err != nil {
			return nil, fmt.Errorf("overlay: ModernMap1 item %q pin %q: %w", ri.Item.ID, entry.pin.ID, err)
		}
		applyPinTiming(&marker, &label, timing)
		if entry.motion != nil {
			applyMotionRouting(&marker, mapPinMotion(entry.motion, marker.Position))
			applyMotionRouting(&label, mapPinMotion(entry.motion, label.Position))
		} else if mapMotion != nil {
			applyMotionRouting(&marker, mapPinMotion(mapMotion, marker.Position))
			applyMotionRouting(&label, mapPinMotion(mapMotion, label.Position))
		}
		out = append(out, marker, label)
	}
	for _, route := range m.Routes {
		samples, err := mapRouteGeoSamples(route.Stops)
		if err != nil {
			return nil, fmt.Errorf("overlay: ModernMap1 item %q route %q: %w", ri.Item.ID, route.ID, err)
		}
		projectedPoints := make([][2]float64, 0, len(samples))
		for _, sample := range samples {
			point := fit.Project(sample.Longitude, sample.Latitude)
			projectedPoints = append(projectedPoints, [2]float64{point[0], point[1]})
		}
		trimStart, trimEnd := 0.0, 1.0
		if route.TrimStart != nil {
			trimStart = *route.TrimStart
		}
		if route.TrimEnd != nil {
			trimEnd = *route.TrimEnd
		}
		// The contract trim is a static range, and the native Vulkan shape path
		// ignores an operator-level trim animation (it draws the seeded range,
		// i.e. nothing). Clip the projected polyline itself instead: the route
		// then draws natively and still honours [trim_start, trim_end].
		projectedPoints = trimMapPolyline(projectedPoints, trimStart, trimEnd)
		commands := make([]LayerPathCommand, 0, len(projectedPoints))
		for pointIndex, point := range projectedPoints {
			commandType := "line_to"
			if pointIndex == 0 {
				commandType = "move_to"
			}
			commands = append(commands, LayerPathCommand{Type: commandType, Point: []float64{point[0], point[1]}})
		}
		stroke := &LayerStroke{Color: route.Color, Width: route.WidthPX}
		routeLayer := Layer{
			ID: ri.Item.ID + ":modernmap1:route:" + route.ID, Type: "shape",
			// Path points are authored in the layer's own [0,size] space and the
			// position is the box centre relative to the canvas centre (the same
			// convention Grid and the graticule use). A full-canvas path therefore
			// sits at -size/2, and carries no depth: an explicit position_z
			// without enable_3d is rejected by the render-plan decoder.
			Position:   []float64{-float64(src.Width) / 2, -float64(src.Height) / 2},
			Size:       []float64{float64(src.Width), float64(src.Height)},
			StartFrame: ri.Start, DurationFrames: duration,
			Shape: &LayerShape{Type: "path", Path: commands, Stroke: stroke},
		}
		if mapMotion != nil {
			applyMotionRouting(&routeLayer, mapMotion)
		}
		out = append(out, routeLayer)
	}
	if _, present := ri.Params["map_fx"]; present {
		resolvedPins := make([]resolvedMapPin, 0, len(placed))
		for _, entry := range placed {
			resolvedPins = append(resolvedPins, resolvedMapPin{
				Contract: entry.pin, RasterX: entry.x, RasterY: entry.y,
				LabelTopLeft: entry.topLeft, LabelWidth: entry.width, LabelHeight: entry.height,
				Motion: entry.motion,
			})
		}
		fxLayers, err := compileMapFxLayers(ri, src, &resolvedMap{Pins: resolvedPins}, duration)
		if err != nil {
			return nil, err
		}
		out = append(out, fxLayers...)
	}
	return out, nil
}

// modernMapFillRevealLayer returns the paper cover that wipes the territory
// fill in from west to east. It is a single rect — paper-coloured, with a soft
// transparent leading edge — whose left edge sweeps across the plate: at frame
// zero the accent is fully hidden, at the end of the wipe it is gone. One
// animated layer replaces the old per-triangle opacity stagger, so the fill
// itself stays static and cacheable.
func modernMapFillRevealLayer(ri resolvedItem, src *semanticPlan, rings [][][2]float64, duration int64) *Layer {
	if duration <= 2 || len(rings) == 0 {
		return nil
	}
	minX, minY, maxX, maxY := modernMapRingsBounds(rings)
	if math.IsInf(minX, 1) || math.IsInf(maxX, -1) {
		return nil
	}
	canvasWidth := float64(src.Width)
	canvasHeight := float64(src.Height)
	// The cover is wider than the canvas plus the plate, so it spans the whole
	// territory before the sweep and clears it afterwards.
	plateWidth := maxX - minX
	width := canvasWidth + plateWidth + modernMapFillWipeFeatherPX*2
	height := (maxY - minY) + modernMapFillWipeFeatherPX*2
	// A linear gradient along +x: transparent at the sheet's west edge, opaque
	// paper a feather-width later. Blending paper over the accent fill turns the
	// hard sweep into a soft flood front.
	featherStop := modernMapFillWipeFeatherPX / width
	gradient := LayerGradient{
		Type: "linear",
		ColorStops: []LayerGradientStop{
			{Position: 0, Color: []float64{modernMapPaper[0], modernMapPaper[1], modernMapPaper[2], 0}},
			{Position: featherStop, Color: modernMapPaper},
			{Position: 1, Color: modernMapPaper},
		},
	}
	wipe := int64(modernMapFillWipeFrames)
	if limit := duration - 1; wipe > limit {
		wipe = limit
	}
	if wipe < 2 {
		return nil
	}
	// A rect layer's local space is centred on its box, so the left edge sits at
	// position + canvas/2 - width/2. The layer starts just west of the plate and
	// the sweep travels the plate width plus one feather on each side.
	leftEdge := func(x float64) float64 { return x - canvasWidth/2 + width/2 }
	centreY := (minY+maxY)/2 - canvasHeight/2
	sweep := plateWidth + modernMapFillWipeFeatherPX*2
	return &Layer{
		ID:         ri.Item.ID + ":modernmap1:fillwipe",
		Type:       "shape",
		Position:   []float64{leftEdge(minX - modernMapFillWipeFeatherPX), centreY},
		Size:       []float64{width, height},
		StartFrame: ri.Start,
		// The cover only exists while it sweeps: once the front has left the
		// plate it covers nothing, and keeping an animated layer alive would
		// stop the renderer from marking every later frame as a duplicate.
		DurationFrames: wipe + 1,
		Shape:          &LayerShape{Type: "rect", Fill: gradient},
		// A component position track is a delta from the layer position, not an
		// absolute coordinate: 0 keeps the cover where it was placed.
		Animation: &LayerAnimation{Tracks: []AnimationTrack{{
			Property: "position_x",
			Easing:   "in_out_cubic",
			Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: 0.0},
				{Frame: wipe, Value: sweep},
			},
		}}},
	}
}

// trimMapPolyline clips a canvas-space polyline to the [start, end] fraction of
// its arc length, interpolating the two cut points. An empty result means the
// requested range is degenerate.
func trimMapPolyline(points [][2]float64, start, end float64) [][2]float64 {
	if len(points) < 2 {
		return points
	}
	start = math.Max(0, math.Min(1, start))
	end = math.Max(0, math.Min(1, end))
	if end <= start {
		return nil
	}
	if start == 0 && end == 1 {
		return points
	}
	lengths := make([]float64, len(points)-1)
	total := 0.0
	for index := range lengths {
		lengths[index] = math.Hypot(points[index+1][0]-points[index][0], points[index+1][1]-points[index][1])
		total += lengths[index]
	}
	if total <= 0 {
		return nil
	}
	startDistance, endDistance := start*total, end*total
	out := make([][2]float64, 0, len(points)+2)
	travelled := 0.0
	for index, length := range lengths {
		segmentStart, segmentEnd := travelled, travelled+length
		travelled = segmentEnd
		if segmentEnd <= startDistance || segmentStart >= endDistance {
			continue
		}
		from, to := points[index], points[index+1]
		fromFraction, toFraction := 0.0, 1.0
		if length > 0 {
			fromFraction = math.Max(0, (startDistance-segmentStart)/length)
			toFraction = math.Min(1, (endDistance-segmentStart)/length)
		}
		cutFrom := mapPolylinePoint(from, to, fromFraction)
		cutTo := mapPolylinePoint(from, to, toFraction)
		if len(out) == 0 || out[len(out)-1] != cutFrom {
			out = append(out, cutFrom)
		}
		out = append(out, cutTo)
	}
	return out
}

func mapPolylinePoint(from, to [2]float64, fraction float64) [2]float64 {
	return [2]float64{
		from[0] + (to[0]-from[0])*fraction,
		from[1] + (to[1]-from[1])*fraction,
	}
}

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
	// Triangles grow with coastline length, so a part's share follows the
	// square root of its area: Sardinia earns a third of Italy, not a twelfth.
	weights := make([]float64, len(kept))
	var totalWeight float64
	for index, candidate := range kept {
		weights[index] = math.Sqrt(candidate.area / kept[0].area)
		totalWeight += weights[index]
	}
	triangles := make([]int, len(kept))
	totalTriangles := 0
	for index := range kept {
		triangles[index] = int(math.Round(float64(modernMapTerritoryTriangleBudget) * weights[index] / totalWeight))
		if triangles[index] < modernMapMinRegionTriangles {
			triangles[index] = modernMapMinRegionTriangles
		}
		totalTriangles += triangles[index]
	}
	for totalTriangles > modernMapTerritoryTriangleBudget {
		largest := 0
		for index := range triangles {
			if triangles[index] > triangles[largest] {
				largest = index
			}
		}
		if triangles[largest] <= modernMapMinRegionTriangles {
			break
		}
		triangles[largest]--
		totalTriangles--
	}
	rings := make([][][2]float64, 0, len(kept))
	for index, candidate := range kept {
		points := simplifyModernMapRing(candidate.points, triangles[index]+2)
		if len(points) >= 3 {
			rings = append(rings, points)
		}
	}
	return rings
}

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
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, ring := range rings {
		for _, point := range ring {
			minX = math.Min(minX, point[0])
			minY = math.Min(minY, point[1])
			maxX = math.Max(maxX, point[0])
			maxY = math.Max(maxY, point[1])
		}
	}
	return minX, minY, maxX, maxY
}

func modernMapRingArea(points [][2]float64) float64 {
	var twice float64
	for index, point := range points {
		next := points[(index+1)%len(points)]
		twice += point[0]*next[1] - next[0]*point[1]
	}
	return math.Abs(twice) / 2
}

func modernMapRingBounds(points [][2]float64) (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, point := range points {
		minX = math.Min(minX, point[0])
		minY = math.Min(minY, point[1])
		maxX = math.Max(maxX, point[0])
		maxY = math.Max(maxY, point[1])
	}
	return minX, minY, maxX, maxY
}

// modernMapCleanRing drops repeated vertices (including a duplicated closing
// point) so no later step has to divide by a zero-length edge.
func modernMapCleanRing(points [][2]float64) [][2]float64 {
	ring := make([][2]float64, 0, len(points))
	for _, point := range points {
		if len(ring) > 0 && modernMapDistanceSquared(ring[len(ring)-1], point) <= 1e-6 {
			continue
		}
		ring = append(ring, point)
	}
	if len(ring) > 1 && modernMapDistanceSquared(ring[0], ring[len(ring)-1]) <= 1e-6 {
		ring = ring[:len(ring)-1]
	}
	return ring
}

// simplifyModernMapRing decimates a closed ring with Ramer–Douglas–Peucker.
// Uniform index sampling (the previous approach) spent points evenly on long
// straight borders and cut through bays; RDP keeps the capes and skips collinear
// coast instead.
func simplifyModernMapRing(points [][2]float64, limit int) [][2]float64 {
	ring := modernMapCleanRing(points)
	if len(ring) <= limit || limit < 4 {
		return ring
	}
	// Two far-apart anchors let RDP run on open polylines while the ring stays
	// closed: the second half ends back at ring[0], which is dropped again.
	far, farthest := 0, -1.0
	for index, point := range ring {
		if distance := modernMapDistanceSquared(ring[0], point); distance > farthest {
			far, farthest = index, distance
		}
	}
	if far == 0 {
		far = len(ring) / 2
	}
	closed := make([][2]float64, 0, len(ring)-far+1)
	closed = append(closed, ring[far:]...)
	closed = append(closed, ring[0])
	minX, minY, maxX, maxY := modernMapRingBounds(ring)
	low, high := 0.0, math.Hypot(maxX-minX, maxY-minY)
	if high <= 0 {
		return ring
	}
	best := ring
	for try := 0; try < 20; try++ {
		tolerance := (low + high) / 2
		simplified := simplifyModernMapPolyline(ring[:far+1], tolerance)
		tail := simplifyModernMapPolyline(closed, tolerance)
		simplified = append(simplified, tail[1:len(tail)-1]...)
		if len(simplified) > limit {
			low = tolerance
			continue
		}
		best = simplified
		high = tolerance
	}
	return best
}

func simplifyModernMapPolyline(points [][2]float64, tolerance float64) [][2]float64 {
	if len(points) < 3 {
		return append([][2]float64(nil), points...)
	}
	keep := make([]bool, len(points))
	keep[0], keep[len(points)-1] = true, true
	stack := [][2]int{{0, len(points) - 1}}
	threshold := tolerance * tolerance
	for len(stack) > 0 {
		segment := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		farthest, farthestIndex := -1.0, -1
		for index := segment[0] + 1; index < segment[1]; index++ {
			if distance := modernMapPointSegmentDistanceSquared(points[index], points[segment[0]], points[segment[1]]); distance > farthest {
				farthest, farthestIndex = distance, index
			}
		}
		if farthestIndex >= 0 && farthest > threshold {
			keep[farthestIndex] = true
			stack = append(stack, [2]int{segment[0], farthestIndex}, [2]int{farthestIndex, segment[1]})
		}
	}
	out := make([][2]float64, 0, len(points))
	for index, point := range points {
		if keep[index] {
			out = append(out, point)
		}
	}
	return out
}

func modernMapPointSegmentDistanceSquared(point, a, b [2]float64) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	length := dx*dx + dy*dy
	if length <= 0 {
		return modernMapDistanceSquared(point, a)
	}
	t := ((point[0]-a[0])*dx + (point[1]-a[1])*dy) / length
	t = math.Max(0, math.Min(1, t))
	px, py := a[0]+t*dx, a[1]+t*dy
	return (point[0]-px)*(point[0]-px) + (point[1]-py)*(point[1]-py)
}

func modernMapDistanceSquared(a, b [2]float64) float64 {
	dx, dy := a[0]-b[0], a[1]-b[1]
	return dx*dx + dy*dy
}

// decimateModernMapRing keeps the silhouette while bounding the command count a
// single shape can contribute. Raster Natural Earth rings easily exceed what the
// renderer should flatten per frame; the first and last point are preserved so
// the ring stays closed.
func decimateModernMapRing(points [][2]float64, limit int) [][2]float64 {
	if len(points) <= limit {
		return points
	}
	if limit < 4 {
		limit = 4
	}
	out := make([][2]float64, 0, limit)
	step := float64(len(points)-1) / float64(limit-1)
	for index := 0; index < limit; index++ {
		position := int(float64(index) * step)
		if position >= len(points) {
			position = len(points) - 1
		}
		out = append(out, points[position])
	}
	out[0] = points[0]
	out[len(out)-1] = points[len(points)-1]
	return out
}
