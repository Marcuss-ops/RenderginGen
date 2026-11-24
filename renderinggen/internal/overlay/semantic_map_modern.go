// semantic_map_modern.go owns the second map presentation family, ModernMap1:
// an editorial vector plate that shows the requested territory inside its
// neighbouring geography. It follows the same paper-map language as the
// ChrononPlugin `chronon_editorial` basemap — warm paper sea, hairline borders,
// a light graticule, one highlighted territory, its name and a credit — instead
// of a lone silhouette on a dark canvas.
//
// Geometry constraints are the renderer's, not a style choice:
//   - a fill path with a dense ring is dropped by the native tessellator, so
//     the territory ring is simplified to the point budget below and drawn as
//     ONE even-odd subpath (no per-triangle surfaces);
//   - the same ring drives the outline and halo, so the accent cannot drift
//     off its own border;
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
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/tacticalfill"
)

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
	mapMotion, err := modernMapOverlayMotion(ri, duration)
	if err != nil {
		return nil, err
	}
	placed, err := placeModernMapPins(ri, src, fit, mapMotion, duration)
	if err != nil {
		return nil, err
	}
	out := make([]Layer, 0, 2*len(placed)+len(m.Routes)+2)
	pins, err := modernMapPinLayers(ri, src, placed, duration)
	if err != nil {
		return nil, err
	}
	out = append(out, pins...)
	routes, err := modernMapRouteLayers(ri, src, fit, mapMotion, duration)
	if err != nil {
		return nil, err
	}
	out = append(out, routes...)
	if _, present := ri.Params["map_fx"]; present {
		fx, err := modernMapFxLayers(ri, src, placed, duration)
		if err != nil {
			return nil, err
		}
		out = append(out, fx...)
	}
	return out, nil
}

func modernMapOverlayMotion(ri resolvedItem, duration int64) (*LayerAnimation, error) {
	m := ri.Item.Map
	if strings.TrimSpace(m.MotionID) == "" {
		return nil, nil
	}
	animation, err := imageMotionAnimation(m.MotionID, nil, duration, 0, "map", TargetMapView)
	if err != nil {
		return nil, fmt.Errorf("overlay: ModernMap1 item %q map motion: %w", ri.Item.ID, err)
	}
	return animation, nil
}

type placedModernMapPin struct {
	pin     SemanticMapPin
	x, y    float64
	topLeft [2]float64
	width   float64
	height  float64
	motion  *LayerAnimation
}

func placeModernMapPins(ri resolvedItem, src *semanticPlan, fit tacticalfill.Fit, mapMotion *LayerAnimation, duration int64) ([]placedModernMapPin, error) {
	m := ri.Item.Map
	placed := make([]placedModernMapPin, 0, len(m.Pins))
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
		placed = append(placed, placedModernMapPin{pin: pin, x: point[0], y: point[1], topLeft: topLeft, width: width, height: height, motion: pinMotion})
	}
	return placed, nil
}

func modernMapPinLayers(ri resolvedItem, src *semanticPlan, placed []placedModernMapPin, duration int64) ([]Layer, error) {
	out := make([]Layer, 0, 2*len(placed))
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
		}
		out = append(out, marker, label)
	}
	return out, nil
}

func modernMapRouteLayers(ri resolvedItem, src *semanticPlan, fit tacticalfill.Fit, mapMotion *LayerAnimation, duration int64) ([]Layer, error) {
	m := ri.Item.Map
	out := make([]Layer, 0, len(m.Routes))
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
		if !finite(trimStart) || !finite(trimEnd) || trimStart < 0 || trimEnd > 1 || trimEnd <= trimStart {
			return nil, fmt.Errorf("overlay: ModernMap1 item %q route %q trim range must satisfy 0 <= trim_start < trim_end <= 1", ri.Item.ID, route.ID)
		}
		commands := make([]LayerPathCommand, 0, len(projectedPoints))
		for pointIndex, point := range projectedPoints {
			commandType := "line_to"
			if pointIndex == 0 {
				commandType = "move_to"
			}
			commands = append(commands, LayerPathCommand{Type: commandType, Point: []float64{point[0], point[1]}})
		}
		stroke := &LayerStroke{Color: route.Color, Width: route.WidthPX}
		trim := LayerPathOperator{Kind: "trim", Params: LayerTrimParams{Start: trimStart, End: trimEnd}}
		routeLayer := Layer{
			ID: ri.Item.ID + ":modernmap1:route:" + route.ID, Type: "shape",
			Position:   []float64{-float64(src.Width) / 2, -float64(src.Height) / 2},
			Size:       []float64{float64(src.Width), float64(src.Height)},
			StartFrame: ri.Start, DurationFrames: duration,
			Shape: &LayerShape{Type: "path", Path: commands, Operators: []LayerPathOperator{trim}, Stroke: stroke},
		}
		if mapMotion != nil {
			applyMotionRouting(&routeLayer, mapMotion)
		}
		out = append(out, routeLayer)
	}
	return out, nil
}

func modernMapFxLayers(ri resolvedItem, src *semanticPlan, placed []placedModernMapPin, duration int64) ([]Layer, error) {
	resolvedPins := make([]resolvedMapPin, 0, len(placed))
	for _, entry := range placed {
		resolvedPins = append(resolvedPins, resolvedMapPin{
			Contract: entry.pin, RasterX: entry.x, RasterY: entry.y,
			LabelTopLeft: entry.topLeft, LabelWidth: entry.width, LabelHeight: entry.height,
			Motion: entry.motion,
		})
	}
	return compileMapFxLayers(ri, src, &resolvedMap{Pins: resolvedPins}, duration)
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
