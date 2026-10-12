package overlay

import (
	"fmt"
	"math"
	"strings"
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
func validateChildCaptionStyles(item semanticItem) error {
	for _, child := range item.ImageLayers {
		if strings.TrimSpace(child.Caption) == "" {
			continue
		}
		if strings.TrimSpace(child.CaptionFontFamily) != "" {
			if _, ok := runtimeFontPath(strings.TrimSpace(child.CaptionFontFamily)); !ok {
				return fmt.Errorf("overlay: item %q image layer %q caption_font_family %q is unsupported (supported: %s)", item.ID, child.ID, child.CaptionFontFamily, strings.Join(runtimeFontFamilies(), ", "))
			}
		}
		if color := strings.TrimSpace(child.CaptionColor); color != "" {
			if _, err := parseHexColor(color); err != nil {
				return fmt.Errorf("overlay: item %q image layer %q caption_color: %w", item.ID, child.ID, err)
			}
		}
	}
	return nil
}

func itemHasCaption(item semanticItem) bool {
	if strings.TrimSpace(item.EntityCaption) != "" {
		return true
	}
	for _, child := range item.ImageLayers {
		if strings.TrimSpace(child.Caption) != "" {
			return true
		}
	}
	return false
}

func validateTextRuntimeOverrides(params map[string]any, itemID string, kind ItemKind, presetID string, hasImage, hasEntityCaption bool) error {
	captionedEntityImage := (isEntityKind(kind) || isImageKind(kind)) && hasImage && hasEntityCaption
	if isShapeKind(kind) || isImageKind(kind) || isVideoKind(kind) || (isEntityKind(kind) && hasImage) {
		imageKeys := make(map[string]bool, len(runtimeImageStyleKeys))
		for _, key := range runtimeImageStyleKeys {
			imageKeys[key] = true
		}
		for _, key := range runtimeTextStyleKeys {
			if imageKeys[key] {
				// Shared name (shadow_opacity): the image validator owns it
				// on media layers, so it is not a text-layer trespass.
				continue
			}
			value, exists := params[key]
			if !exists {
				continue
			}
			// A captioned entity image carries the payload's text style for
			// its caption layer: every runtime text key is legal here and
			// validated with the same rules as a text layer, so the
			// payload's font, glow, stroke, size and shadow reach the
			// caption instead of falling back to preset constants.
			if captionedEntityImage {
				if err := validateRuntimeTextStyleKey(key, value, itemID); err != nil {
					return err
				}
				continue
			}
			if exists {
				return fmt.Errorf("overlay: item %q cannot apply text runtime override %q to a non-text layer", itemID, key)
			}
		}
		if err := validateImageRuntimeOverrides(params, itemID); err != nil {
			return err
		}
		return nil
	}
	// A semantic map lowers to one or more text layers (pin labels and
	// attribution) alongside its basemap. Its run-level text style is therefore
	// valid even though the map itself has no text preset; map roles provide
	// their own geometry and fallback styling.
	if isMapKind(kind) {
		for _, key := range runtimeTextStyleKeys {
			if value, exists := params[key]; exists {
				if err := validateRuntimeTextStyleKey(key, value, itemID); err != nil {
					return err
				}
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
		if err := validateRuntimeTextStyleKey(key, value, itemID); err != nil {
			return err
		}
	}
	return nil
}

// validateRuntimeTextStyleKey is the single validator for one runtime text
// override value. Text layers and captioned entity images share it so the
// payload's font, glow, stroke, size and shadow obey one contract wherever
// a caption can render them.
func validateRuntimeTextStyleKey(key string, value any, itemID string) error {
	switch key {
	case "font_family":
		family, ok := value.(string)
		if !ok {
			return fmt.Errorf("overlay: item %q params.font_family must be a string family id", itemID)
		}
		if _, ok := runtimeFontPath(family); !ok {
			return fmt.Errorf("overlay: item %q params.font_family %q is unsupported (supported: %s)", itemID, family, strings.Join(runtimeFontFamilies(), ", "))
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
	case "cursor_color":
		color, ok := value.(string)
		if !ok || !isHexColor(color) {
			return fmt.Errorf("overlay: item %q params.cursor_color must be #RRGGBB", itemID)
		}
	case "cursor_width_px":
		if _, err := boundedRuntimeSize(value, key, maxRuntimeStrokeSize); err != nil {
			return fmt.Errorf("overlay: item %q %w", itemID, err)
		}
	case "cursor_height_px":
		if _, err := boundedRuntimeSize(value, key, maxRuntimeShadowSize); err != nil {
			return fmt.Errorf("overlay: item %q %w", itemID, err)
		}
	case "cursor_blink":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("overlay: item %q params.cursor_blink must be a boolean", itemID)
		}
	}
	return nil
}

var runtimeTextStyleKeys = []string{
	"font_family", "font_size_px", "glow_size", "stroke_size",
	"shadow_blur_px", "shadow_opacity", "shadow_offset_x_px", "shadow_offset_y_px",
	"cursor_color", "cursor_width_px", "cursor_height_px", "cursor_blink",
}

// runtimeImageStyleKeys are the flat image-card overrides PipelineGen emits
// (overlay_style.image.* trimmed of its image_ prefix). Geometry and fit ride
// the layer directly; border/stroke/shadow ride the card Frame, with flat
// keys acting as overrides when no Frame block is present.
var runtimeImageStyleKeys = []string{
	"width", "height", "position_x", "position_y", "radius",
	"bounded", "fit",
	"border_width", "border_color", "border_radius",
	"stroke_width", "stroke_color",
	"shadow_enabled", "shadow_color", "shadow_opacity", "shadow_blur",
	"shadow_offset_x", "shadow_offset_y",
}

func validateImageRuntimeOverrides(params map[string]any, itemID string) error {
	for _, key := range runtimeImageStyleKeys {
		value, exists := params[key]
		if !exists {
			continue
		}
		if err := validateRuntimeImageStyleKey(key, value, itemID); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeImageStyleKey(key string, value any, itemID string) error {
	switch key {
	case "width", "height":
		n, ok := numericValue(value)
		if !ok || n <= 0 || n > 8192 {
			return fmt.Errorf("overlay: item %q params.%s must be between 1 and 8192 pixels", itemID, key)
		}
	case "position_x", "position_y":
		n, ok := numericValue(value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 8192 {
			return fmt.Errorf("overlay: item %q params.%s must be within ±8192 pixels", itemID, key)
		}
	case "radius", "border_radius":
		n, ok := numericValue(value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > maxFrameRadius {
			return fmt.Errorf("overlay: item %q params.%s must be within [0,%g]", itemID, key, maxFrameRadius)
		}
	case "border_width":
		n, ok := numericValue(value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > maxFrameBorderWidth {
			return fmt.Errorf("overlay: item %q params.%s must be within [0,%g]", itemID, key, maxFrameBorderWidth)
		}
	case "stroke_width":
		n, ok := numericValue(value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > maxFrameStrokeWidth {
			return fmt.Errorf("overlay: item %q params.%s must be within [0,%g]", itemID, key, maxFrameStrokeWidth)
		}
	case "border_color", "stroke_color", "shadow_color":
		color, ok := value.(string)
		if !ok || !isHexColor(color) {
			return fmt.Errorf("overlay: item %q params.%s must be #RRGGBB", itemID, key)
		}
	case "bounded", "shadow_enabled":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("overlay: item %q params.%s must be a boolean", itemID, key)
		}
	case "fit":
		fit, ok := value.(string)
		if !ok || (fit != "cover" && fit != "contain" && fit != "fill") {
			return fmt.Errorf("overlay: item %q params.fit must be cover, contain or fill", itemID)
		}
	case "shadow_opacity":
		n, ok := numericValue(value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1 {
			return fmt.Errorf("overlay: item %q params.shadow_opacity must be within [0,1]", itemID)
		}
	case "shadow_blur":
		n, ok := numericValue(value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > maxFrameShadowBlur {
			return fmt.Errorf("overlay: item %q params.shadow_blur must be within [0,%g]", itemID, maxFrameShadowBlur)
		}
	case "shadow_offset_x", "shadow_offset_y":
		n, ok := numericValue(value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > maxFrameShadowOffset {
			return fmt.Errorf("overlay: item %q params.%s must be within ±%g", itemID, key, maxFrameShadowOffset)
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
		if layer.Style.FitMode != "" && layer.Style.FitMode != "none" {
			// Keep shrink-only fitting bounded by the runtime-authored size,
			// and keep the interval valid even when the override is smaller
			// than the preset's normal minimum.
			layer.Style.MaxFontSize = fontSize
			if layer.Style.MinFontSize > fontSize {
				layer.Style.MinFontSize = fontSize
			}
		}
	}
	if hasGlow {
		if glowSize == 0 {
			layer.Style.Glow = nil
		} else {
			// A supplied glow_size is an affirmative visual request. The preset's
			// restrained .25 intensity is nearly invisible on video, so runtime
			// overrides get a clearly legible halo unless the payload grows an
			// explicit intensity control.
			// Match the Chronon Short Phrases treatment: a restrained white halo
			// around the white face, kept crisp by the dark stroke.
			glow := LayerGlow{Radius: glowSize, Intensity: 0.4, Color: "#FFFFFF"}
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
