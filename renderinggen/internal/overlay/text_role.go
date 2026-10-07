package overlay

// textRole names the semantic job a text layer does. It is the seam the
// visual 2.0 refactor grows from: phrase, entity-caption and map-label text
// must become roles of one text system instead of disguised template kinds
// with style patched after lowering. Today only entity_caption is resolved
// here; the remaining roles are declared so the vocabulary is fixed before
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

// applyTextRoleBaseStyle owns a role's base treatment: the fill, keyline,
// shadow, glow and background-card policy that used to be patched field by
// field inside the image compiler after compileTextLayer returned. Geometry,
// font-family choice and fill validation stay with the caller: an explicit
// fill wins over the role default, exactly as the compiler's override block
// used to. Migrated call-sites render byte-identical layers.
func applyTextRoleBaseStyle(role textRole, style *LayerStyle, fontSize float64, fill string) {
	switch role {
	case textRoleEntityCaption:
		// Cinematic nameplate treatment: a large warm-white title, a
		// restrained dark keyline and soft drop shadow keep names readable
		// over moving footage.
		if fill == "" {
			fill = "#F8F5EA"
		}
		style.Fill = fill
		style.FontSize = fontSize
		style.MinFontSize = fontSize
		style.MaxFontSize = fontSize
		style.Stroke = &LayerStroke{Color: "#111827", Width: 2.0}
		style.Shadow = &LayerShadow{Color: "#000000", Opacity: 0.78, Blur: 9, Offset: []float64{0, 3}}
		// A low intensity warm halo adds a current editorial finish without
		// washing out the letterforms or competing with the portrait.
		style.Glow = &LayerGlow{Color: "#F8F5EA", Radius: 14, Intensity: 0.24}
		// Avoid text background cards here: the native Vulkan text path
		// lowers those to a text_card node that the strict GPU backend
		// cannot execute.
		style.Background = nil
	default:
		// Roles not yet migrated keep the preset style untouched.
	}
}
