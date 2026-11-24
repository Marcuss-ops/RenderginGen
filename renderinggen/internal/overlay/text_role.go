package overlay

import "math"

// textRole names the semantic job a text layer does. Phrase, important phrase,
// lower-third, metric and date use dedicated resolvers; entity/image captions
// and map labels/attribution provide role-resolved specs directly. All share
// compileResolvedText, keeping semantic policy out of template-specific lowering.
type textRole string

const (
	textRolePhrase          textRole = "phrase"
	textRoleImportantPhrase textRole = textRole(KindImportantPhrase)
	textRoleEntityCaption   textRole = "entity_caption"
	textRoleImageCaption    textRole = "image_caption"
	textRoleLowerThird      textRole = textRole(KindLowerThird)
	textRoleMetric          textRole = "metric"
	textRoleDate            textRole = "date"
	textRoleMapLabel        textRole = "map_label"
	textRoleMapAttribution  textRole = "map_attribution"
)

// Map text-plate policy.
//
// A map plate is drawn over a satellite/photo basemap, so its base treatment is
// text-system policy rather than a drawing constant of the map. The map owns
// WHERE a plate sits (its position, the box it measures, collision handling and
// priority) and its own contract may override the effects; what a plate looks
// like BY DEFAULT is decided here, in one place, next to the other roles. A
// change below is one edit that changes every map label — which is the point:
// before this, the rule lived inline in the map's layer builder, so a subsystem
// that must not own typography effectively did.
const (
	mapTextPlateShadowColor   = "#000000"
	mapTextPlateShadowOpacity = 0.75
	mapTextPlateShadowBlur    = 6.0
	mapTextPlateFitMode       = FitModeShrinkOnly
)

// Shared caption treatment. Entity and editorial image captions render over
// moving footage: one dark keyline plus one soft drop shadow, no glow pass
// (the halo's separable blur saturated the RTX A4000 on the strict Vulkan
// path). These are the single source of truth for that treatment — the role
// resolver is the only writer, so tuning caption legibility is one edit here.
const (
	captionKeylineColor   = "#111827"
	captionKeylineWidth   = 2.0
	captionShadowColor    = "#000000"
	captionShadowOpacity  = 0.78
	captionShadowBlur     = 9.0
	captionShadowOffsetX  = 0.0
	captionShadowOffsetY  = 3.0
	captionForegroundFill = "#F8F5EA"
)

// applyTextRoleBaseStyle owns a role's base treatment: the fill, keyline,
// shadow, glow, fit and background-card policy that used to be patched field by
// field inside the image compiler after compileTextLayer returned, or built
// inline by the map layer builder. Geometry, font-family choice and fill
// validation stay with the caller: an explicit fill wins over the role default,
// exactly as the compiler's override block used to. Migrated call-sites render
// byte-identical layers.
//
// runtimeStyle carries the plan's explicit runtime style keys
// (font_family, glow_size, stroke_size, shadow_*, …). A key present there is
// the payload's choice and the role default must not clobber it — the
// single guarantee that every animation renders the payload's look.
func applyTextRoleBaseStyle(role textRole, style *LayerStyle, fontSize float64, fill string, runtimeStyle map[string]any) {
	switch role {
	case textRolePhrase, textRoleImportantPhrase, textRoleLowerThird:
		applyPhraseRoleBaseStyle(style, fill, runtimeStyle)
	case textRoleEntityCaption, textRoleImageCaption:
		applyCaptionRoleBaseStyle(style, fontSize, fill, runtimeStyle)
	case textRoleMetric, textRoleDate:
		applyDataRoleBaseStyle(style, fill)
	case textRoleMapLabel, textRoleMapAttribution:
		applyMapRoleBaseStyle(style, fontSize, fill, runtimeStyle)
	default:
		// Unrecognized roles keep the preset style untouched.
	}
}

// applyPhraseRoleBaseStyle gives editorial text a contrast-safe foreground and
// keyline while preserving every runtime choice.
func applyPhraseRoleBaseStyle(style *LayerStyle, fill string, runtimeStyle map[string]any) {
	// Phrase recipes can arrive without their authored fill in the runtime
	// motion catalog. Everything else is already on the seeded style.
	if fill == "" {
		fill = "#F8F5EA"
	}
	style.Fill = fill
	// Phrase text may sit directly over a photograph. Text cards are not
	// supported by the strict Vulkan path, so give every phrase a keyline whose
	// luminance opposes its fill.
	if style.Stroke == nil {
		if _, chosen := runtimeStyle["stroke_size"]; !chosen {
			style.Stroke = &LayerStroke{Color: contrastingKeyline(fill), Width: 2.5}
		}
	}
	// The phrase role stays plate-free unless the payload explicitly asked for
	// card_background; the plan-level pass then lowers it to a shape.
	if !runtimeStyleHasBackground(runtimeStyle) {
		style.Background = nil
	}
}

