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
	// Cursor controls are recognized only so they can fail closed on every item
	// kind. Chronon has no caret primitive, so accepting them here would validate
	// a control the renderer cannot draw.
	for _, key := range runtimeUnsupportedTextStyleKeys {
		if _, exists := params[key]; exists {
			return fmt.Errorf("overlay: item %q params.%s is not supported by the Chronon text lowering; remove cursor controls", itemID, key)
		}
	}
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
			if key == "fill" && isShapeKind(kind) {
				// Shape layers own `fill` natively (rect/bar color as an
				// RGBA array or hex string, validated by the shape
				// compiler via parseRGBA). It is geometry, not a text
				// face-color override, so the text trespass gate must not
				// reject producer chart bars carrying it.
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

// runtimeTextStyle is the typed, validated form of the item-local text
// controls. The decoder below is the only place that reads these keys: the
// validator and the lowering must not maintain separate switches, because a
// key added to one and forgotten in the other becomes a silent no-op.
type runtimeTextStyle struct {
	FontPath        string
	FontSizePX      *float64
	Fill            *string
	GlowSize        *float64
	GlowColor       *string
	StrokeSize      *float64
	ShadowBlurPX    *float64
	ShadowOpacity   *float64
	ShadowOffsetXPX *float64
	ShadowOffsetYPX *float64
}

func (s runtimeTextStyle) empty() bool {
	return s.FontPath == "" && s.FontSizePX == nil && s.Fill == nil &&
		s.GlowSize == nil && s.GlowColor == nil && s.StrokeSize == nil &&
		s.ShadowBlurPX == nil && s.ShadowOpacity == nil &&
		s.ShadowOffsetXPX == nil && s.ShadowOffsetYPX == nil
}

// decodeRuntimeTextStyle parses the controls once, for validation and lowering.
// Any present but malformed value is an error, never a skipped key.
func decodeRuntimeTextStyle(params map[string]any) (runtimeTextStyle, error) {
	var style runtimeTextStyle
	if len(params) == 0 {
		return style, nil
	}
	// Chronon has no caret primitive and the overlay schema's cursor controls
	// have no lowering. Accepting them here would validate a control that never
	// reaches the renderer; fail closed until the renderer grows the primitive.
	for _, key := range runtimeUnsupportedTextStyleKeys {
		if _, exists := params[key]; exists {
			return runtimeTextStyle{}, fmt.Errorf("params.%s is not supported by the Chronon text lowering; remove cursor controls", key)
		}
	}
	if value, exists := params["font_family"]; exists {
		family, ok := value.(string)
		if !ok {
			return runtimeTextStyle{}, fmt.Errorf("params.font_family must be a string family id")
		}
		fontPath, ok := runtimeFontPath(family)
		if !ok {
			return runtimeTextStyle{}, fmt.Errorf("params.font_family %q is unsupported (supported: %s)", family, strings.Join(runtimeFontFamilies(), ", "))
		}
		style.FontPath = fontPath
	}
	if value, exists := params["font_size_px"]; exists {
		n, ok := value.(float64)
		if !ok || !finite(n) || n <= 0 || n > maxRuntimeFontSize {
			return runtimeTextStyle{}, fmt.Errorf("params.font_size_px must be greater than 0 and at most %g pixels", maxRuntimeFontSize)
		}
		style.FontSizePX = &n
	}
	if value, exists := params["fill"]; exists {
		color, err := runtimeColor(value, "fill")
		if err != nil {
			return runtimeTextStyle{}, err
		}
		style.Fill = &color
	}
	if value, exists := params["glow_color"]; exists {
		color, err := runtimeColor(value, "glow_color")
		if err != nil {
			return runtimeTextStyle{}, err
		}
		style.GlowColor = &color
	}
	var err error
	if style.GlowSize, err = runtimeNumberPointer(params, "glow_size", maxRuntimeGlowSize, "pixels"); err != nil {
		return runtimeTextStyle{}, err
	}
	if style.StrokeSize, err = runtimeNumberPointer(params, "stroke_size", maxRuntimeStrokeSize, "pixels"); err != nil {
		return runtimeTextStyle{}, err
	}
	if style.ShadowBlurPX, err = runtimeNumberPointer(params, "shadow_blur_px", maxRuntimeShadowSize, "pixels"); err != nil {
		return runtimeTextStyle{}, err
	}
	if style.ShadowOpacity, err = runtimeNumberPointer(params, "shadow_opacity", 1, ""); err != nil {
		return runtimeTextStyle{}, err
	}
	if style.ShadowOffsetXPX, err = runtimeSignedNumber(params, "shadow_offset_x_px", maxRuntimeShadowSize); err != nil {
		return runtimeTextStyle{}, err
	}
	if style.ShadowOffsetYPX, err = runtimeSignedNumber(params, "shadow_offset_y_px", maxRuntimeShadowSize); err != nil {
		return runtimeTextStyle{}, err
	}
	return style, nil
}

func runtimeColor(value any, key string) (string, error) {
	color, ok := value.(string)
	if !ok || !isHexColor(color) {
		return "", fmt.Errorf("params.%s must be #RRGGBB", key)
	}
	return color, nil
}

func runtimeNumberPointer(params map[string]any, key string, maximum float64, unit string) (*float64, error) {
	value, exists := params[key]
	if !exists {
		return nil, nil
	}
	number, err := boundedRuntimeNumber(value, key, maximum, unit)
	if err != nil {
		return nil, err
	}
	return &number, nil
}

func runtimeSignedNumber(params map[string]any, key string, maximum float64) (*float64, error) {
	value, exists := params[key]
	if !exists {
		return nil, nil
	}
	number, ok := value.(float64)
	if !ok || !finite(number) || math.Abs(number) > maximum {
		return nil, fmt.Errorf("params.%s must be between -%g and %g pixels", key, maximum, maximum)
	}
	return &number, nil
}

// validateRuntimeTextStyleKey validates one key through the same decoder the
// lowering uses. Text layers and captioned entity images share it.
func validateRuntimeTextStyleKey(key string, value any, itemID string) error {
	if _, err := decodeRuntimeTextStyle(map[string]any{key: value}); err != nil {
		return fmt.Errorf("overlay: item %q %w", itemID, err)
	}
	return nil
}

var runtimeTextStyleKeys = []string{
	"font_family", "font_size_px", "fill", "glow_size", "glow_color", "stroke_size",
	"shadow_blur_px", "shadow_opacity", "shadow_offset_x_px", "shadow_offset_y_px",
}

// runtimeUnsupportedTextStyleKeys are recognized only to reject them. They used
// to be advertised by the overlay schema and silently ignored by the lowering;
// Chronon has no caret primitive, so the contract no longer carries them.
var runtimeUnsupportedTextStyleKeys = []string{
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
		if !ok || !finite(n) || math.Abs(n) > 8192 {
			return fmt.Errorf("overlay: item %q params.%s must be within ±8192 pixels", itemID, key)
		}
	case "radius", "border_radius":
		n, ok := numericValue(value)
		if !ok || !finite(n) || n < 0 || n > maxFrameRadius {
			return fmt.Errorf("overlay: item %q params.%s must be within [0,%g]", itemID, key, maxFrameRadius)
		}
	case "border_width":
		n, ok := numericValue(value)
		if !ok || !finite(n) || n < 0 || n > maxFrameBorderWidth {
			return fmt.Errorf("overlay: item %q params.%s must be within [0,%g]", itemID, key, maxFrameBorderWidth)
		}
	case "stroke_width":
		n, ok := numericValue(value)
		if !ok || !finite(n) || n < 0 || n > maxFrameStrokeWidth {
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
		if !ok || !finite(n) || n < 0 || n > 1 {
			return fmt.Errorf("overlay: item %q params.shadow_opacity must be within [0,1]", itemID)
		}
	case "shadow_blur":
		n, ok := numericValue(value)
		if !ok || !finite(n) || n < 0 || n > maxFrameShadowBlur {
			return fmt.Errorf("overlay: item %q params.shadow_blur must be within [0,%g]", itemID, maxFrameShadowBlur)
		}
	case "shadow_offset_x", "shadow_offset_y":
		n, ok := numericValue(value)
		if !ok || !finite(n) || math.Abs(n) > maxFrameShadowOffset {
			return fmt.Errorf("overlay: item %q params.%s must be within ±%g", itemID, key, maxFrameShadowOffset)
		}
	}
	return nil
}

func boundedRuntimeNumber(value any, key string, maximum float64, unit string) (float64, error) {
	number, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("params.%s must be a number", key)
	}
	if !finite(number) || number < 0 || number > maximum {
		if unit == "" {
			return 0, fmt.Errorf("params.%s must be between 0 and %g", key, maximum)
		}
		return 0, fmt.Errorf("params.%s must be between 0 and %g %s", key, maximum, unit)
	}
	return number, nil
}

