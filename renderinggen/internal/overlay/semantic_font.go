package overlay

import "strings"

const (
	// officialCyrillicFontPath is used for semantic text whose language needs
	// glyph coverage that Poppins-Bold does not provide. DejaVu Sans is already
	// part of the worker's certified asset bundle and covers the Cyrillic
	// scripts used by the multilingual runtime.
	officialCyrillicFontPath = "assets/fonts/DejaVuSans.ttf"
)

// OfficialFontPathForLanguage keeps the visual preset unchanged for Latin
// languages while selecting a font with actual Cyrillic coverage for semantic
// text. The language comes from the same localized overlay plan that carries
// the text, so this decision is made before Chronon lays out glyphs.
//
// godlike/06 SSOT: this is the ONLY owner of the "which language burns which
// primary font" rule. The batch builder derives the fonts a job must DECLARE
// from this same function, because the worker resolves a job's assets against
// its workspace and Chronon scans the primary font's directory for fallback: a
// plan font the manifest does not declare does not exist for the shaper and the
// Cyrillic render fails in preflight.
func OfficialFontPathForLanguage(language string) string {
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
