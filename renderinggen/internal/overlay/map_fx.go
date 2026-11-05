package overlay

import (
	"fmt"
	"math"
)

// The minimal map FX lane. These are the same four ids authored by
// ChrononTemplate's MapFxPack and are deliberately NOT part of the
// ChrononPlugin GPU map catalog: each treatment lowers to ellipse, rectangle or
// trimmed path layers that the ordinary Chronon renderer already executes.
const (
	mapFxAccent      = "#38D9C5"
	mapFxSecondary   = "#F3C76A"
	mapFxRingWidthPX = 3.0
)

// compileMapFxLayers is the runtime projection of the authoring pack. It is
// static-map only: map_fx is an overlay treatment, not a second camera/map
// renderer, so validation rejects a camera_move map before this point.
func compileMapFxLayers(ri resolvedItem, src *semanticPlan, resolved *resolvedMap, duration int64) ([]Layer, error) {
	raw, present := ri.Params["map_fx"]
	if !present {
		return nil, nil
	}
	fx, ok := raw.(string)
	if !ok || fx == "" {
		return nil, fmt.Errorf("overlay: map item %q map_fx must be a non-empty string", ri.Item.ID)
	}
	if resolved.Camera != nil {
		return nil, fmt.Errorf("overlay: map item %q map_fx %q requires a static map", ri.Item.ID, fx)
	}
	if len(resolved.Pins) == 0 {
		return nil, fmt.Errorf("overlay: map item %q map_fx %q requires at least one pin", ri.Item.ID, fx)
	}

	accent, err := parseHexColor(mapFxAccent)
	if err != nil {
		return nil, fmt.Errorf("overlay: map fx accent: %w", err)
	}
	secondary, err := parseHexColor(mapFxSecondary)
	if err != nil {
		return nil, fmt.Errorf("overlay: map fx secondary: %w", err)
	}

	switch fx {
	case "area_glow_pulse":
		return mapFxAreaGlowPulse(ri, src, resolved.Pins[0], accent, duration), nil
	case "radar_location_reveal":
		return mapFxRadarLocationReveal(ri, src, resolved.Pins[0], accent, secondary, duration), nil
	case "light_beam_drop":
		return mapFxLightBeamDrop(ri, src, resolved.Pins[0], accent, secondary, duration), nil
	case "city_network_expansion":
		return mapFxCityNetworkExpansion(ri, src, resolved.Pins, accent, duration), nil
	default:
		// validateMapPresentationParams owns the message; this is defensive.
		return nil, fmt.Errorf("overlay: map item %q declares unsupported map_fx %q", ri.Item.ID, fx)
	}
}

func mapFxShapePosition(x, y, w, h float64, src *semanticPlan) []float64 {
	return canvasBoxPosition("shape", x-w/2, y-h/2, w, h, src.Width, src.Height)
}

func mapFxEllipse(id string, x, y, diameter float64, fill []float64, stroke *LayerStroke,
	style *LayerStyle, start, duration int64, src *semanticPlan, animation *LayerAnimation) Layer {
	return Layer{
		ID: id, Type: "shape",
		Size:       []float64{diameter, diameter},
		Position:   mapFxShapePosition(x, y, diameter, diameter, src),
		StartFrame: start, DurationFrames: duration,
		Style:     style,
		Shape:     &LayerShape{Type: "ellipse", Fill: fill, Stroke: stroke},
		Animation: animation,
	}
}

func mapFxRectangle(id string, x, y, w, h float64, fill []float64, style *LayerStyle,
	start, duration int64, src *semanticPlan, animation *LayerAnimation) Layer {
	return Layer{
		ID: id, Type: "shape",
		Size:       []float64{w, h},
		Position:   mapFxShapePosition(x, y, w, h, src),
		StartFrame: start, DurationFrames: duration,
		Style:     style,
		Shape:     &LayerShape{Type: "rectangle", Fill: fill},
		Animation: animation,
	}
}

func mapFxFadeAnimation(duration int64, peak float64) *LayerAnimation {
	if duration < 4 {
		return &LayerAnimation{Tracks: []AnimationTrack{{
			Property:  "opacity",
			Keyframes: []AnimationKeyframe{{Frame: 0, Value: peak}},
		}}}
	}
	fade := int64(8)
	if duration < 24 {
		fade = duration / 4
	}
	if fade < 1 {
		fade = 1
	}
	return &LayerAnimation{Tracks: []AnimationTrack{{
		Property: "opacity", Easing: "in_out_cubic",
		Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: 0.0},
			{Frame: fade, Value: peak},
			{Frame: duration - fade, Value: peak},
			{Frame: duration, Value: 0.0},
		},
	}}}
}

