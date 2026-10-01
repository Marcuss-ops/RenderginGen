package overlay

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var supportedShapeTypes = map[string]struct{}{
	"rect": {}, "rounded_rect": {}, "ellipse": {}, "line": {}, "path": {},
	"star": {}, "polygon": {}, "arrow": {}, "arc": {}, "grid": {},
	"dot_grid": {}, "device_frame": {}, "world_map": {}, "chart": {},
}

func isSupportedShapeType(t string) bool {
	_, ok := supportedShapeTypes[t]
	return ok
}

// EffectRegistry is the closed vocabulary accepted by shape layers. It is
// intentionally private: external mutation would turn validation into a
// bypass around Chronon's effect contract.
var effectRegistry = map[string]struct{}{
	"noise": {}, "fractal_noise": {}, "vignette": {}, "gaussian_blur": {},
	"drop_shadow": {}, "bloom": {}, "glow": {}, "light_rays": {},
	"threshold": {}, "mosaic": {}, "lut_3d": {}, "echo": {}, "keying": {},
	"chromatic_aberration": {}, "halftone": {}, "directional_blur": {},
	"radial_blur": {}, "sharpen": {}, "turbulent_displace": {}, "wave_warp": {},
	"ripple": {}, "displacement_map": {}, "corner_pin": {}, "twirl": {},
	"bulge": {}, "mesh_warp": {}, "catalog": {},
}

func isSupportedEffectType(t string) bool {
	_, ok := effectRegistry[t]
	return ok
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func validateRGBA(color []float64) error {
	if len(color) != 4 {
		return fmt.Errorf("expected RGBA[4], got %d components", len(color))
	}
	for index, component := range color {
		if !finite(component) || component < 0 || component > 1 {
			return fmt.Errorf("component %d must be finite and in [0, 1]", index)
		}
	}
	return nil
}

func validatePair(raw any, itemID, name string) ([]float64, error) {
	pair, err := parse2Floats(raw)
	if err != nil {
		return nil, fmt.Errorf("overlay: item %q %s must contain two finite numbers", itemID, name)
	}
	return pair, nil
}

func parsePathCommand(command map[string]any) (LayerPathCommand, error) {
	commandType, ok := command["type"].(string)
	if !ok || !isSupportedPathCommand(commandType) {
		return LayerPathCommand{}, fmt.Errorf("unsupported path command type %q", commandType)
	}
	allowed := map[string]bool{"type": true}
	result := LayerPathCommand{Type: commandType}
	readPair := func(key string) ([]float64, error) {
		allowed[key] = true
		raw, exists := command[key]
		if !exists {
			return nil, fmt.Errorf("missing %s", key)
		}
		return validatePair(raw, "", "path."+key)
	}
	switch commandType {
	case "move_to", "line_to":
		point, err := readPair("point")
		if err != nil {
			return LayerPathCommand{}, err
		}
		result.Point = point
	case "M", "L":
		var pRaw any
		var key string
		if raw, ok := command["point"]; ok {
			pRaw, key = raw, "point"
		} else if raw, ok := command["points"]; ok {
			pRaw, key = raw, "points"
		} else {
			return LayerPathCommand{}, fmt.Errorf("missing point or points")
		}
		allowed[key] = true
		pt, err := validatePair(pRaw, "", "path."+key)
		if err != nil {
			return LayerPathCommand{}, err
		}
		result.Point = pt
		result.Points = pt
	case "quadratic_to", "Q":
		point, pointErr := readPair("point")
		control, controlErr := readPair("control1")
		if pointErr != nil || controlErr != nil {
			return LayerPathCommand{}, fmt.Errorf("quadratic_to requires a finite point and control1")
		}
		result.Point, result.Control1 = point, control
	case "cubic_to", "C":
		point, pointErr := readPair("point")
		control1, control1Err := readPair("control1")
		control2, control2Err := readPair("control2")
		if pointErr != nil || control1Err != nil || control2Err != nil {
			return LayerPathCommand{}, fmt.Errorf("cubic_to requires a finite point, control1 and control2")
		}
		result.Point, result.Control1, result.Control2 = point, control1, control2
	case "close", "Z":
	}
	for key := range command {
		if !allowed[key] {
			return LayerPathCommand{}, fmt.Errorf("path command has unsupported property %q", key)
		}
	}
	return result, nil
}

func parsePath(raw any, itemID string) ([]LayerPathCommand, error) {
	commands, ok := raw.([]any)
	if !ok || len(commands) == 0 || len(commands) > 256 {
		return nil, fmt.Errorf("overlay: item %q shape.path must contain 1..256 commands", itemID)
	}
	result := make([]LayerPathCommand, 0, len(commands))
	for index, entry := range commands {
		command, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("overlay: item %q shape.path[%d] must be an object", itemID, index)
		}
		parsed, err := parsePathCommand(command)
		if err != nil {
			return nil, fmt.Errorf("overlay: item %q shape.path[%d]: %w", itemID, index, err)
		}
		if index == 0 && parsed.Type != "move_to" && parsed.Type != "M" {
			return nil, fmt.Errorf("overlay: item %q shape.path must begin with move_to", itemID)
		}
		result = append(result, parsed)
	}
	return result, nil
}

