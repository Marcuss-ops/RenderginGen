package overlay

import (
	"fmt"
	"strconv"
	"strings"
)

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
	m, err := parseGradientObject(raw, itemID)
	if err != nil {
		return nil, err
	}
	gradType, err := parseGradientType(m, itemID)
	if err != nil {
		return nil, err
	}
	if _, hasStops := m["stops"]; hasStops {
		if _, hasColorStops := m["color_stops"]; hasColorStops {
			return nil, fmt.Errorf("overlay: item %q gradient cannot declare both stops and color_stops", itemID)
		}
	}
	start, end, err := parseGradientEndpoints(m, itemID)
	if err != nil {
		return nil, err
	}
	colorStops, stopsRaw, err := parseGradientColorStops(m, itemID)
	if err != nil {
		return nil, err
	}
	opacityStops, err := parseGradientOpacityStops(m, itemID)
	if err != nil {
		return nil, err
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

// parseGradientObject admits only the gradient vocabulary. Keeping the key
// check separate from lowering makes an unsupported property fail before any
// value is interpreted, which is the historical error precedence.
func parseGradientObject(raw any, itemID string) (map[string]any, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("overlay: item %q gradient must be an object", itemID)
	}
	for key := range m {
		switch key {
		case "type", "stops", "color_stops", "opacity_stops", "start", "end":
		default:
			return nil, fmt.Errorf("overlay: item %q gradient has unsupported property %q", itemID, key)
		}
	}
	return m, nil
}

func parseGradientType(m map[string]any, itemID string) (string, error) {
	rawType, exists := m["type"]
	if !exists {
		return "linear", nil
	}
	t, ok := rawType.(string)
	if !ok || (t != "linear" && t != "radial" && t != "conic") {
		return "", fmt.Errorf("overlay: item %q unsupported gradient type %v", itemID, rawType)
	}
	return t, nil
}

func parseGradientEndpoints(m map[string]any, itemID string) ([]float64, []float64, error) {
	start := []float64{0, 0}
	if raw, ok := m["start"]; ok {
		parsed, err := parse2Floats(raw)
		if err != nil || !finite(parsed[0]) || !finite(parsed[1]) {
			return nil, nil, fmt.Errorf("overlay: item %q gradient start must contain two finite numbers", itemID)
		}
		start = parsed
	}
	end := []float64{0, 1}
	if raw, ok := m["end"]; ok {
		parsed, err := parse2Floats(raw)
		if err != nil || !finite(parsed[0]) || !finite(parsed[1]) {
			return nil, nil, fmt.Errorf("overlay: item %q gradient end must contain two finite numbers", itemID)
		}
		end = parsed
	}
	return start, end, nil
}

// parseGradientColorStops owns the two accepted stop dialects: the legacy
// [color, position] tuples in "stops" and the object form in "color_stops".
// They are mutually exclusive and produce the same LayerGradient projection.
func parseGradientColorStops(m map[string]any, itemID string) ([]LayerGradientStop, [][2]any, error) {
	if rawStops, exists := m["stops"]; exists {
		return parseGradientTupleStops(rawStops, itemID)
	}
	if rawStops, exists := m["color_stops"]; exists {
		return parseGradientObjectStops(rawStops, itemID)
	}
	return nil, nil, nil
}

func parseGradientTupleStops(rawStops any, itemID string) ([]LayerGradientStop, [][2]any, error) {
	stopsList, ok := rawStops.([]any)
	if !ok || len(stopsList) < 2 {
		return nil, nil, fmt.Errorf("overlay: item %q gradient stops must contain at least two entries", itemID)
	}
	colorStops := make([]LayerGradientStop, 0, len(stopsList))
	stopsRaw := make([][2]any, 0, len(stopsList))
	for _, stopItem := range stopsList {
		pair, ok := stopItem.([]any)
		if !ok || len(pair) != 2 {
			return nil, nil, fmt.Errorf("overlay: item %q gradient stop must be [color, position]", itemID)
		}
		col, err := parseRGBA(pair[0])
		if err != nil {
			return nil, nil, fmt.Errorf("overlay: item %q gradient stop color invalid: %w", itemID, err)
		}
		pos, err := toFloat(pair[1])
		if err != nil || pos < 0 || pos > 1 || !finite(pos) {
			return nil, nil, fmt.Errorf("overlay: item %q gradient stop position %v must be in [0, 1]", itemID, pair[1])
		}
		colorStops = append(colorStops, LayerGradientStop{Position: pos, Color: col})
		stopsRaw = append(stopsRaw, [2]any{pair[0], pos})
	}
	return colorStops, stopsRaw, nil
}

func parseGradientObjectStops(rawStops any, itemID string) ([]LayerGradientStop, [][2]any, error) {
	cStops, ok := rawStops.([]any)
	if !ok || len(cStops) < 2 {
		return nil, nil, fmt.Errorf("overlay: item %q gradient color_stops must contain at least two entries", itemID)
	}
	colorStops := make([]LayerGradientStop, 0, len(cStops))
	stopsRaw := make([][2]any, 0, len(cStops))
	for _, stopItem := range cStops {
		sm, ok := stopItem.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("overlay: item %q color_stop must be an object", itemID)
		}
		for key := range sm {
			if key != "color" && key != "position" {
				return nil, nil, fmt.Errorf("overlay: item %q color_stop has unsupported property %q", itemID, key)
			}
		}
		colorRaw, hasColor := sm["color"]
		positionRaw, hasPosition := sm["position"]
		if !hasColor || !hasPosition {
			return nil, nil, fmt.Errorf("overlay: item %q color_stop requires color and position", itemID)
		}
		col, err := parseRGBA(colorRaw)
		if err != nil {
			return nil, nil, fmt.Errorf("overlay: item %q gradient stop color invalid: %w", itemID, err)
		}
		pos, err := toFloat(positionRaw)
		if err != nil || pos < 0 || pos > 1 || !finite(pos) {
			return nil, nil, fmt.Errorf("overlay: item %q gradient stop position %v must be in [0, 1]", itemID, positionRaw)
		}
		colorStops = append(colorStops, LayerGradientStop{Position: pos, Color: col})
		stopsRaw = append(stopsRaw, [2]any{colorRaw, pos})
	}
	return colorStops, stopsRaw, nil
}

func parseGradientOpacityStops(m map[string]any, itemID string) ([]LayerOpacityStop, error) {
	rawStops, exists := m["opacity_stops"]
	if !exists {
		return nil, nil
	}
	oStops, ok := rawStops.([]any)
	if !ok || len(oStops) < 2 {
		return nil, fmt.Errorf("overlay: item %q gradient opacity_stops must contain at least two entries", itemID)
	}
	opacityStops := make([]LayerOpacityStop, 0, len(oStops))
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
	return opacityStops, nil
}
