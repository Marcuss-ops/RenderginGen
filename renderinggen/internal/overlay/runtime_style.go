package overlay

import (
	"fmt"
	"math"
)

const (
	maxRuntimeGlowSize   = 256.0
	maxRuntimeStrokeSize = 64.0
)

// validateTextRuntimeOverrides accepts only controls the compiler can lower
// deterministically. font_family names a bundled family, while glow_size and
// stroke_size are pixel dimensions. Zero explicitly disables that effect.
func validateTextRuntimeOverrides(params map[string]any, itemID string, kind ItemKind, presetID string, hasImage bool) error {
	if isImageKind(kind) || isVideoKind(kind) || (isEntityKind(kind) && hasImage) {
		for _, key := range []string{"font_family", "glow_size", "stroke_size"} {
			if _, exists := params[key]; exists {
				return fmt.Errorf("overlay: item %q cannot apply text runtime override %q to a non-text layer", itemID, key)
			}
		}
		return nil
	}
	for _, key := range []string{"font_family", "glow_size", "stroke_size"} {
		value, exists := params[key]
		if !exists {
			continue
		}
		if presetID == "" {
			return fmt.Errorf("overlay: item %q runtime override %q requires a text preset", itemID, key)
		}
		switch key {
		case "font_family":
			family, ok := value.(string)
			if !ok {
				return fmt.Errorf("overlay: item %q params.font_family must be a string family id", itemID)
			}
			if _, ok := runtimeFontPath(family); !ok {
				return fmt.Errorf("overlay: item %q params.font_family %q is unsupported (supported: poppins, inter, dejavu_sans)", itemID, family)
			}
		case "glow_size":
			if _, err := boundedRuntimeSize(value, key, maxRuntimeGlowSize); err != nil {
				return fmt.Errorf("overlay: item %q %w", itemID, err)
			}
		case "stroke_size":
			if _, err := boundedRuntimeSize(value, key, maxRuntimeStrokeSize); err != nil {
				return fmt.Errorf("overlay: item %q %w", itemID, err)
			}
		}
	}
	return nil
}

func boundedRuntimeSize(value any, key string, maximum float64) (float64, error) {
	size, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("params.%s must be a number", key)
	}
	if math.IsNaN(size) || math.IsInf(size, 0) || size < 0 || size > maximum {
		return 0, fmt.Errorf("params.%s must be between 0 and %g pixels", key, maximum)
	}
	return size, nil
}

func applyTextRuntimeOverrides(layer *Layer, params map[string]any) error {
	if len(params) == 0 {
		return nil
	}
	fontPath := ""
	if value, exists := params["font_family"]; exists {
		family, ok := value.(string)
		if !ok {
			return fmt.Errorf("params.font_family must be a string family id")
		}
		var supported bool
		fontPath, supported = runtimeFontPath(family)
		if !supported {
			return fmt.Errorf("unsupported font family %q", family)
		}
	}
	hasFont := fontPath != ""
	_, hasGlow := params["glow_size"]
	_, hasStroke := params["stroke_size"]
	if !hasFont && !hasGlow && !hasStroke {
		return nil
	}
	glowSize, hasGlow := params["glow_size"].(float64)
	strokeSize, hasStroke := params["stroke_size"].(float64)
	if layer.Style == nil {
		return fmt.Errorf("text runtime overrides require a preset style")
	}
	if hasFont {
		layer.Style.Font = fontPath
	}
	if hasGlow {
		if glowSize == 0 {
			layer.Style.Glow = nil
		} else {
			glow := LayerGlow{Radius: glowSize, Intensity: 0.25, Color: "#FFFFFF"}
			if layer.Style.Glow != nil {
				glow.Intensity = layer.Style.Glow.Intensity
				glow.Color = layer.Style.Glow.Color
			}
			layer.Style.Glow = &glow
		}
	}
	if hasStroke {
		if strokeSize == 0 {
			layer.Style.Stroke = nil
		} else {
			color := "#111827"
			if layer.Style.Stroke != nil && layer.Style.Stroke.Color != "" {
				color = layer.Style.Stroke.Color
			}
			layer.Style.Stroke = &LayerStroke{Color: color, Width: strokeSize}
		}
	}
	return nil
}