func validateEffectNumber(params map[string]any, key, effect, itemID string, minimum, maximum float64, exclusiveMinimum bool) error {
	raw, exists := params[key]
	if !exists {
		return nil
	}
	value, err := toFloat(raw)
	if err != nil || !finite(value) || value > maximum || value < minimum || (exclusiveMinimum && value == minimum) {
		comparison := "["
		if exclusiveMinimum {
			comparison = "("
		}
		return fmt.Errorf("overlay: item %q %s %s must be in %s%g, %g]", itemID, effect, key, comparison, minimum, maximum)
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
		if _, err := toFloat(value); err == nil {
			if !finite(mustFloat(value)) {
				return fmt.Errorf("overlay: item %q effect %s parameter %s must be finite", itemID, effect.Type, key)
			}
		}
	}
	bounds := map[string][3]float64{
		"noise.amount": {0, 1, 0}, "noise.size": {0, 256, 1},
		"fractal_noise.amplitude": {0, 4, 0}, "fractal_noise.frequency": {0, 10, 1}, "fractal_noise.octaves": {1, 16, 0},
		"vignette.amount": {0, 1, 0}, "vignette.radius": {0, 1, 0}, "vignette.softness": {0, 1, 0},
		"gaussian_blur.radius": {0, 256, 0}, "bloom.radius": {0, 256, 0}, "bloom.intensity": {0, 4, 0},
		"glow.radius": {0, 256, 0}, "glow.intensity": {0, 4, 0},
	}
	for key, limits := range bounds {
		prefix, name, _ := strings.Cut(key, ".")
		if effect.Type != prefix {
			continue
		}
		if err := validateEffectNumber(params, name, effect.Type, itemID, limits[0], limits[1], limits[2] == 1); err != nil {
			return err
		}
	}
	return nil
}

func mustFloat(raw any) float64 {
	value, _ := toFloat(raw)
	return value
}

func finiteAny(raw any) bool {
	value, err := toFloat(raw)
	return err == nil && finite(value)
}

func isSupportedPathCommand(command string) bool {
	switch command {
	case "move_to", "line_to", "quadratic_to", "cubic_to", "close", "M", "L", "Q", "C", "Z":
		return true
	default:
		return false
	}
}

func validateDimension(value float64, name, itemID string) error {
	if !finite(value) || value <= 0 {
		return fmt.Errorf("overlay: item %q requires positive finite %s", itemID, name)
	}
	return nil
}

func validateShapeContract(shape *LayerShape, width, height float64, itemID string) error {
	if err := validateRGBAFromShape(shape); err != nil {
		return fmt.Errorf("overlay: item %q shape.fill is invalid: %w", itemID, err)
	}
	if shape.Radius < 0 || !finite(shape.Radius) {
		return fmt.Errorf("overlay: item %q shape.radius must be non-negative and finite", itemID)
	}
	maxRadius := math.Min(width, height) * 0.5
	for _, radius := range shape.CornerRadius {
		if radius < 0 || radius > maxRadius || !finite(radius) {
			return fmt.Errorf("overlay: item %q corner_radius values must be in [0, %v]", itemID, maxRadius)
		}
	}
	if shape.Stroke != nil {
		if shape.Stroke.Width < 0 || !finite(shape.Stroke.Width) {
			return fmt.Errorf("overlay: item %q stroke width must be non-negative and finite", itemID)
		}
		if _, err := parseHexColor(shape.Stroke.Color); err != nil {
			return fmt.Errorf("overlay: item %q stroke color is invalid: %w", itemID, err)
		}
	}
	return nil
}

func validateRGBAFromShape(shape *LayerShape) error {
	fill, ok := shape.Fill.([]float64)
	if !ok || fill == nil {
		return nil
	}
	return validateRGBA(fill)
}

func firstParam(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

func parse2Floats(raw any) ([]float64, error) {
	var pair []float64
	switch value := raw.(type) {
	case []float64:
		if len(value) != 2 {
			break
		}
		pair = append([]float64(nil), value...)
	case []any:
		if len(value) != 2 {
			break
		}
		pair = make([]float64, 2)
		for index := range value {
			parsed, err := toFloat(value[index])
			if err != nil {
				return nil, err
			}
			pair[index] = parsed
		}
	default:
		return nil, fmt.Errorf("expected 2 numbers, got %T", raw)
	}
	if pair == nil || !finite(pair[0]) || !finite(pair[1]) {
		return nil, fmt.Errorf("expected 2 finite numbers, got %v", raw)
	}
	return pair, nil
}

func parse4Floats(raw any) ([]float64, error) {
	var result []float64
	switch values := raw.(type) {
	case []float64:
		if len(values) != 4 {
			break
		}
		result = append([]float64(nil), values...)
	case []any:
		if len(values) != 4 {
			break
		}
		result = make([]float64, len(values))
		for index, value := range values {
			parsed, err := toFloat(value)
			if err != nil {
				return nil, err
			}
			result[index] = parsed
		}
	}
	if result == nil {
		return nil, fmt.Errorf("expected 4 numbers, got %v", raw)
	}
	for _, value := range result {
		if !finite(value) {
			return nil, fmt.Errorf("expected 4 finite numbers")
		}
	}
	return result, nil
}

func parseHexColor(s string) ([]float64, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") {
		return nil, fmt.Errorf("invalid hex color %q (missing #)", s)
	}
	hexPart := s[1:]
	if len(hexPart) == 6 {
		val, err := strconv.ParseUint(hexPart, 16, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid hex color %q: %w", s, err)
		}
		r := float64((val>>16)&0xFF) / 255.0
		g := float64((val>>8)&0xFF) / 255.0
		b := float64(val&0xFF) / 255.0
		return []float64{r, g, b, 1.0}, nil
	} else if len(hexPart) == 8 {
		val, err := strconv.ParseUint(hexPart, 16, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid hex color %q: %w", s, err)
		}
		r := float64((val>>24)&0xFF) / 255.0
		g := float64((val>>16)&0xFF) / 255.0
		b := float64((val>>8)&0xFF) / 255.0
		a := float64(val&0xFF) / 255.0
		return []float64{r, g, b, a}, nil
	}
	return nil, fmt.Errorf("invalid hex color length %q", s)
}