func mapFxPulseAnimation(duration int64, from, to, peak float64) *LayerAnimation {
	pulse := duration / 2
	if pulse < 2 {
		pulse = 2
	}
	if pulse > duration {
		pulse = duration
	}
	return &LayerAnimation{Tracks: []AnimationTrack{
		{
			Property: "scale", Easing: "out_cubic",
			Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: []float64{from, from, 1}},
				{Frame: pulse, Value: []float64{to, to, 1}},
			},
		},
		{
			Property: "opacity", Easing: "out_cubic",
			Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: 0.0},
				{Frame: minInt64(2, pulse), Value: peak},
				{Frame: pulse, Value: 0.0},
			},
		},
	}}
}

func mapFxPopAnimation(duration int64) *LayerAnimation {
	pop := minInt64(14, duration)
	if pop < 1 {
		pop = 1
	}
	fade := minInt64(4, pop)
	return &LayerAnimation{Tracks: []AnimationTrack{
		{
			Property: "scale", Easing: "out_cubic",
			Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: []float64{0.2, 0.2, 1}},
				{Frame: pop, Value: []float64{1, 1, 1}},
			},
		},
		{
			Property: "opacity", Easing: "out_cubic",
			Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: 0.0},
				{Frame: fade, Value: 1.0},
			},
		},
	}}
}

func mapFxGlowStyle(radius, intensity float64) *LayerStyle {
	return &LayerStyle{Glow: &LayerGlow{Color: mapFxAccent, Radius: radius, Intensity: intensity}}
}

func mapFxAreaGlowPulse(ri resolvedItem, src *semanticPlan, pin resolvedMapPin, accent []float64, duration int64) []Layer {
	const diameter = 240.0
	x, y := pin.RasterX, pin.RasterY
	layers := []Layer{mapFxEllipse(ri.Item.ID+":map_fx:area_glow", x, y, diameter*1.3,
		accent, nil, mapFxGlowStyle(20, 0.35), ri.Start, duration, src, mapFxFadeAnimation(duration, 0.42))}

	stagger := duration / 6
	if stagger < 1 {
		stagger = 1
	}
	for index := int64(0); index < 3; index++ {
		delay := index * stagger
		if delay >= duration-12 {
			break
		}
		ringDuration := duration - delay
		layers = append(layers, mapFxEllipse(
			fmt.Sprintf("%s:map_fx:area_ring_%d", ri.Item.ID, index), x, y, diameter,
			nil, &LayerStroke{Color: mapFxAccent, Width: mapFxRingWidthPX},
			mapFxGlowStyle(12, 0.55), ri.Start+delay, ringDuration, src,
			mapFxPulseAnimation(ringDuration, 0.55, 1.55, 0.62)))
	}
	return layers
}

func mapFxRadarLocationReveal(ri resolvedItem, src *semanticPlan, pin resolvedMapPin,
	accent, secondary []float64, duration int64) []Layer {
	const diameter = 260.0
	x, y := pin.RasterX, pin.RasterY
	layers := []Layer{mapFxEllipse(ri.Item.ID+":map_fx:radar_glow", x, y, diameter*1.2,
		accent, nil, mapFxGlowStyle(20, 0.3), ri.Start, duration, src, mapFxFadeAnimation(duration, 0.32))}

	stagger := duration / 9
	if stagger < 1 {
		stagger = 1
	}
	for index := int64(0); index < 3; index++ {
		delay := index * stagger
		if delay >= duration-12 {
			break
		}
		ringDuration := duration - delay
		layers = append(layers, mapFxEllipse(
			fmt.Sprintf("%s:map_fx:radar_ring_%d", ri.Item.ID, index), x, y, diameter,
			nil, &LayerStroke{Color: mapFxAccent, Width: mapFxRingWidthPX},
			mapFxGlowStyle(12, 0.5), ri.Start+delay, ringDuration, src,
			mapFxPulseAnimation(ringDuration, 0.22, 1.6, 0.55)))
	}

	layers = append(layers,
		mapFxRectangle(ri.Item.ID+":map_fx:radar_crosshair_x", x, y, diameter, 2,
			accent, mapFxGlowStyle(10, 0.4), ri.Start, duration, src, mapFxFadeAnimation(duration, 0.9)),
		mapFxRectangle(ri.Item.ID+":map_fx:radar_crosshair_y", x, y, 2, diameter,
			accent, mapFxGlowStyle(10, 0.4), ri.Start, duration, src, mapFxFadeAnimation(duration, 0.9)),
		mapFxEllipse(ri.Item.ID+":map_fx:radar_dot", x, y, 28,
			secondary, nil, &LayerStyle{Glow: &LayerGlow{Color: mapFxSecondary, Radius: 18, Intensity: 0.7}},
			ri.Start, duration, src, mapFxPopAnimation(duration)),
	)
	return layers
}

