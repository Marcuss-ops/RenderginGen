package overlay

import (
	"fmt"
	"strings"
)

// editorialImageLook maps the two selectable archival treatments to existing
// renderer effects. These are image-only stacks, not full-frame effects.
func editorialImageLook(name string) ([]LayerEffect, bool) {
	stack := map[string][]LayerEffect{
		"archive_print": {
			{Type: "catalog", Params: map[string]any{"effect_id": "color.saturation", "params": []map[string]any{{"param": "value", "value": 0.0}}}},
			{Type: "noise", Params: map[string]any{"amount": 0.055, "size": 1.4, "seed": 1942, "animated": false, "color_mode": "monochrome"}},
			{Type: "vignette", Params: map[string]any{"amount": 0.13, "radius": 0.82, "softness": 0.55}},
		},
		"vintage_film_projection": {
			{Type: "catalog", Params: map[string]any{"effect_id": "color.saturation", "params": []map[string]any{{"param": "value", "value": 0.72}}}},
			{Type: "noise", Params: map[string]any{"amount": 0.045, "size": 1.2, "seed": 1924, "animated": true, "color_mode": "monochrome"}},
			{Type: "vignette", Params: map[string]any{"amount": 0.2, "radius": 0.78, "softness": 0.62}},
			{Type: "chromatic_aberration", Params: map[string]any{"red_offset": 0.8, "blue_offset": -0.8}},
		},
	}
	result, ok := stack[name]
	return result, ok
}

// resolveClipEffects lowers the clip-level adjustment stack. Parameter names,
// bounds, required fields and surface availability all come from the shared
// effect catalog; this function only decides how a public type becomes
// Chronon primitives (glitch fans out into three effects, scanlines become a
// wave warp, and so on).
func resolveClipEffects(raw []semanticClipEffect) ([]LayerEffect, error) {
	out := make([]LayerEffect, 0, len(raw))
	for i, spec := range raw {
		typ := spec.Type
		if typ == "" {
			return nil, fmt.Errorf("overlay: clip effect %d requires a type", i)
		}
		definition, ok := effectDefinitionFor(typ, effectSurfaceClip)
		if !ok {
			return nil, fmt.Errorf("overlay: unsupported clip effect type %q", typ)
		}
		params := clipEffectParams(spec)
		for key := range params {
			if _, accepted := definition.params[key]; !accepted {
				return nil, fmt.Errorf("overlay: clip effect %s does not accept %s", typ, key)
			}
		}
		for _, key := range definition.clipRequired {
			if _, present := params[key]; !present {
				return nil, fmt.Errorf("overlay: clip effect %s requires %s", typ, key)
			}
		}
		if err := validateEffectParams(definition, typ, params, "clip effect"); err != nil {
			return nil, err
		}
		switch typ {
		case "glitch":
			amount := params["amount"].(float64)
			seed := 0
			if value, present := params["seed"].(int); present {
				seed = value
			}
			out = append(out, LayerEffect{Type: "wave_warp", Params: map[string]any{"amplitude": 0.025 * amount, "wavelength": 0.035, "speed": 0.0, "direction": []float64{0, 1}, "wave_type": "square"}})
			out = append(out, LayerEffect{Type: "chromatic_aberration", Params: map[string]any{"red_offset": 8 * amount, "blue_offset": -8 * amount}})
			out = append(out, LayerEffect{Type: "noise", Params: map[string]any{"amount": 0.12 * amount, "seed": seed, "animated": true, "color_mode": "rgb", "size": 1.0}})
		case "scanlines":
			amount := params["amount"].(float64)
			wavelength := params["wavelength"].(float64)
			out = append(out, LayerEffect{Type: "wave_warp", Params: map[string]any{"amplitude": amount, "wavelength": wavelength, "speed": 0.0, "direction": []float64{0, 1}, "wave_type": "square"}})
		case "chromatic_aberration":
			amount := params["amount"].(float64)
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"red_offset": amount, "blue_offset": -amount}})
		case "bloom":
			threshold := 0.8
			if value, present := params["threshold"].(float64); present {
				threshold = value
			}
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"radius": params["radius"], "intensity": params["intensity"], "threshold": threshold}})
		case "lens_distortion":
			scale := 1.0
			if value, present := params["scale"].(float64); present {
				scale = value
			}
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"curvature": params["curvature"], "scale": scale}})
		case "glow":
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"radius": params["radius"], "intensity": params["intensity"]}})
		case "banded_grain":
			speed := 0.0
			if value, present := params["speed"].(float64); present {
				speed = value
			}
			seed := 0
			if value, present := params["seed"].(int); present {
				seed = value
			}
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"intensity": params["intensity"], "speed": speed, "seed": seed}})
		case "sharpen":
			out = append(out, LayerEffect{Type: typ, Params: map[string]any{"amount": params["amount"]}})
		default:
			out = append(out, LayerEffect{Type: typ, Params: params})
		}
	}
	return out, nil
}

// clipEffectParams projects the clip wire's flat typed fields onto the shared
// effect parameter vocabulary. Seeds are carried as int so the catalog's
// uint32 validation applies to both surfaces through toInt.
func clipEffectParams(spec semanticClipEffect) map[string]any {
	params := map[string]any{}
	if spec.Amount != nil {
		params["amount"] = *spec.Amount
	}
	if spec.Radius != nil {
		params["radius"] = *spec.Radius
	}
	if spec.Softness != nil {
		params["softness"] = *spec.Softness
	}
	if spec.BlockSize != nil {
		params["block_size"] = *spec.BlockSize
	}
	if spec.Size != nil {
		params["size"] = *spec.Size
	}
	if spec.Angle != nil {
		params["angle"] = *spec.Angle
	}
	if spec.Wavelength != nil {
		params["wavelength"] = *spec.Wavelength
	}
	if spec.Seed != nil {
		params["seed"] = int(*spec.Seed)
	}
	if spec.Animated != nil {
		params["animated"] = *spec.Animated
	}
	if spec.Intensity != nil {
		params["intensity"] = *spec.Intensity
	}
	if spec.Curvature != nil {
		params["curvature"] = *spec.Curvature
	}
	if spec.Scale != nil {
		params["scale"] = *spec.Scale
	}
	if spec.Threshold != nil {
		params["threshold"] = *spec.Threshold
	}
	if spec.Speed != nil {
		params["speed"] = *spec.Speed
	}
	return params
}

