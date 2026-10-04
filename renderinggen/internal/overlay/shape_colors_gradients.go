package overlay

import (
	"fmt"
	"math"
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