// applyCaptionRoleBaseStyle is the cinematic caption treatment shared by entity
// and editorial image captions.
func applyCaptionRoleBaseStyle(style *LayerStyle, fontSize float64, fill string, runtimeStyle map[string]any) {
	if fill == "" {
		fill = captionForegroundFill
	}
	style.Fill = fill
	style.FontSize = fontSize
	// EntityCardLayout resolves the final font size before lowering. Keep a
	// fixed point size here instead of emitting min/max fitting bounds with
	// fit_mode=none.
	style.MinFontSize = 0
	style.MaxFontSize = 0
	// The payload's explicit look always wins: only seed the role treatment for
	// effects the plan did not choose.
	if _, chosen := runtimeStyle["stroke_size"]; !chosen {
		style.Stroke = &LayerStroke{Color: captionKeylineColor, Width: captionKeylineWidth}
	}
	if !hasRuntimeShadowKey(runtimeStyle) {
		style.Shadow = &LayerShadow{Color: captionShadowColor, Opacity: captionShadowOpacity, Blur: captionShadowBlur, Offset: []float64{captionShadowOffsetX, captionShadowOffsetY}}
	}
	// The default stays glow-free: on the strict Vulkan path each animated text
	// frame renders at output size, and a full-frame halo's separable blur
	// saturated the RTX A4000 (Xid 109).
	if _, chosen := runtimeStyle["glow_size"]; !chosen {
		style.Glow = nil
	}
	// A caption plate is opt-in.
	if !runtimeStyleHasBackground(runtimeStyle) {
		style.Background = nil
	}
}

// applyDataRoleBaseStyle keeps the catalog/runtime seeded style intact and only
// supplies the caller's explicit fill.
func applyDataRoleBaseStyle(style *LayerStyle, fill string) {
	if fill != "" {
		style.Fill = fill
	}
}

// applyMapRoleBaseStyle adds the shrink-only fit and soft shadow that keep map
// text legible over a basemap. The fill is always supplied by the map contract.
func applyMapRoleBaseStyle(style *LayerStyle, fontSize float64, fill string, runtimeStyle map[string]any) {
	style.Fill = fill
	style.FontSize = fontSize
	style.MinFontSize = fontSize
	style.MaxFontSize = fontSize
	style.FitMode = mapTextPlateFitMode
	if !hasRuntimeShadowKey(runtimeStyle) {
		style.Shadow = &LayerShadow{
			Color:   mapTextPlateShadowColor,
			Opacity: mapTextPlateShadowOpacity,
			Blur:    mapTextPlateShadowBlur,
			Offset:  []float64{0, 2},
		}
	}
}

func contrastingKeyline(fill string) string {
	rgb, err := parseHexColor(fill)
	if err != nil || len(rgb) < 3 {
		return "#111827"
	}
	// WCAG relative luminance threshold in sRGB; only the polarity matters
	// here because the keyline is an outline, not a text/background pair.
	linear := func(v float64) float64 {
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	luminance := 0.2126*linear(rgb[0]) + 0.7152*linear(rgb[1]) + 0.0722*linear(rgb[2])
	if luminance > 0.45 {
		return "#111827"
	}
	return "#FFFFFF"
}

// hasRuntimeShadowKey reports whether the plan explicitly chose any shadow
// control for this layer.
func hasRuntimeShadowKey(runtimeStyle map[string]any) bool {
	for _, key := range []string{"shadow_blur_px", "shadow_opacity", "shadow_offset_x_px", "shadow_offset_y_px"} {
		if _, chosen := runtimeStyle[key]; chosen {
			return true
		}
	}
	return false
}

// runtimeStyleHasBackground reports whether the plan explicitly chose a text
// plate. card_background reaches the layer as the nested style.background block;
// an absent block means the role default stays plate-free.
func runtimeStyleHasBackground(runtimeStyle map[string]any) bool {
	nested, ok := runtimeStyle["style"].(map[string]any)
	if !ok {
		return false
	}
	_, chosen := nested["background"]
	return chosen
}
