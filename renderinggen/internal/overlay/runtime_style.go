package overlay

import (
	"fmt"
	"math"
	"strings"
)

const (
	maxRuntimeGlowSize      = 256.0
	maxRuntimeGlowIntensity = 4.0
	maxRuntimeStrokeSize    = 64.0
	maxRuntimeFontSize      = 512.0
	maxRuntimeShadowSize    = 256.0
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

// textRuntimeKeyAllowedForKind is the single owner of "may this runtime text
// key travel on a layer of this kind?". It is used by the validator (to reject
// a text override on a media layer) and by the chain segment scrubber (to drop
// such keys from a segment that inherited them). Two copies of this rule would
// drift: a key allowed in one and scrubbed in the other becomes a silent no-op.
//
// Non-media kinds own every text key. A media kind may carry a key only when:
//   - the item is a captioned entity/image, where the key styles the caption;
//   - the key is shape `fill` (geometry, not a text face colour);
//   - the key is shared with the image-style vocabulary (shadow_opacity), owned
//     by the image validator.
func textRuntimeKeyAllowedForKind(key string, kind ItemKind, hasImage, hasEntityCaption bool) bool {
	if !isShapeKind(kind) && !isImageKind(kind) && !isVideoKind(kind) && !(isEntityKind(kind) && hasImage) {
		return true
	}
	if (isEntityKind(kind) || isImageKind(kind)) && hasImage && hasEntityCaption {
		return true
	}
	if key == "fill" && isShapeKind(kind) {
		return true
	}
	for _, imageKey := range runtimeImageStyleKeys {
		if imageKey == key {
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
			value, exists := params[key]
			if !exists {
				continue
			}
			// The same predicate the chain scrubber uses decides whether the
			// key may travel at all. Captioned entity images then validate the
			// key with the text-layer rules, so the payload's font, glow,
			// stroke, size and shadow reach the caption instead of falling
			// back to preset constants.
			if !textRuntimeKeyAllowedForKind(key, kind, hasImage, hasEntityCaption) {
				return fmt.Errorf("overlay: item %q cannot apply text runtime override %q to a non-text layer", itemID, key)
			}
			if captionedEntityImage {
				if err := validateRuntimeTextStyleKey(key, value, itemID); err != nil {
					return err
				}
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
	// Background is the optional plate behind the text run. It rides the
	// nested style.background block (card_background in PipelineGen) so the
	// payload can put a coloured rectangle behind an entity context label.
	Background *LayerBackground
}

func (s runtimeTextStyle) empty() bool {
	return s.FontPath == "" && s.FontSizePX == nil && s.Fill == nil &&
		s.GlowSize == nil && s.GlowColor == nil && s.StrokeSize == nil &&
		s.ShadowBlurPX == nil && s.ShadowOpacity == nil &&
		s.ShadowOffsetXPX == nil && s.ShadowOffsetYPX == nil &&
		s.Background == nil
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
	if value, exists := params["style"]; exists {
		// A string style id is a legacy presentation selector, not the nested
		// runtime style block; only a map can carry the caption plate.
		nested, ok := value.(map[string]any)
		if !ok {
			return style, nil
		}
		if raw, chosen := nested["background"]; chosen {
			background, err := decodeRuntimeTextBackground(raw)
			if err != nil {
				return runtimeTextStyle{}, err
			}
			style.Background = background
		}
	}
	return style, nil
}

// decodeRuntimeTextBackground parses the optional text plate. The renderer's
// BackgroundStyle is the contract: color is required, opacity defaults to the
// renderer's 1, radius to 0 and padding to 0. Malformed values fail closed so a
// producer never ships a plate the renderer would silently drop.
func decodeRuntimeTextBackground(raw any) (*LayerBackground, error) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("params.style.background must be an object")
	}
	color, ok := entry["color"].(string)
	if !ok || !isHexColor(color) {
		return nil, fmt.Errorf("params.style.background.color must be #RRGGBB")
	}
	background := &LayerBackground{Color: color, LiftToShape: true}
	if value, exists := entry["opacity"]; exists {
		n, ok := numericValue(value)
		if !ok || !finite(n) || n < 0 || n > 1 {
			return nil, fmt.Errorf("params.style.background.opacity must be between 0 and 1")
		}
		background.Opacity = &n
	}
	if value, exists := entry["radius"]; exists {
		n, ok := numericValue(value)
		if !ok || !finite(n) || n < 0 || n > 512 {
			return nil, fmt.Errorf("params.style.background.radius must be between 0 and 512 pixels")
		}
		background.Radius = n
	}
	if value, exists := entry["padding"]; exists {
		padding, err := runtimeBackgroundPadding(value)
		if err != nil {
			return nil, err
		}
		background.Padding = padding
	}
	if value, exists := entry["stroke"]; exists {
		stroke, err := decodeRuntimePlateStroke(value)
		if err != nil {
			return nil, err
		}
		background.Stroke = stroke
	}
	if value, exists := entry["shadow"]; exists {
		shadow, err := decodeRuntimePlateShadow(value)
		if err != nil {
			return nil, err
		}
		background.Shadow = shadow
	}
	if value, exists := entry["motions"]; exists {
		motions, err := decodeRuntimePlateMotions(value)
		if err != nil {
			return nil, err
		}
		background.Motions = motions
	}
	return background, nil
}

func runtimeBackgroundPadding(value any) ([]float64, error) {
	var raw []any
	switch values := value.(type) {
	case []any:
		raw = values
	case []float64:
		raw = make([]any, len(values))
		for i, n := range values {
			raw[i] = n
		}
	default:
		return nil, fmt.Errorf("params.style.background.padding accepts at most [x,y]")
	}
	if len(raw) == 0 || len(raw) > 2 {
		return nil, fmt.Errorf("params.style.background.padding accepts at most [x,y]")
	}
	out := make([]float64, len(raw))
	for i, value := range raw {
		n, ok := numericValue(value)
		if !ok || !finite(n) || n < 0 || n > 512 {
			return nil, fmt.Errorf("params.style.background.padding values must be between 0 and 512 pixels")
		}
		out[i] = n
	}
	return out, nil
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
	"image_glow_radius", "image_glow_intensity", "image_glow_color",
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

// imageStyleNumberMessage names the contract sentence a numeric rule uses. The
// wording is part of the payload contract, so it is selected by kind instead of
// being repeated in every table entry.
type imageStyleNumberMessage int

const (
	imageStyleWithin        imageStyleNumberMessage = iota // [min,max]
	imageStyleWithinPixels                                 // ±max pixels
	imageStyleWithinSigned                                 // ±max
	imageStyleBetweenPixels                                // between min and max pixels
)

// imageStyleNumberRule is one numeric image-style override. Adding a key means
// adding a row, not another switch arm: the acceptance check lives in
// validate() and every message keeps the same phrasing.
type imageStyleNumberRule struct {
	min          float64
	max          float64
	minExclusive bool
	// finite rejects NaN/Inf. The dimension rules predate this check and only
	// bound the magnitude, so they leave it off.
	finite  bool
	message imageStyleNumberMessage
}

func (rule imageStyleNumberRule) validate(key string, value any, itemID string) error {
	n, ok := numericValue(value)
	if !ok || (rule.finite && !finite(n)) {
		return rule.error(key, itemID)
	}
	if rule.minExclusive {
		if n <= rule.min || n > rule.max {
			return rule.error(key, itemID)
		}
		return nil
	}
	if n < rule.min || n > rule.max {
		return rule.error(key, itemID)
	}
	return nil
}

func (rule imageStyleNumberRule) error(key, itemID string) error {
	low := rule.min
	if rule.minExclusive {
		// A strict zero bound is the card dimension contract: the smallest
		// legal pixel is 1, which is what the payload message prints.
		low = 1
	}
	switch rule.message {
	case imageStyleBetweenPixels:
		return fmt.Errorf("overlay: item %q params.%s must be between %g and %g pixels", itemID, key, low, rule.max)
	case imageStyleWithinPixels:
		return fmt.Errorf("overlay: item %q params.%s must be within ±%g pixels", itemID, key, rule.max)
	case imageStyleWithinSigned:
		return fmt.Errorf("overlay: item %q params.%s must be within ±%g", itemID, key, rule.max)
	default:
		return fmt.Errorf("overlay: item %q params.%s must be within [%g,%g]", itemID, key, low, rule.max)
	}
}

// runtimeImageNumberRules is the numeric half of the image-style contract.
// Every key advertised in runtimeImageStyleKeys must appear either here or in
// the color/bool/fit tables below; TestRuntimeImageStyleRulesCoverCatalog
// enforces that split so a key can never become an unchecked no-op.
var runtimeImageNumberRules = map[string]imageStyleNumberRule{
	"width":  {min: 0, minExclusive: true, max: 8192, message: imageStyleBetweenPixels},
	"height": {min: 0, minExclusive: true, max: 8192, message: imageStyleBetweenPixels},

	"position_x": {min: -8192, max: 8192, finite: true, message: imageStyleWithinPixels},
	"position_y": {min: -8192, max: 8192, finite: true, message: imageStyleWithinPixels},

	"radius":        {max: maxFrameRadius, finite: true, message: imageStyleWithin},
	"border_radius": {max: maxFrameRadius, finite: true, message: imageStyleWithin},
	"border_width":  {max: maxFrameBorderWidth, finite: true, message: imageStyleWithin},
	"stroke_width":  {max: maxFrameStrokeWidth, finite: true, message: imageStyleWithin},

	"image_glow_radius":    {max: maxRuntimeGlowSize, finite: true, message: imageStyleWithin},
	"image_glow_intensity": {max: maxRuntimeGlowIntensity, finite: true, message: imageStyleWithin},

	"shadow_opacity":  {max: 1, finite: true, message: imageStyleWithin},
	"shadow_blur":     {max: maxFrameShadowBlur, finite: true, message: imageStyleWithin},
	"shadow_offset_x": {min: -maxFrameShadowOffset, max: maxFrameShadowOffset, finite: true, message: imageStyleWithinSigned},
	"shadow_offset_y": {min: -maxFrameShadowOffset, max: maxFrameShadowOffset, finite: true, message: imageStyleWithinSigned},
}

var runtimeImageColorKeys = map[string]bool{
	"border_color": true, "stroke_color": true, "shadow_color": true, "image_glow_color": true,
}

var runtimeImageBoolKeys = map[string]bool{
	"bounded": true, "shadow_enabled": true,
}

func validateRuntimeImageStyleKey(key string, value any, itemID string) error {
	if rule, registered := runtimeImageNumberRules[key]; registered {
		return rule.validate(key, value, itemID)
	}
	if runtimeImageColorKeys[key] {
		color, ok := value.(string)
		if !ok || !isHexColor(color) {
			return fmt.Errorf("overlay: item %q params.%s must be #RRGGBB", itemID, key)
		}
		return nil
	}
	if runtimeImageBoolKeys[key] {
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("overlay: item %q params.%s must be a boolean", itemID, key)
		}
		return nil
	}
	if key == "fit" {
		fit, ok := value.(string)
		if !ok || (fit != "cover" && fit != "contain" && fit != "fill") {
			return fmt.Errorf("overlay: item %q params.fit must be cover, contain or fill", itemID)
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
	if style.Background != nil {
		layer.Style.Background = cloneLayerStyleBackground(style.Background)
	}
	return nil
}
