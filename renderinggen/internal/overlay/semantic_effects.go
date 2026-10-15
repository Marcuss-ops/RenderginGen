package overlay

import (
	"fmt"
	"math"
	"strings"
)

type effectParameterBounds struct {
	minimum      float64
	maximum      float64
	exclusiveMin bool
	integer      bool
}

func validateEffectNumber(params map[string]any, key, effect, itemID string, bounds effectParameterBounds) error {
	raw, exists := params[key]
	if !exists {
		return nil
	}
	value, err := toFloat(raw)
	if err != nil || !finite(value) || value > bounds.maximum || value < bounds.minimum || (bounds.exclusiveMin && value == bounds.minimum) {
		comparison := "["
		if bounds.exclusiveMin {
			comparison = "("
		}
		return fmt.Errorf("overlay: item %q %s %s must be in %s%g, %g]", itemID, effect, key, comparison, bounds.minimum, bounds.maximum)
	}
	if bounds.integer {
		integer, err := toInt(raw)
		if err != nil {
			return fmt.Errorf("overlay: item %q %s %s must be an integer", itemID, effect, key)
		}
		params[key] = integer
		return nil
	}
	params[key] = value
	return nil
}

func validateEffectParams(effect LayerEffect, itemID string) error {
	params := effect.Params
	for key, value := range params {
		if key == "color" {
			color, err := parseRGBA(value)
			if err != nil {
				return fmt.Errorf("overlay: item %q effect %s color is invalid: %w", itemID, effect.Type, err)
			}
			if err := validateRGBA(color); err != nil {
				return fmt.Errorf("overlay: item %q effect %s color is invalid: %w", itemID, effect.Type, err)
			}
			params[key] = color
			continue
		}
		if number, err := toFloat(value); err == nil && !finite(number) {
			return fmt.Errorf("overlay: item %q effect %s parameter %s must be finite", itemID, effect.Type, key)
		}
	}
	bounds := map[string]effectParameterBounds{
		"noise.amount": {minimum: 0, maximum: 1}, "noise.size": {minimum: 0, maximum: 256, exclusiveMin: true},
		"fractal_noise.amplitude": {minimum: 0, maximum: 4}, "fractal_noise.frequency": {minimum: 0, maximum: 10, exclusiveMin: true},
		"fractal_noise.octaves": {minimum: 1, maximum: 16, integer: true},
		"vignette.amount":       {minimum: 0, maximum: 1}, "vignette.radius": {minimum: 0, maximum: 1}, "vignette.softness": {minimum: 0, maximum: 1},
		"gaussian_blur.radius": {minimum: 0, maximum: 256}, "bloom.radius": {minimum: 0, maximum: 256}, "bloom.intensity": {minimum: 0, maximum: 4},
		"glow.radius": {minimum: 0, maximum: 256}, "glow.intensity": {minimum: 0, maximum: 4},
	}
	for key, limits := range bounds {
		prefix, name, _ := strings.Cut(key, ".")
		if effect.Type == prefix {
			if err := validateEffectNumber(params, name, effect.Type, itemID, limits); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveClipEffects(raw []semanticClipEffect) ([]LayerEffect, error) {
	out := make([]LayerEffect, 0, len(raw))
	for i, spec := range raw {
		typ := spec.Type
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
			params["seed"] = *spec.Seed
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
		if typ == "" {
			return nil, fmt.Errorf("overlay: clip effect %d requires a type", i)
		}
		present := map[string]bool{
			"amount": spec.Amount != nil, "radius": spec.Radius != nil, "softness": spec.Softness != nil,
			"block_size": spec.BlockSize != nil, "size": spec.Size != nil, "angle": spec.Angle != nil,
			"wavelength": spec.Wavelength != nil, "seed": spec.Seed != nil, "animated": spec.Animated != nil,
			"intensity": spec.Intensity != nil, "curvature": spec.Curvature != nil, "scale": spec.Scale != nil,
			"threshold": spec.Threshold != nil, "speed": spec.Speed != nil,
		}
		allowed := map[string]bool{}
		switch typ {
		case "glitch":
			allowed["amount"], allowed["seed"] = true, true
		case "scanlines":
			allowed["amount"], allowed["wavelength"] = true, true
		case "mosaic":
			allowed["block_size"] = true
		case "chromatic_aberration":
			allowed["amount"] = true
		case "halftone":
			allowed["size"], allowed["angle"], allowed["amount"] = true, true, true
		case "vignette":
			allowed["amount"], allowed["radius"], allowed["softness"] = true, true, true
		case "gaussian_blur":
			allowed["radius"] = true
		case "noise":
			allowed["amount"], allowed["size"], allowed["seed"], allowed["animated"] = true, true, true, true
		case "glow":
			allowed["radius"], allowed["intensity"] = true, true
		case "bloom":
			allowed["radius"], allowed["intensity"], allowed["threshold"] = true, true, true
		case "lens_distortion":
			allowed["curvature"], allowed["scale"] = true, true
		case "sharpen":
			allowed["amount"] = true
		case "banded_grain":
			allowed["intensity"], allowed["speed"], allowed["seed"] = true, true, true
		default:
			return nil, fmt.Errorf("overlay: unsupported clip effect type %q", typ)
		}
		for key, supplied := range present {
			if supplied && !allowed[key] {
				return nil, fmt.Errorf("overlay: clip effect %s does not accept %s", typ, key)
			}
		}
		if typ == "glitch" {
			amount, err := toFloat(params["amount"])
			if err != nil || !finite(amount) || amount < 0 || amount > 1 {
				return nil, fmt.Errorf("overlay: clip glitch amount must be in [0,1]")
			}
			seed := uint32(0)
			if params["seed"] != nil {
				rawSeed, ok := params["seed"].(uint32)
				if ok {
					seed = rawSeed
				} else {
					value, err := toInt(params["seed"])
					if err != nil || value < 0 || uint64(value) > uint64(^uint32(0)) {
						return nil, fmt.Errorf("overlay: clip glitch seed must be an unsigned 32-bit integer")
					}
					seed = uint32(value)
				}
			}
			out = append(out, LayerEffect{Type: "wave_warp", Params: map[string]any{"amplitude": 0.025 * amount, "wavelength": 0.035, "speed": 0.0, "direction": []float64{0, 1}, "wave_type": "square"}})
			out = append(out, LayerEffect{Type: "chromatic_aberration", Params: map[string]any{"red_offset": 8 * amount, "blue_offset": -8 * amount}})
			out = append(out, LayerEffect{Type: "noise", Params: map[string]any{"amount": 0.12 * amount, "seed": seed, "animated": true, "color_mode": "rgb", "size": 1.0}})
			continue
		}
		if typ == "scanlines" {
			amount, err := toFloat(params["amount"])
			if err != nil || !finite(amount) || amount < 0 || amount > 0.25 {
				return nil, fmt.Errorf("overlay: clip scanlines amount must be in [0,0.25]")
			}
			wavelength, err := toFloat(params["wavelength"])
			if err != nil || !finite(wavelength) || wavelength < 0.002 || wavelength > 1 {
				return nil, fmt.Errorf("overlay: clip scanlines wavelength must be in [0.002,1]")
			}
			params = map[string]any{"amplitude": amount, "wavelength": wavelength, "speed": 0.0, "direction": []float64{0, 1}, "wave_type": "square"}
			typ = "wave_warp"
		}
		if typ == "chromatic_aberration" {
			amount, err := toFloat(params["amount"])
			if err != nil || !finite(amount) || amount < -128 || amount > 128 {
				return nil, fmt.Errorf("overlay: chromatic_aberration amount must be in [-128,128]")
			}
			params = map[string]any{"red_offset": amount, "blue_offset": -amount}
		}
		if typ == "mosaic" {
			size, err := toInt(params["block_size"])
			if err != nil || size < 1 || size > 512 {
				return nil, fmt.Errorf("overlay: mosaic block_size must be in [1,512]")
			}
			params = map[string]any{"block_size": size}
		}
		if typ == "halftone" {
			size, err := toInt(params["size"])
			angle, ae := toFloat(params["angle"])
			amount, me := toFloat(params["amount"])
			if err != nil || ae != nil || me != nil || size < 1 || size > 128 || !finite(angle) || angle < -3600 || angle > 3600 || !finite(amount) || amount < 0 || amount > 1 {
				return nil, fmt.Errorf("overlay: halftone parameters are outside supported bounds")
			}
			params = map[string]any{"size": size, "angle": angle, "amount": amount}
		}
		if typ == "vignette" {
			amount, a := toFloat(params["amount"])
			radius, r := toFloat(params["radius"])
			softness, s := toFloat(params["softness"])
			if a != nil || r != nil || s != nil || !finite(amount) || !finite(radius) || !finite(softness) || amount < 0 || amount > 1 || radius < 0 || radius > 1 || softness < 0 || softness > 1 {
				return nil, fmt.Errorf("overlay: vignette parameters must be in [0,1]")
			}
		}
		if typ == "gaussian_blur" {
			radius, err := toFloat(params["radius"])
			if err != nil || !finite(radius) || radius < 0 || radius > 256 {
				return nil, fmt.Errorf("overlay: gaussian_blur radius must be in [0,256]")
			}
		}
		if typ == "noise" {
			amount, a := toFloat(params["amount"])
			size, s := toFloat(params["size"])
			if a != nil || s != nil || !finite(amount) || !finite(size) || amount < 0 || amount > 1 || size < 1 || size > 256 || math.Trunc(size) != size {
				return nil, fmt.Errorf("overlay: noise parameters are outside supported bounds")
			}
		}
		if typ == "glow" {
			radius, r := toFloat(params["radius"])
			intensity, i := toFloat(params["intensity"])
			if r != nil || i != nil || !finite(radius) || !finite(intensity) || radius < 0 || radius > 256 || intensity < 0 || intensity > 4 {
				return nil, fmt.Errorf("overlay: glow parameters must be radius in [0,256] and intensity in [0,4]")
			}
			params = map[string]any{"radius": radius, "intensity": intensity}
		}
		if typ == "bloom" {
			radius, r := toFloat(params["radius"])
			intensity, i := toFloat(params["intensity"])
			if r != nil || i != nil || !finite(radius) || !finite(intensity) || radius < 0 || radius > 256 || intensity < 0 || intensity > 4 {
				return nil, fmt.Errorf("overlay: bloom parameters must be radius in [0,256] and intensity in [0,4]")
			}
			threshold := 0.8
			if rawT, exists := params["threshold"]; exists {
				t, te := toFloat(rawT)
				if te != nil || !finite(t) || t < 0 || t > 1 {
					return nil, fmt.Errorf("overlay: bloom threshold must be in [0,1]")
				}
				threshold = t
			}
			params = map[string]any{"radius": radius, "intensity": intensity, "threshold": threshold}
		}
		if typ == "lens_distortion" {
			curv, c := toFloat(params["curvature"])
			if c != nil || !finite(curv) || curv < -1 || curv > 1 {
				return nil, fmt.Errorf("overlay: lens_distortion curvature must be in [-1,1]")
			}
			scale := 1.0
			if rawS, exists := params["scale"]; exists {
				s, se := toFloat(rawS)
				if se != nil || !finite(s) || s < 0.1 || s > 4 {
					return nil, fmt.Errorf("overlay: lens_distortion scale must be in [0.1,4]")
				}
				scale = s
			}
			params = map[string]any{"curvature": curv, "scale": scale}
		}
		if typ == "sharpen" {
			amount, a := toFloat(params["amount"])
			if a != nil || !finite(amount) || amount < 0 || amount > 10 {
				return nil, fmt.Errorf("overlay: sharpen amount must be in [0,10]")
			}
			params = map[string]any{"amount": amount}
		}
		if typ == "banded_grain" {
			intensity, ie := toFloat(params["intensity"])
			if ie != nil || !finite(intensity) || intensity < 0 || intensity > 4 {
				return nil, fmt.Errorf("overlay: banded_grain intensity must be in [0,4]")
			}
			speed := 0.0
			if rawSp, exists := params["speed"]; exists {
				sp, spe := toFloat(rawSp)
				if spe != nil || !finite(sp) || sp < -1000 || sp > 1000 {
					return nil, fmt.Errorf("overlay: banded_grain speed must be in [-1000,1000]")
				}
				speed = sp
			}
			seed := uint32(0)
			if rawSeed, exists := params["seed"]; exists {
				sd, sde := toInt(rawSeed)
				if sde == nil && sd >= 0 {
					seed = uint32(sd)
				}
			}
			params = map[string]any{"intensity": intensity, "speed": speed, "seed": seed}
		}
		out = append(out, LayerEffect{Type: typ, Params: params})
	}
	return out, nil
}

func compileClipGrid(grid *semanticClipGrid, width, height int, duration int64) (Layer, error) {
	if grid == nil || !finite(grid.SpacingPX) || grid.SpacingPX < 4 || grid.SpacingPX > 512 || !finite(grid.Opacity) || grid.Opacity < 0 || grid.Opacity > 1 {
		return Layer{}, fmt.Errorf("overlay: clip_grid parameters are outside supported bounds")
	}
	hexColor, err := parseHexColor(grid.Color)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: clip_grid color must be #RRGGBB: %w", err)
	}
	opacity := grid.Opacity
	isDots := grid.Style == "dots" || grid.Style == "dot_grid"
	if isDots {
		dotRadius := 2.0
		if grid.DotRadiusPX != nil && *grid.DotRadiusPX > 0 {
			if !finite(*grid.DotRadiusPX) || *grid.DotRadiusPX < 0.5 || *grid.DotRadiusPX > 64 {
				return Layer{}, fmt.Errorf("overlay: clip_grid dot_radius_px must be in [0.5,64]")
			}
			dotRadius = *grid.DotRadiusPX
		}
		return Layer{
			ID:             "clip__grid",
			Type:           "shape",
			StartFrame:     0,
			DurationFrames: duration,
			Size:           []float64{float64(width), float64(height)},
			Position:       []float64{0, 0},
			ScreenSpace:    true,
			Opacity:        &opacity,
			Shape: &LayerShape{
				Type:        "dot_grid",
				GridSpacing: grid.SpacingPX,
				DotRadius:   dotRadius,
				Fill:        hexColor,
			},
		}, nil
	}
	if grid.LineWidthPX == nil || !finite(*grid.LineWidthPX) || *grid.LineWidthPX < 0.25 || *grid.LineWidthPX > 32 {
		return Layer{}, fmt.Errorf("overlay: clip_grid line_width_px must be in [0.25,32]")
	}
	lineWidth := *grid.LineWidthPX
	return Layer{
		ID:             "clip__grid",
		Type:           "shape",
		StartFrame:     0,
		DurationFrames: duration,
		Size:           []float64{float64(width), float64(height)},
		Position:       []float64{0, 0},
		ScreenSpace:    true,
		Opacity:        &opacity,
		Shape: &LayerShape{
			Type:          "grid",
			GridSpacing:   grid.SpacingPX,
			GridLineWidth: lineWidth,
			Stroke:        &LayerStroke{Color: grid.Color, Width: lineWidth},
		},
	}, nil
}

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
		if !isSupportedEffectType(effType) {
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
			if key != "type" && key != "params" {
				if _, duplicate := params[key]; duplicate {
					return nil, fmt.Errorf("overlay: item %q effect %s repeats parameter %s", itemID, effType, key)
				}
				params[key] = value
			}
		}
		allowed := map[string]bool{"type": true, "params": true, "intensity": true, "amount": true, "amplitude": true, "size": true, "frequency": true, "octaves": true, "radius": true, "softness": true, "color": true, "threshold": true, "levels": true, "angle": true, "seed": true, "center": true, "direction": true}
		for key := range m {
			if !allowed[key] {
				return nil, fmt.Errorf("overlay: item %q effect %s has unsupported property %q", itemID, effType, key)
			}
		}
		if intensity, ok := params["intensity"]; ok {
			f, err := toFloat(intensity)
			if err != nil {
				return nil, fmt.Errorf("overlay: item %q effect %s intensity must be a number: %w", itemID, effType, err)
			}
			switch effType {
			case "noise", "vignette":
				if _, has := params["amount"]; !has {
					params["amount"] = f
				}
			case "fractal_noise":
				if _, has := params["amplitude"]; !has {
					params["amplitude"] = f
				}
			case "bloom", "glow":
				params["intensity"] = f
			}
		}
		effect := LayerEffect{Type: effType, Params: params}
		if err := validateEffectParams(effect, itemID); err != nil {
			return nil, err
		}
		out = append(out, effect)
	}
	return out, nil
}
