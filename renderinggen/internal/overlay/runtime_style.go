package overlay

import (
	"fmt"
	"math"
)

const (
	maxRuntimeGlowSize   = 256.0
	maxRuntimeStrokeSize = 64.0
	maxRuntimeFontSize   = 512.0
	maxRuntimeShadowSize = 256.0
)

// validateTextRuntimeOverrides accepts only controls the compiler can lower
// deterministically. font_family names a bundled family, while glow_size and
// stroke_size are pixel dimensions. Zero explicitly disables that effect.
func validateTextRuntimeOverrides(params map[string]any, itemID string, kind ItemKind, presetID string, hasImage bool) error {
	if isImageKind(kind) || isVideoKind(kind) || (isEntityKind(kind) && hasImage) {
		for _, key := range runtimeTextStyleKeys {
			if _, exists := params[key]; exists {
				return fmt.Errorf("overlay: item %q cannot apply text runtime override %q to a non-text layer", itemID, key)
			}
		}
		return nil
	}
	for _, key := range runtimeTextStyleKeys {
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
		case "font_size_px":
			if n, ok := value.(float64); !ok || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n > maxRuntimeFontSize {
				return fmt.Errorf("overlay: item %q params.font_size_px must be greater than 0 and at most %g pixels", itemID, maxRuntimeFontSize)
			}
		case "shadow_blur_px", "shadow_offset_x_px", "shadow_offset_y_px":
			if key == "shadow_blur_px" {
				if _, err := boundedRuntimeSize(value, key, maxRuntimeShadowSize); err != nil {
					return fmt.Errorf("overlay: item %q %w", itemID, err)
				}
			} else if n, ok := value.(float64); !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > maxRuntimeShadowSize {
				return fmt.Errorf("overlay: item %q params.%s must be between -%g and %g pixels", itemID, key, maxRuntimeShadowSize, maxRuntimeShadowSize)
			}
		case "shadow_opacity":
			if _, err := boundedRuntimeSize(value, key, 1); err != nil {
				return fmt.Errorf("overlay: item %q %w", itemID, err)
			}
		}
	}
	return nil
}

var runtimeTextStyleKeys = []string{
	"font_family", "font_size_px", "glow_size", "stroke_size",
	"shadow_blur_px", "shadow_opacity", "shadow_offset_x_px", "shadow_offset_y_px",
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

// runtimeNumber reads one runtime control out of the merged override map.
//
// PRESENCE decides whether a control applies, never the outcome of a type
// assertion: the second return value of a bare `value.(float64)` was used as the
// "has this control" flag, so a control that was present but not a number
// vanished from the plan with no error at all — the layer rendered with the
// preset's own styling and nothing said why. Every control this function reads
// is therefore either a finite number or a build-time error.
func runtimeNumber(params map[string]any, key string) (float64, bool, error) {
	value, exists := params[key]
	if !exists {
		return 0, false, nil
	}
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, true, fmt.Errorf("params.%s must be a finite number", key)
	}
	return number, true, nil
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
	glowSize, hasGlow, err := runtimeNumber(params, "glow_size")
	if err != nil {
		return err
	}
	strokeSize, hasStroke, err := runtimeNumber(params, "stroke_size")
	if err != nil {
		return err
	}
	fontSize, hasFontSize, err := runtimeNumber(params, "font_size_px")
	if err != nil {
		return err
	}
	shadowBlur, hasShadowBlur, err := runtimeNumber(params, "shadow_blur_px")
	if err != nil {
		return err
	}
	shadowOpacity, hasShadowOpacity, err := runtimeNumber(params, "shadow_opacity")
	if err != nil {
		return err
	}
	shadowOffsetX, hasShadowOffsetX, err := runtimeNumber(params, "shadow_offset_x_px")
	if err != nil {
		return err
	}
	shadowOffsetY, hasShadowOffsetY, err := runtimeNumber(params, "shadow_offset_y_px")
	if err != nil {
		return err
	}
	hasShadow := hasShadowBlur || hasShadowOpacity || hasShadowOffsetX || hasShadowOffsetY
	if !hasFont && !hasGlow && !hasStroke && !hasFontSize && !hasShadow {
		return nil
	}
	if layer.Style == nil {
		return fmt.Errorf("text runtime overrides require a preset style")
	}
	if hasFont {
		layer.Style.Font = fontPath
	}
	if hasFontSize {
		layer.Style.FontSize = fontSize
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
	if hasShadow {
		shadow := LayerShadow{Color: "#000000", Opacity: 0.8, Blur: 8, Offset: []float64{0, 4}}
		if layer.Style.Shadow != nil {
			shadow = *layer.Style.Shadow
			shadow.Offset = append([]float64(nil), layer.Style.Shadow.Offset...)
		}
		if hasShadowBlur {
			shadow.Blur = shadowBlur
		}
		if hasShadowOpacity {
			shadow.Opacity = shadowOpacity
		}
		if hasShadowOffsetX {
			if len(shadow.Offset) < 2 {
				shadow.Offset = []float64{0, 0}
			}
			shadow.Offset[0] = shadowOffsetX
		}
		if hasShadowOffsetY {
			if len(shadow.Offset) < 2 {
				shadow.Offset = []float64{0, 0}
			}
			shadow.Offset[1] = shadowOffsetY
		}
		// Zero opacity is the documented "disable" switch, the same way a zero
		// glow/stroke size disables those effects. A shadow at zero opacity
		// renders nothing, so keeping the block in the plan would only mislead
		// the next reader of it.
		if shadow.Opacity == 0 {
			layer.Style.Shadow = nil
		} else {
			layer.Style.Shadow = &shadow
		}
	}
	return nil
}
