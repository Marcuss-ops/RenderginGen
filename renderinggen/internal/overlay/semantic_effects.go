package overlay

import (
	"fmt"
	"math"
	"strings"
)

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
