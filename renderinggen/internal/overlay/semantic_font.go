package overlay

import "strings"

const (
	// officialCyrillicFontPath is used for semantic text whose language needs
	// glyph coverage that Poppins-Bold does not provide. DejaVu Sans is already
	// part of the worker's certified asset bundle and covers the Cyrillic
	// scripts used by the multilingual runtime.
	officialCyrillicFontPath = "assets/fonts/DejaVuSans.ttf"
)

// officialFontPathForLanguage keeps the visual preset unchanged for Latin
// languages while selecting a font with actual Cyrillic coverage for semantic
// text. The language comes from the same localized overlay plan that carries
// the text, so this decision is made before Chronon lays out glyphs.
func officialFontPathForLanguage(language string) string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(language)), func(r rune) bool {
		return r == '-' || r == '_'
	})
	for _, part := range parts {
		if part == "cyrl" {
			return officialCyrillicFontPath
		}
	}
	if len(parts) == 0 {
		return officialFontPath
	}
	switch parts[0] {
	case "az", "be", "bg", "kk", "ky", "mk", "mn", "ru", "sr", "tg", "uk", "uz":
		return officialCyrillicFontPath
	default:
		return officialFontPath
	}
}