// compileClipGrid lowers the clip_grid block through the same shape resolver
// every shape item uses. The block is just a full-frame, screen-space grid;
// keeping a second bounds/fill/stroke implementation here let the two contracts
// drift (spacing, line width and dot radius each had duplicated limits).
func compileClipGrid(grid *semanticClipGrid, width, height int, duration int64) (Layer, error) {
	if grid == nil {
		return Layer{}, fmt.Errorf("overlay: clip_grid is required")
	}
	isDots := grid.Style == "dots" || grid.Style == "dot_grid"
	shapeType := "grid"
	params := map[string]any{
		"shape":        shapeType,
		"width":        float64(width),
		"height":       float64(height),
		"grid_spacing": grid.SpacingPX,
		"position":     []float64{0, 0},
		"opacity":      grid.Opacity,
	}
	if isDots {
		shapeType = "dot_grid"
		params["shape"] = shapeType
		params["fill"] = grid.Color
		dotRadius := 2.0
		if grid.DotRadiusPX != nil && *grid.DotRadiusPX > 0 {
			dotRadius = *grid.DotRadiusPX
		}
		params["dot_radius"] = dotRadius
	} else {
		if grid.LineWidthPX == nil {
			return Layer{}, fmt.Errorf("overlay: clip_grid line_width_px must be in [0.25,32]")
		}
		lineWidth := *grid.LineWidthPX
		params["grid_line_width"] = lineWidth
		params["stroke"] = map[string]any{"color": grid.Color, "width": lineWidth}
	}
	ri := resolvedItem{
		Item:   semanticItem{ID: "clip__grid", Kind: string(KindShape)},
		Params: params,
		Start:  0,
		End:    duration,
	}
	layer, err := resolveShape(ri, &semanticPlan{Width: width, Height: height})
	if err != nil {
		return Layer{}, err
	}
	layer.ScreenSpace = true
	return layer, nil
}

// resolveLayerEffects lowers an item's ordered effect stack. The shared catalog
// owns availability, parameters and bounds; this function only merges the
// entry-level shorthand (`radius`, `amount`, …) into `params`, applies the
// legacy intensity aliases and keeps authored order.
func resolveLayerEffects(raw any, itemID string) ([]LayerEffect, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q effects must be an array", itemID)
	}
	if len(list) > 64 {
		return nil, fmt.Errorf("overlay: item %q effects cannot exceed 64 entries", itemID)
	}
	subject := fmt.Sprintf("item %q effect", itemID)
	out := make([]LayerEffect, 0, len(list))
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("overlay: item %q effect entry must be an object", itemID)
		}
		effType, _ := m["type"].(string)
		effType = strings.TrimSpace(effType)
		if effType == "" {
			return nil, fmt.Errorf("overlay: item %q effect requires a type", itemID)
		}
		// Editorial image looks are payload-level shortcuts for stable stacks
		// of renderer-native effects. They decorate the image surface only;
		// scene background selection remains an independent plan-level choice.
		if stack, ok := editorialImageLook(effType); ok {
			params, hasParams := m["params"].(map[string]any)
			if len(m) > 1 && (!hasParams || len(params) != 0 || len(m) > 2) {
				return nil, fmt.Errorf("overlay: item %q effect %s does not accept parameters", itemID, effType)
			}
			out = append(out, stack...)
			continue
		}
		definition, supported := effectDefinitionFor(effType, effectSurfaceLayer)
		if !supported {
			return nil, fmt.Errorf("overlay: item %q unsupported effect type %q", itemID, effType)
		}
		params := make(map[string]any)
		if rawParams, exists := m["params"]; exists {
			pMap, ok := rawParams.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("overlay: item %q effect %s params must be an object", itemID, effType)
			}
			for key, value := range pMap {
				params[key] = value
			}
		}
		for key, value := range m {
			if key == "type" || key == "params" {
				continue
			}
			if _, duplicate := params[key]; duplicate {
				return nil, fmt.Errorf("overlay: item %q effect %s repeats parameter %s", itemID, effType, key)
			}
			params[key] = value
		}
		for key := range m {
			if !layerEffectProperty(key) {
				return nil, fmt.Errorf("overlay: item %q effect %s has unsupported property %q", itemID, effType, key)
			}
		}
		if intensity, ok := params["intensity"]; ok {
			value, err := toFloat(intensity)
			if err != nil {
				return nil, fmt.Errorf("overlay: item %q effect %s intensity must be a number: %w", itemID, effType, err)
			}
			switch effType {
			case "noise", "vignette":
				if _, has := params["amount"]; !has {
					params["amount"] = value
				}
			case "fractal_noise":
				if _, has := params["amplitude"]; !has {
					params["amplitude"] = value
				}
			case "bloom", "glow":
				params["intensity"] = value
			}
		}
		if err := validateEffectParams(definition, effType, params, subject); err != nil {
			return nil, err
		}
		out = append(out, LayerEffect{Type: effType, Params: params})
	}
	return out, nil
}
