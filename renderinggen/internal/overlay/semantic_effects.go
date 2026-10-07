package overlay

import (
	"fmt"
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
			return fmt.Errorf("overlay: item %q %s %s must be in [%g, %g]", itemID, effect, key, bounds.minimum, bounds.maximum)
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
		if effect.Type != prefix {
			continue
		}
		if err := validateEffectNumber(params, name, effect.Type, itemID, limits); err != nil {
			return err
		}
	}
	return nil
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
			if err != nil {
				return nil, fmt.Errorf("overlay: item %q effect %s intensity must be a number: %w", itemID, effType, err)
			}
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

		effect := LayerEffect{Type: effType, Params: params}
		if err := validateEffectParams(effect, itemID); err != nil {
			return nil, err
		}
		out = append(out, effect)
	}
	return out, nil
}