func applyTextRuntimeOverrides(layer *Layer, params map[string]any) error {
	style, err := decodeRuntimeTextStyle(params)
	if err != nil {
		return err
	}
	return applyDecodedTextRuntimeOverrides(layer, style)
}

func applyDecodedTextRuntimeOverrides(layer *Layer, style runtimeTextStyle) error {
	if style.empty() {
		return nil
	}
	if layer.Style == nil {
		return fmt.Errorf("text runtime overrides require a preset style")
	}
	if style.FontPath != "" {
		layer.Style.Font = style.FontPath
	}
	if style.Fill != nil {
		layer.Style.Fill = *style.Fill
	}
	if style.FontSizePX != nil {
		fontSize := *style.FontSizePX
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
	if style.GlowSize != nil {
		glowSize := *style.GlowSize
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
	if style.GlowColor != nil && layer.Style.Glow != nil {
		glow := *layer.Style.Glow
		glow.Color = *style.GlowColor
		layer.Style.Glow = &glow
	}
	if style.StrokeSize != nil {
		strokeSize := *style.StrokeSize
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
	hasShadow := style.ShadowBlurPX != nil || style.ShadowOpacity != nil ||
		style.ShadowOffsetXPX != nil || style.ShadowOffsetYPX != nil
	if hasShadow {
		shadow := LayerShadow{Color: "#000000", Opacity: 0.8, Blur: 8, Offset: []float64{0, 4}}
		if layer.Style.Shadow != nil {
			shadow = *layer.Style.Shadow
			shadow.Offset = append([]float64(nil), layer.Style.Shadow.Offset...)
		}
		if style.ShadowBlurPX != nil {
			shadow.Blur = *style.ShadowBlurPX
		}
		if style.ShadowOpacity != nil {
			shadow.Opacity = *style.ShadowOpacity
		}
		if style.ShadowOffsetXPX != nil {
			if len(shadow.Offset) < 2 {
				shadow.Offset = []float64{0, 0}
			}
			shadow.Offset[0] = *style.ShadowOffsetXPX
		}
		if style.ShadowOffsetYPX != nil {
			if len(shadow.Offset) < 2 {
				shadow.Offset = []float64{0, 0}
			}
			shadow.Offset[1] = *style.ShadowOffsetYPX
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
