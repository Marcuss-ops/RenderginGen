package overlay

// textRole names the semantic job a text layer does. Phrase, important phrase,
// lower-third, metric and date use dedicated resolvers; entity/image captions
// and map labels/attribution provide role-resolved specs directly. All share
// compileResolvedText, keeping semantic policy out of template-specific lowering.
type textRole string

const (
	textRolePhrase          textRole = "phrase"
	textRoleImportantPhrase textRole = "important_phrase"
	textRoleEntityCaption   textRole = "entity_caption"
	textRoleImageCaption    textRole = "image_caption"
	textRoleLowerThird      textRole = "lower_third"
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
	mapTextPlateFitMode       = "shrink_only"
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
		// Phrase recipes can arrive without their authored fill in the runtime
		// motion catalog. Give editorial text a contrast-safe foreground so it
		// remains readable over the dark phrase background. Everything else —
		// the preset's legibility stroke/shadow, the shrink-only fit range and
		// any runtime override — is already on the seeded style: re-deriving
		// them here would clobber explicit decisions (zero-disables, font
		// size, fit bounds) the runtime style contract pins.
		if fill == "" {
			fill = "#F8F5EA"
		}
		style.Fill = fill
		style.Background = nil
	case textRoleEntityCaption, textRoleImageCaption:
		// Cinematic caption treatment: a large warm-white title, a
		// restrained dark keyline and soft drop shadow keep captions readable
		// over moving footage. Entity and editorial image captions share this
		// policy while retaining distinct semantic roles for future resolvers.
		if fill == "" {
			fill = captionForegroundFill
		}
		style.Fill = fill
		style.FontSize = fontSize
		// EntityCardLayout resolves the final font size before lowering. Keep a
		// fixed point size here instead of emitting min/max fitting bounds with
		// fit_mode=none (Chronon correctly rejects that contradictory payload).
		style.MinFontSize = 0
		style.MaxFontSize = 0
		// The payload's explicit look always wins: only seed the role
		// treatment for effects the plan did not choose. Without this guard
		// the role base runs after applyTextRuntimeOverrides and would
		// silently replace the payload's stroke, shadow and glow.
		if _, chosen := runtimeStyle["stroke_size"]; !chosen {
			style.Stroke = &LayerStroke{Color: captionKeylineColor, Width: captionKeylineWidth}
		}
		if !hasRuntimeShadowKey(runtimeStyle) {
			style.Shadow = &LayerShadow{Color: captionShadowColor, Opacity: captionShadowOpacity, Blur: captionShadowBlur, Offset: []float64{captionShadowOffsetX, captionShadowOffsetY}}
		}
		// The default stays glow-free: on the strict Vulkan path each
		// animated text frame renders at output size, and a full-frame
		// halo's separable blur saturated the RTX A4000 (Xid 109). An
		// explicit payload glow_size overrides this default — the
		// producer chose the halo knowingly, and applyTextRuntimeOverrides
		// already lowered it above.
		if _, chosen := runtimeStyle["glow_size"]; !chosen {
			style.Glow = nil
		}
		// Avoid text background cards here: the native Vulkan text path
		// lowers those to a text_card node that the strict GPU backend
		// cannot execute.
		style.Background = nil
	case textRoleMetric, textRoleDate:
		// Data roles keep the catalog/runtime seeded style intact; the resolver
		// may supply a no-shrink fit policy and larger box, but semantic data
		// does not receive phrase or caption nameplate effects.
	case textRoleMapLabel, textRoleMapAttribution:
		// Legibility over a basemap comes from a shrink-only fit plus a soft
		// drop shadow. The fill is supplied by the caller — a pin's contract
		// colour, or the attribution's white — and is deliberately NOT
		// defaulted here: a map plate's foreground is part of the published map
		// contract, so inventing one would change rendered maps.
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
	default:
		// Unrecognized roles keep the preset style untouched.
	}
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