func parseRGBA(raw any) ([]float64, error) {
	var color []float64
	switch value := raw.(type) {
	case string:
		parsed, err := parseHexColor(value)
		if err != nil {
			return nil, err
		}
		color = parsed
	case []float64:
		if len(value) != 3 && len(value) != 4 {
			return nil, fmt.Errorf("expected 3 or 4 RGBA components, got %d", len(value))
		}
		color = append([]float64(nil), value...)
	case []any:
		if len(value) != 3 && len(value) != 4 {
			return nil, fmt.Errorf("expected 3 or 4 RGBA components, got %d", len(value))
		}
		color = make([]float64, len(value))
		for index, component := range value {
			parsed, err := toFloat(component)
			if err != nil {
				return nil, err
			}
			color[index] = parsed
		}
	default:
		return nil, fmt.Errorf("unsupported color format %T", raw)
	}
	if len(color) == 3 {
		color = append(color, 1)
	}
	if err := validateRGBA(color); err != nil {
		return nil, err
	}
	return color, nil
}

func parseGradient(raw any, itemID string) (*LayerGradient, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q gradient must be an object", itemID)
	}
	allowed := map[string]bool{"type": true, "stops": true, "color_stops": true, "opacity_stops": true, "start": true, "end": true}
	for key := range m {
		if !allowed[key] {
			return nil, fmt.Errorf("overlay: item %q gradient has unsupported property %q", itemID, key)
		}
	}
	gradType := "linear"
	if rawType, exists := m["type"]; exists {
		t, ok := rawType.(string)
		if !ok || (t != "linear" && t != "radial" && t != "conic") {
			return nil, fmt.Errorf("overlay: item %q unsupported gradient type %v", itemID, rawType)
		}
		gradType = t
	}
	if _, hasStops := m["stops"]; hasStops {
		if _, hasColorStops := m["color_stops"]; hasColorStops {
			return nil, fmt.Errorf("overlay: item %q gradient cannot declare both stops and color_stops", itemID)
		}
	}
	start := []float64{0, 0}
	if sRaw, ok := m["start"]; ok {
		s, err := parse2Floats(sRaw)
		if err != nil || !finite(s[0]) || !finite(s[1]) {
			return nil, fmt.Errorf("overlay: item %q gradient start must contain two finite numbers", itemID)
		}
		start = s
	}
	end := []float64{0, 1}
	if eRaw, ok := m["end"]; ok {
		e, err := parse2Floats(eRaw)
		if err != nil || !finite(e[0]) || !finite(e[1]) {
			return nil, fmt.Errorf("overlay: item %q gradient end must contain two finite numbers", itemID)
		}
		end = e
	}

	var colorStops []LayerGradientStop
	var stopsRaw [][2]any

	if rawStops, exists := m["stops"]; exists {
		stopsList, ok := rawStops.([]any)
		if !ok || len(stopsList) < 2 {
			return nil, fmt.Errorf("overlay: item %q gradient stops must contain at least two entries", itemID)
		}
		for _, stopItem := range stopsList {
			pair, ok := stopItem.([]any)
			if !ok || len(pair) != 2 {
				return nil, fmt.Errorf("overlay: item %q gradient stop must be [color, position]", itemID)
			}
			col, err := parseRGBA(pair[0])
			if err != nil {
				return nil, fmt.Errorf("overlay: item %q gradient stop color invalid: %w", itemID, err)
			}
			pos, err := toFloat(pair[1])
			if err != nil || pos < 0 || pos > 1 || math.IsNaN(pos) || math.IsInf(pos, 0) {
				return nil, fmt.Errorf("overlay: item %q gradient stop position %v must be in [0, 1]", itemID, pair[1])
			}
			colorStops = append(colorStops, LayerGradientStop{Position: pos, Color: col})
			stopsRaw = append(stopsRaw, [2]any{pair[0], pos})
		}
	} else if rawStops, exists := m["color_stops"]; exists {
		cStops, ok := rawStops.([]any)
		if !ok || len(cStops) < 2 {
			return nil, fmt.Errorf("overlay: item %q gradient color_stops must contain at least two entries", itemID)
		}
		for _, stopItem := range cStops {
			sm, ok := stopItem.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("overlay: item %q color_stop must be an object", itemID)
			}
			for key := range sm {
				if key != "color" && key != "position" {
					return nil, fmt.Errorf("overlay: item %q color_stop has unsupported property %q", itemID, key)
				}
			}
			colorRaw, hasColor := sm["color"]
			positionRaw, hasPosition := sm["position"]
			if !hasColor || !hasPosition {
				return nil, fmt.Errorf("overlay: item %q color_stop requires color and position", itemID)
			}
			col, err := parseRGBA(colorRaw)
			if err != nil {
				return nil, fmt.Errorf("overlay: item %q gradient stop color invalid: %w", itemID, err)
			}
			pos, err := toFloat(positionRaw)
			if err != nil || pos < 0 || pos > 1 || !finite(pos) {
				return nil, fmt.Errorf("overlay: item %q gradient stop position %v must be in [0, 1]", itemID, positionRaw)
			}
			colorStops = append(colorStops, LayerGradientStop{Position: pos, Color: col})
			stopsRaw = append(stopsRaw, [2]any{colorRaw, pos})
		}
	}
	var opacityStops []LayerOpacityStop
	if rawStops, exists := m["opacity_stops"]; exists {
		oStops, ok := rawStops.([]any)
		if !ok || len(oStops) < 2 {
			return nil, fmt.Errorf("overlay: item %q gradient opacity_stops must contain at least two entries", itemID)
		}
		for _, stopItem := range oStops {
			sm, ok := stopItem.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("overlay: item %q gradient opacity_stop must be an object", itemID)
			}
			for key := range sm {
				if key != "position" && key != "opacity" {
					return nil, fmt.Errorf("overlay: item %q opacity_stop has unsupported property %q", itemID, key)
				}
			}
			positionRaw, hasPosition := sm["position"]
			opacityRaw, hasOpacity := sm["opacity"]
			if !hasPosition || !hasOpacity {
				return nil, fmt.Errorf("overlay: item %q opacity_stop requires position and opacity", itemID)
			}
			pos, errP := toFloat(positionRaw)
			op, errO := toFloat(opacityRaw)
			if errP != nil || errO != nil || pos < 0 || pos > 1 || op < 0 || op > 1 || !finite(pos) || !finite(op) {
				return nil, fmt.Errorf("overlay: item %q gradient opacity_stop values must be in [0, 1]", itemID)
			}
			opacityStops = append(opacityStops, LayerOpacityStop{Position: pos, Opacity: op})
		}
	}
	if len(colorStops) < 2 {
		return nil, fmt.Errorf("overlay: item %q gradient requires at least two color stops", itemID)
	}
	return &LayerGradient{
		Type:         gradType,
		Stops:        stopsRaw,
		ColorStops:   colorStops,
		OpacityStops: opacityStops,
		Start:        start,
		End:          end,
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

		// Normalize parameters
		if intensity, ok := params["intensity"]; ok {
			f, err := toFloat(intensity)
			if err == nil {
				switch effType {
				case "noise", "vignette":
					if _, hasAmount := params["amount"]; !hasAmount {
						params["amount"] = f
					}
				case "fractal_noise":
					if _, hasAmp := params["amplitude"]; !hasAmp {
						params["amplitude"] = f
					}
				case "bloom", "glow":
					params["intensity"] = f
				}
			}
		}

		// Fail-closed bounds check for each effect kind
		switch effType {
		case "noise":
			if amt, ok := params["amount"]; ok {
				f, err := toFloat(amt)
				if err != nil || f < 0 || f > 1 || math.IsNaN(f) || math.IsInf(f, 0) {
					return nil, fmt.Errorf("overlay: item %q noise amount %v must be in [0, 1]", itemID, amt)
				}
			}
			if sz, ok := params["size"]; ok {
				f, err := toFloat(sz)
				if err != nil || f <= 0 || f > 256 || math.IsNaN(f) {
					return nil, fmt.Errorf("overlay: item %q noise size %v must be in (0, 256]", itemID, sz)
				}
			}
		case "fractal_noise":
			if amp, ok := params["amplitude"]; ok {
				f, err := toFloat(amp)
				if err != nil || f < 0 || f > 4 || math.IsNaN(f) {
					return nil, fmt.Errorf("overlay: item %q fractal_noise amplitude %v must be in [0, 4]", itemID, amp)
				}
			}
			if freq, ok := params["frequency"]; ok {
				f, err := toFloat(freq)
				if err != nil || f <= 0 || f > 10 || math.IsNaN(f) {
					return nil, fmt.Errorf("overlay: item %q fractal_noise frequency %v must be in (0, 10]", itemID, freq)
				}
			}
			if oct, ok := params["octaves"]; ok {
				i, err := toInt(oct)
				if err != nil || i < 1 || i > 16 {
					return nil, fmt.Errorf("overlay: item %q fractal_noise octaves %v must be in [1, 16]", itemID, oct)
				}
			}
		case "vignette":
			if rad, ok := params["radius"]; ok {
				f, err := toFloat(rad)
				if err != nil || f < 0 || f > 1 || math.IsNaN(f) || math.IsInf(f, 0) {
					return nil, fmt.Errorf("overlay: item %q vignette radius %v must be in [0, 1]", itemID, rad)
				}
			}
			if soft, ok := params["softness"]; ok {
				f, err := toFloat(soft)
				if err != nil || f < 0 || f > 1 || math.IsNaN(f) || math.IsInf(f, 0) {
					return nil, fmt.Errorf("overlay: item %q vignette softness %v must be in [0, 1]", itemID, soft)
				}
			}
			if amt, ok := params["amount"]; ok {
				f, err := toFloat(amt)
				if err != nil || f < 0 || f > 1 || math.IsNaN(f) || math.IsInf(f, 0) {
					return nil, fmt.Errorf("overlay: item %q vignette amount %v must be in [0, 1]", itemID, amt)
				}
			}
		case "gaussian_blur":
			if rad, ok := params["radius"]; ok {
				f, err := toFloat(rad)
				if err != nil || f < 0 || f > 256 || math.IsNaN(f) {
					return nil, fmt.Errorf("overlay: item %q gaussian_blur radius %v must be in [0, 256]", itemID, rad)
				}
			}
		case "bloom":
			if rad, ok := params["radius"]; ok {
				f, err := toFloat(rad)
				if err != nil || f < 0 || f > 256 || math.IsNaN(f) {
					return nil, fmt.Errorf("overlay: item %q bloom radius %v must be in [0, 256]", itemID, rad)
				}
			}
			if inten, ok := params["intensity"]; ok {
				f, err := toFloat(inten)
				if err != nil || f < 0 || f > 4 || math.IsNaN(f) {
					return nil, fmt.Errorf("overlay: item %q bloom intensity %v must be in [0, 4]", itemID, inten)
				}
			}
		case "glow":
			if rad, ok := params["radius"]; ok {
				f, err := toFloat(rad)
				if err != nil || f < 0 || f > 256 || math.IsNaN(f) {
					return nil, fmt.Errorf("overlay: item %q glow radius %v must be in [0, 256]", itemID, rad)
				}
			}
			if inten, ok := params["intensity"]; ok {
				f, err := toFloat(inten)
				if err != nil || f < 0 || f > 4 || math.IsNaN(f) {
					return nil, fmt.Errorf("overlay: item %q glow intensity %v must be in [0, 4]", itemID, inten)
				}
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

func compileShapeLayer(ri resolvedItem, src *semanticPlan) (Layer, error) {
	return resolveShape(ri, src)
}

// resolveShape is the one lowering boundary for shape items: semantic params
// enter here, are validated fail-closed, and leave as the normalized Chronon
// layer representation.
func resolveShape(ri resolvedItem, src *semanticPlan) (Layer, error) {
	if ri.Params == nil {
		return Layer{}, fmt.Errorf("overlay: item %q shape params are required", ri.Item.ID)
	}
	shapeType := stringParam(ri.Params, "shape", stringParam(ri.Params, "type", "rect"))
	if raw, exists := ri.Params["shape"]; exists {
		if _, ok := raw.(string); !ok {
			return Layer{}, fmt.Errorf("overlay: item %q shape must be a string", ri.Item.ID)
		}
	}
	if raw, exists := ri.Params["type"]; exists {
		if _, ok := raw.(string); !ok {
			return Layer{}, fmt.Errorf("overlay: item %q shape.type must be a string", ri.Item.ID)
		}
	}
	if rawShape, hasShape := ri.Params["shape"]; hasShape {
		if rawType, hasType := ri.Params["type"]; hasType && rawType != rawShape {
			return Layer{}, fmt.Errorf("overlay: item %q shape and type disagree", ri.Item.ID)
		}
	}
	if shapeType == "dotgrid" {
		shapeType = "dot_grid"
	} else if shapeType == "worldmap" {
		shapeType = "world_map"
	}
	if !isSupportedShapeType(shapeType) {
		return Layer{}, fmt.Errorf("overlay: item %q has unsupported shape type %q", ri.Item.ID, shapeType)
	}

	width := float64(src.Width)
	height := float64(src.Height)
	if raw, exists := ri.Params["width"]; exists {
		w, err := toFloat(raw)
		if err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q requires positive finite width", ri.Item.ID)
		}
		if err := validateDimension(w, "width", ri.Item.ID); err != nil {
			return Layer{}, err
		}
		width = w
	}
	if raw, exists := ri.Params["height"]; exists {
		h, err := toFloat(raw)
		if err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q requires positive finite height", ri.Item.ID)
		}
		if err := validateDimension(h, "height", ri.Item.ID); err != nil {
			return Layer{}, err
		}
		height = h
	}
	if err := validateDimension(width, "width", ri.Item.ID); err != nil {
		return Layer{}, err
	}
	if err := validateDimension(height, "height", ri.Item.ID); err != nil {
		return Layer{}, err
	}

	pos := []float64{0, 0}
	if pRaw, ok := ri.Params["position"]; ok {
		p, err := parse2Floats(pRaw)
		if err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q shape.position must contain two finite numbers: %w", ri.Item.ID, err)
		}
		if math.IsNaN(p[0]) || math.IsNaN(p[1]) || math.IsInf(p[0], 0) || math.IsInf(p[1], 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.position must contain two finite numbers", ri.Item.ID)
		}
		pos = p
	} else if width < float64(src.Width) || height < float64(src.Height) {
		pos = []float64{(float64(src.Width) - width) / 2, (float64(src.Height) - height) / 2}
	}

	shape := &LayerShape{Type: shapeType}

	// 1. Procedural bounds checks
	if ptsRaw, ok := ri.Params["points"]; ok {
		pts, err := toInt(ptsRaw)
		if err != nil || pts < 3 || pts > 64 {
			return Layer{}, fmt.Errorf("overlay: item %q shape.points %v must be in [3, 64]", ri.Item.ID, ptsRaw)
		}
		shape.Points = pts
	}

	if rotRaw, ok := firstParam(ri.Params, "rotation_degrees", "rotation", "shape_rotation_deg"); ok {
		rot, err := toFloat(rotRaw)
		if err != nil || math.IsNaN(rot) || math.IsInf(rot, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.rotation_degrees must be finite", ri.Item.ID)
		}
		shape.RotationDeg = rot
	}

	if irRaw, ok := ri.Params["inner_radius_ratio"]; ok {
		ir, err := toFloat(irRaw)
		if err != nil || ir < 0.05 || ir > 0.95 || math.IsNaN(ir) || math.IsInf(ir, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.inner_radius_ratio %v must be in [0.05, 0.95]", ri.Item.ID, irRaw)
		}
		shape.InnerRadius = ir
	}
	if spRaw, ok := firstParam(ri.Params, "grid_spacing", "spacing"); ok {
		sp, err := toFloat(spRaw)
		if err != nil || sp < 4.0 || sp > 512.0 || math.IsNaN(sp) || math.IsInf(sp, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.grid_spacing %v must be in [4, 512]", ri.Item.ID, spRaw)
		}
		shape.GridSpacing = sp
	}

	if lwRaw, ok := firstParam(ri.Params, "grid_line_width", "line_width"); ok {
		lw, err := toFloat(lwRaw)
		if err != nil || lw < 0.25 || lw > 32.0 || math.IsNaN(lw) || math.IsInf(lw, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.grid_line_width %v must be in [0.25, 32]", ri.Item.ID, lwRaw)
		}
		shape.GridLineWidth = lw
	}

	if drRaw, ok := firstParam(ri.Params, "dot_radius", "dotRadius"); ok {
		dr, err := toFloat(drRaw)
		if err != nil || dr < 0.5 || dr > 64.0 || math.IsNaN(dr) || math.IsInf(dr, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.dot_radius %v must be in [0.5, 64]", ri.Item.ID, drRaw)
		}
		shape.DotRadius = dr
	}

	if aStartRaw, ok := firstParam(ri.Params, "arc_start_deg", "start_deg"); ok {
		st, err := toFloat(aStartRaw)
		if err != nil || math.IsNaN(st) || math.IsInf(st, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.arc_start_deg must be finite", ri.Item.ID)
		}
		shape.ArcStartDeg = st
	}

	if aSweepRaw, ok := firstParam(ri.Params, "arc_sweep_deg", "sweep_deg"); ok {
		sw, err := toFloat(aSweepRaw)
		if err != nil || sw < 1.0 || sw > 359.9 || math.IsNaN(sw) || math.IsInf(sw, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.arc_sweep_deg %v must be in [1, 359.9]", ri.Item.ID, aSweepRaw)
		}
		shape.ArcSweepDeg = sw
	}

	if aThickRaw, ok := firstParam(ri.Params, "arc_thickness", "thickness"); ok {
		th, err := toFloat(aThickRaw)
		if err != nil || th < 0 || th > 10000.0 || math.IsNaN(th) || math.IsInf(th, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.arc_thickness %v must be in [0, 10000]", ri.Item.ID, aThickRaw)
		}
		shape.ArcThickness = th
	}

	maxRad := math.Min(width, height) * 0.5
	if radRaw, ok := ri.Params["radius"]; ok {
		rad, err := toFloat(radRaw)
		if err != nil || rad < 0 || math.IsNaN(rad) || math.IsInf(rad, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.radius must be non-negative", ri.Item.ID)
		}
		if shapeType == "rounded_rect" && rad > maxRad {
			return Layer{}, fmt.Errorf("overlay: item %q rounded_rect radius %v exceeds half the smaller dimension %v", ri.Item.ID, rad, maxRad)
		}
		shape.Radius = rad
	}

	if crRaw, ok := ri.Params["corner_radius"]; ok {
		cr, err := parse4Floats(crRaw)
		if err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q shape.corner_radius must contain [tl, tr, br, bl]: %w", ri.Item.ID, err)
		}
		for i, c := range cr {
			if c < 0 || c > maxRad || math.IsNaN(c) || math.IsInf(c, 0) {
				return Layer{}, fmt.Errorf("overlay: item %q corner_radius[%d]=%v outside [0, %v]", ri.Item.ID, i, c, maxRad)
			}
		}
		shape.CornerRadius = cr
	}

	if shapeType == "device_frame" || ri.Params["device_frame_kind"] != nil || ri.Params["device"] != nil {
		dfKind := stringParam(ri.Params, "device_frame_kind", "")
		if dfKind == "" {
			dfKind = stringParam(ri.Params, "device", "phone")
		}
		if dfKind != "phone" && dfKind != "browser" && dfKind != "window" {
			return Layer{}, fmt.Errorf("overlay: item %q device_frame_kind must be phone or browser", ri.Item.ID)
		}
		shape.DeviceFrame = dfKind
		shape.Device = dfKind
		chrome := true
		if rawChrome, exists := ri.Params["chrome"]; exists {
			value, ok := rawChrome.(bool)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q device_frame chrome must be a boolean", ri.Item.ID)
			}
			chrome = value
		}

		shape.Chrome = &chrome
	}

	wmRaw, wmExists := ri.Params["world_map"]
	if shapeType == "world_map" || wmExists {
		srcMap, ok := wmRaw.(map[string]any)
		if !ok && wmExists {
			return Layer{}, fmt.Errorf("overlay: item %q world_map must be an object", ri.Item.ID)
		}
		if srcMap == nil {
			srcMap = ri.Params
		}
		proj := "mercator"
		if rawProjection, exists := srcMap["projection"]; exists {
			p, ok := rawProjection.(string)
			if !ok || (p != "mercator" && p != "equirectangular" && p != "equirect") {
				return Layer{}, fmt.Errorf("overlay: item %q world_map projection must be mercator or equirectangular", ri.Item.ID)
			}
			if p == "equirect" {
				p = "equirectangular"
			}
			proj = p
		}
		for key := range srcMap {
			switch key {
			case "projection", "center", "zoom", "highlight", "highlight_color", "route", "dots":
			default:
				return Layer{}, fmt.Errorf("overlay: item %q world_map has unsupported property %q", ri.Item.ID, key)
			}
		}
		center := []float64{0, 0}
		if cRaw, ok := srcMap["center"]; ok {
			c, err := parse2Floats(cRaw)
			if err != nil || c[0] < -180 || c[0] > 180 || c[1] < -90 || c[1] > 90 {
				return Layer{}, fmt.Errorf("overlay: item %q world_map center must be [lon, lat] within [-180, 180] and [-90, 90]", ri.Item.ID)
			}
			center = c
		}
		zoom := 1.0
		if zRaw, ok := srcMap["zoom"]; ok {
			z, err := toFloat(zRaw)
			if err != nil || z < 1.0 || z > 8.0 || math.IsNaN(z) {
				return Layer{}, fmt.Errorf("overlay: item %q world_map zoom %v must be in [1, 8]", ri.Item.ID, zRaw)
			}
			zoom = z
		}
		var highlight []string
		if rawHighlight, exists := srcMap["highlight"]; exists {
			hlRaw, ok := rawHighlight.([]any)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q world_map highlight must be an array", ri.Item.ID)
			}
			if len(hlRaw) > 64 {
				return Layer{}, fmt.Errorf("overlay: item %q world_map highlight cannot exceed 64 codes", ri.Item.ID)
			}
			for _, code := range hlRaw {
				s, ok := code.(string)
				if !ok || strings.TrimSpace(s) == "" {
					return Layer{}, fmt.Errorf("overlay: item %q world_map highlight entries must be non-empty strings", ri.Item.ID)
				}
				highlight = append(highlight, s)
			}
		}
		hlCol, _ := srcMap["highlight_color"].(string)
		if rawColor, exists := srcMap["highlight_color"]; exists {
			color, ok := rawColor.(string)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q world_map highlight_color must be a string", ri.Item.ID)
			}
			if _, err := parseHexColor(color); err != nil {
				return Layer{}, fmt.Errorf("overlay: item %q world_map highlight_color is invalid", ri.Item.ID)
			}
			hlCol = color
		}
		var route [][]float64
		if rawRoute, exists := srcMap["route"]; exists {
			routeRaw, ok := rawRoute.([]any)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q world_map route must be an array", ri.Item.ID)
			}
			if len(routeRaw) > 256 {
				return Layer{}, fmt.Errorf("overlay: item %q world_map route cannot exceed 256 points", ri.Item.ID)
			}
			for _, ptRaw := range routeRaw {
				pt, err := parse2Floats(ptRaw)
				if err != nil || pt[0] < -180 || pt[0] > 180 || pt[1] < -90 || pt[1] > 90 {
					return Layer{}, fmt.Errorf("overlay: item %q world_map route coordinate out of range", ri.Item.ID)
				}
				route = append(route, pt)
			}
		}
		dots, _ := srcMap["dots"].(bool)
		if rawDots, exists := srcMap["dots"]; exists {
			value, ok := rawDots.(bool)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q world_map dots must be a boolean", ri.Item.ID)
			}
			dots = value
		}
		shape.WorldMap = &LayerWorldMap{
			Projection:     proj,
			Center:         center,
			Zoom:           zoom,
			Highlight:      highlight,
			HighlightColor: hlCol,
			Route:          route,
			Dots:           dots,
		}
	}

	if shapeType == "arrow" || ri.Params["arrow_direction"] != nil || ri.Params["direction"] != nil {
		if rawDirection, exists := ri.Params["arrow_direction"]; exists {
			direction, ok := rawDirection.(string)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q arrow_direction must be a string", ri.Item.ID)
			}
			shape.ArrowDirection = direction
		} else if rawDirection, exists := ri.Params["direction"]; exists {
			direction, ok := rawDirection.(string)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q direction must be a string", ri.Item.ID)
			}
			shape.ArrowDirection = direction
		}
		if shapeType == "arrow" && shape.ArrowDirection != "up" && shape.ArrowDirection != "down" && shape.ArrowDirection != "left" && shape.ArrowDirection != "right" {
			return Layer{}, fmt.Errorf("overlay: item %q arrow_direction must be up, down, left or right", ri.Item.ID)
		}
		for _, item := range []struct {
			keys []string
			dst  *float64
			name string
		}{{[]string{"arrow_head_length", "head_length"}, &shape.ArrowHeadLength, "arrow_head_length"}, {[]string{"arrow_head_width", "head_width"}, &shape.ArrowHeadWidth, "arrow_head_width"}, {[]string{"arrow_shaft_width", "shaft_width"}, &shape.ArrowShaftWidth, "arrow_shaft_width"}} {
			if raw, ok := firstParam(ri.Params, item.keys...); ok {
				value, err := toFloat(raw)
				if err != nil || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
					return Layer{}, fmt.Errorf("overlay: item %q shape.%s must be positive and finite", ri.Item.ID, item.name)
				}
				*item.dst = value
			}
		}
	}

	chartRaw, chartExists := ri.Params["chart"]
	if shapeType == "chart" || chartExists {
		srcMap, ok := chartRaw.(map[string]any)
		if !ok && chartExists {
			return Layer{}, fmt.Errorf("overlay: item %q chart must be an object", ri.Item.ID)
		}
		if srcMap == nil {
			srcMap = ri.Params
		}
		for key := range srcMap {
			switch key {
			case "chart_type", "type", "data", "colors":
			default:
				return Layer{}, fmt.Errorf("overlay: item %q chart has unsupported property %q", ri.Item.ID, key)
			}
		}
		cType := "bar"
		if rawChartType, exists := srcMap["chart_type"]; exists {
			value, ok := rawChartType.(string)
			if !ok || value != "bar" && value != "line" && value != "pie" && value != "donut" {
				return Layer{}, fmt.Errorf("overlay: item %q chart_type is unsupported", ri.Item.ID)
			}
			cType = value
		} else if rawChartType, exists := srcMap["type"]; exists && rawChartType != "chart" {
			value, ok := rawChartType.(string)
			if !ok || value != "bar" && value != "line" && value != "pie" && value != "donut" {
				return Layer{}, fmt.Errorf("overlay: item %q chart type is unsupported", ri.Item.ID)
			}
			cType = value
		}
		var data []float64
		if rawData, exists := srcMap["data"]; exists {
			dRaw, ok := rawData.([]any)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q chart data must be an array", ri.Item.ID)
			}
			for _, value := range dRaw {
				f, err := toFloat(value)
				if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
					return Layer{}, fmt.Errorf("overlay: item %q chart data values must be finite numbers", ri.Item.ID)
				}
				data = append(data, f)
			}
		}
		var colors [][]float64
		if rawColors, exists := srcMap["colors"]; exists {
			colRaw, ok := rawColors.([]any)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q chart colors must be an array", ri.Item.ID)
			}
			for _, color := range colRaw {
				rgba, err := parseRGBA(color)
				if err != nil {
					return Layer{}, fmt.Errorf("overlay: item %q chart color is invalid: %w", ri.Item.ID, err)
				}
				colors = append(colors, rgba)
			}
		}
		if len(data) == 0 {
			return Layer{}, fmt.Errorf("overlay: item %q chart requires non-empty data", ri.Item.ID)
		}
		shape.Chart = &LayerChart{
			ChartType: cType,
			Data:      data,
			Colors:    colors,
		}
	}

	if pathValue, exists := ri.Params["path"]; exists {
		path, err := parsePath(pathValue, ri.Item.ID)
		if err != nil {
			return Layer{}, err
		}
		shape.Path = path
	}

	// 2. Fills and Gradients
	if fill, hasFill := ri.Params["fill"]; hasFill {
		if color, hasColor := ri.Params["color"]; hasColor && color != fill {
			return Layer{}, fmt.Errorf("overlay: item %q fill and color disagree", ri.Item.ID)
		}
	}
	if gradRaw, ok := firstParam(ri.Params, "fill_gradient", "gradient"); ok {
		grad, err := parseGradient(gradRaw, ri.Item.ID)
		if err != nil {
			return Layer{}, err
		}
		shape.Gradient = grad
		shape.Fill = grad
	} else if fillRaw, ok := firstParam(ri.Params, "fill", "color"); ok {
		col, err := parseRGBA(fillRaw)
		if err != nil {
			return Layer{}, fmt.Errorf("overlay: item %q shape.fill is invalid: %w", ri.Item.ID, err)
		}
		shape.Fill = col
	}

	// 3. Stroke
	if strokeValue, exists := ri.Params["stroke"]; exists {
		strokeRaw, ok := strokeValue.(map[string]any)
		if !ok {
			return Layer{}, fmt.Errorf("overlay: item %q shape.stroke must be an object", ri.Item.ID)
		}
		for key := range strokeRaw {
			if key != "color" && key != "width" {
				return Layer{}, fmt.Errorf("overlay: item %q shape.stroke has unsupported property %q", ri.Item.ID, key)
			}
		}
		colStr := "#FFFFFF"
		if cRaw, exists := strokeRaw["color"]; exists {
			value, ok := cRaw.(string)
			if !ok {
				return Layer{}, fmt.Errorf("overlay: item %q shape.stroke.color must be a hex color string", ri.Item.ID)
			}
			if _, err := parseHexColor(value); err != nil {
				return Layer{}, fmt.Errorf("overlay: item %q shape.stroke.color is invalid: %w", ri.Item.ID, err)
			}
			colStr = value
		}
		w := 1.0
		if wRaw, exists := strokeRaw["width"]; exists {
			wf, err := toFloat(wRaw)
			if err != nil || wf < 0 || math.IsNaN(wf) || math.IsInf(wf, 0) {
				return Layer{}, fmt.Errorf("overlay: item %q shape.stroke.width must be a non-negative finite number", ri.Item.ID)
			}
			w = wf
		}
		shape.Stroke = &LayerStroke{Color: colStr, Width: w}
	} else if shapeType == "grid" && shape.GridLineWidth > 0 {
		shape.Stroke = &LayerStroke{Color: "#FFFFFF", Width: shape.GridLineWidth}
	} else if shapeType == "arc" {
		sw := 2.0
		if shape.ArcThickness > 0 {
			sw = shape.ArcThickness
		}
		shape.Stroke = &LayerStroke{Color: "#FFFFFF", Width: sw}
	}

	if shapeType == "device_frame" && shape.Fill == nil {
		shape.Fill = []float64{0.12, 0.16, 0.23, 1.0}
	}
	if shapeType == "arc" {
		shape.Fill = nil
	}

	// 4. Effects
	var effects []LayerEffect
	if effRaw, ok := ri.Params["effects"]; ok {
		effs, err := resolveLayerEffects(effRaw, ri.Item.ID)
		if err != nil {
			return Layer{}, err
		}
		effects = effs
	}

	layer := Layer{
		ID:             ri.Item.ID,
		Type:           "shape",
		Size:           []float64{width, height},
		Position:       pos,
		StartFrame:     ri.Start,
		DurationFrames: ri.End - ri.Start,
		Shape:          shape,
		Effects:        effects,
	}

	if err := validateShapeContract(shape, width, height, ri.Item.ID); err != nil {
		return Layer{}, err
	}
	if opRaw, ok := ri.Params["opacity"]; ok {
		op, err := toFloat(opRaw)
		if err != nil || op < 0 || op > 1 || math.IsNaN(op) || math.IsInf(op, 0) {
			return Layer{}, fmt.Errorf("overlay: item %q shape.opacity must be finite and in [0, 1]", ri.Item.ID)
		}
		layer.Opacity = &op
	}

	return layer, nil
}