func mapFxLightBeamDrop(ri resolvedItem, src *semanticPlan, pin resolvedMapPin,
	accent, secondary []float64, duration int64) []Layer {
	const height = 210.0
	const width = 4.0
	x, y := pin.RasterX, pin.RasterY
	drop := duration / 5
	if drop < 8 {
		drop = 8
	}
	if drop > 24 {
		drop = 24
	}
	restCenterY := y - height/2
	restOffsetY := restCenterY - float64(src.Height)/2
	fadeStart := duration - 8
	if fadeStart < drop {
		fadeStart = drop
	}
	beam := mapFxRectangle(ri.Item.ID+":map_fx:energy_beam", x, restCenterY, width, height,
		accent, mapFxGlowStyle(16, 0.8), ri.Start, duration, src,
		&LayerAnimation{Tracks: []AnimationTrack{
			{
				Property: "position_y", Easing: "out_cubic",
				Keyframes: []AnimationKeyframe{
					{Frame: 0, Value: restOffsetY - height},
					{Frame: drop, Value: restOffsetY},
				},
			},
			{
				Property: "opacity", Easing: "in_out_cubic",
				Keyframes: []AnimationKeyframe{
					{Frame: 0, Value: 0.0},
					{Frame: drop / 2, Value: 0.9},
					{Frame: fadeStart, Value: 0.9},
					{Frame: duration, Value: 0.0},
				},
			},
		}})
	layers := []Layer{beam}
	impactStart := ri.Start + drop
	impactDuration := duration - drop
	if impactDuration < 1 {
		impactDuration = 1
	}
	layers = append(layers,
		mapFxEllipse(ri.Item.ID+":map_fx:beam_impact", x, y, 18,
			secondary, nil, &LayerStyle{Glow: &LayerGlow{Color: mapFxSecondary, Radius: 22, Intensity: 0.9}},
			impactStart, impactDuration, src, mapFxPopAnimation(impactDuration)),
		mapFxEllipse(ri.Item.ID+":map_fx:beam_ring", x, y, 260,
			nil, &LayerStroke{Color: mapFxAccent, Width: mapFxRingWidthPX},
			mapFxGlowStyle(12, 0.5), impactStart, impactDuration, src,
			mapFxPulseAnimation(impactDuration, 0.3, 1.45, 0.5)),
	)
	return layers
}

func mapFxCityNetworkExpansion(ri resolvedItem, src *semanticPlan, pins []resolvedMapPin,
	accent []float64, duration int64) []Layer {
	segmentCount := int64(len(pins) - 1)
	step := duration / segmentCount
	if step < 1 {
		step = 1
	}
	layers := make([]Layer, 0, len(pins)*2)
	for index, pin := range pins {
		delay := int64(index) * step
		if delay > duration-12 {
			delay = duration - 12
		}
		if delay < 0 {
			delay = 0
		}
		layers = append(layers, mapFxEllipse(
			fmt.Sprintf("%s:map_fx:network_node_%d", ri.Item.ID, index), pin.RasterX, pin.RasterY, 16,
			accent, nil, mapFxGlowStyle(16, 0.8), ri.Start+delay, duration-delay, src,
			mapFxPopAnimation(duration-delay)))
	}

	for index := 0; index < len(pins)-1; index++ {
		from, to := pins[index], pins[index+1]
		delay := int64(index) * step
		layerDuration := duration - delay
		if layerDuration < 1 {
			layerDuration = 1
		}
		reveal := step
		if reveal > layerDuration {
			reveal = layerDuration
		}
		trim := LayerPathOperator{Kind: "trim", Params: LayerTrimParams{
			Start: 0, End: 0, Offset: 0,
			Animation: &LayerPathAnimation{Easing: "in_out_cubic", Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: []float64{0, 0, 0}},
				{Frame: reveal, Value: []float64{0, 1, 0}},
				{Frame: layerDuration, Value: []float64{0, 1, 0}},
			}},
		}}
		layers = append(layers, Layer{
			ID: fmt.Sprintf("%s:map_fx:network_link_%d", ri.Item.ID, index), Type: "shape",
			Size:       []float64{float64(src.Width), float64(src.Height)},
			Position:   []float64{0, 0, 0},
			StartFrame: ri.Start + delay, DurationFrames: layerDuration,
			Style: mapFxGlowStyle(10, 0.45),
			Shape: &LayerShape{
				Type: "path",
				Path: []LayerPathCommand{
					{Type: "move_to", Point: []float64{from.RasterX, from.RasterY}},
					{Type: "line_to", Point: []float64{to.RasterX, to.RasterY}},
				},
				Stroke:    &LayerStroke{Color: mapFxAccent, Width: mapFxRingWidthPX},
				Operators: []LayerPathOperator{trim},
			},
		})
	}
	return layers
}

func minInt64(a, b int64) int64 {
	return int64(math.Min(float64(a), float64(b)))
}
