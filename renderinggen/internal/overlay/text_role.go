package overlay

// textRole names the semantic job a text layer does. It is the seam the
// visual 2.0 refactor grows from: phrase, entity-caption and map-label text
// must become roles of one text system instead of disguised template kinds
// with style patched after lowering. entity_caption and the two map roles are
// migrated; the remaining roles are declared so the vocabulary is fixed before
// each migration, not invented per call-site.
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

// mapTextPlateStyle builds a map plate's base style for a role. The map supplies
// the measured typography scale and the minimum fit bound, because both are
// consequences of the box the map measured; everything else is the role's.
func mapTextPlateStyle(role textRole, font string, fontSize, minFontSize float64, fill string) *LayerStyle {
	style := &LayerStyle{Font: font}
	applyTextRoleBaseStyle(role, style, fontSize, fill)
	style.MinFontSize = minFontSize
	return style
}

// applyTextRoleBaseStyle owns a role's base treatment: the fill, keyline,
// shadow, glow, fit and background-card policy that used to be patched field by
// field inside the image compiler after compileTextLayer returned, or built
// inline by the map layer builder. Geometry, font-family choice and fill
// validation stay with the caller: an explicit fill wins over the role default,
// exactly as the compiler's override block used to. Migrated call-sites render
// byte-identical layers.
func applyTextRoleBaseStyle(role textRole, style *LayerStyle, fontSize float64, fill string) {
	switch role {
	case textRolePhrase, textRoleImportantPhrase:
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
			fill = "#F8F5EA"
		}
		style.Fill = fill
		style.FontSize = fontSize
		// EntityCardLayout resolves the final font size before lowering. Keep a
		// fixed point size here instead of emitting min/max fitting bounds with
		// fit_mode=none (Chronon correctly rejects that contradictory payload).
		style.MinFontSize = 0
		style.MaxFontSize = 0
		style.Stroke = &LayerStroke{Color: "#111827", Width: 2.0}
		style.Shadow = &LayerShadow{Color: "#000000", Opacity: 0.78, Blur: 9, Offset: []float64{0, 3}}
		// A low intensity warm halo adds a current editorial finish without
		// washing out the letterforms or competing with the portrait.
		style.Glow = &LayerGlow{Color: "#F8F5EA", Radius: 14, Intensity: 0.24}
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
		style.Shadow = &LayerShadow{
			Color:   mapTextPlateShadowColor,
			Opacity: mapTextPlateShadowOpacity,
			Blur:    mapTextPlateShadowBlur,
			Offset:  []float64{0, 2},
		}
	default:
		// Roles not yet migrated keep the preset style untouched.
	}
}
