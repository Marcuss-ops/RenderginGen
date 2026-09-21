package overlay

// official_presets_text.go owns the TEXT half of the official preset catalog:
// the font asset every text preset shares, the authoring rows, and the
// canonical Apple-style phrase preset.
//
// A text preset is the only place a phrase's legibility is decided, so both
// rules that apply to every one of them live here: a stroke and a shadow are
// mandatory (a phrase over arbitrary footage is unreadable without them) and
// the canonical preset is the one whose geometry the 1920-wide assembly canvas
// is sized for.

// Preset IDs of the text family.
const (
	// StaticTextSmokePresetID is the deliberately static smoke/canary preset:
	// it has no motion at all, which is what lets a short composition certify
	// text pixels without an animation window.
	StaticTextSmokePresetID = "static_text_smoke"
)

// officialFontPath is the single font asset every official text preset
// references. Keeping this path in one place prevents visual presets from
// silently drifting to unavailable font aliases.
const officialFontPath = "assets/fonts/Poppins-Bold.ttf"

// textSpec is the text-family authoring row: an anchor, an alignment, the
// motion id/unit the preset selects by default, its enter/exit windows, and
// the legibility pair (stroke + shadow) plus the optional halo (glow).
func textSpec(anchor, align, anim, unit string, enter, exit int, shadow *StyleShadow, glow *StyleGlow) presetSpec {
	return presetSpec{family: PresetText, anchor: anchor, align: align, anim: anim, unit: unit, enter: enter, exit: exit, shadow: shadow, glow: glow}
}

// staticTextSmokeSpec is the smoke preset's row: safe_area, centered, no
// motion, no shadow (makePreset adds neither stroke nor shadow for it).
func staticTextSmokeSpec() presetSpec {
	return textSpec("safe_area", "center", "", "line", 0, 0, nil, nil)
}

// canonicalTextPreset is the Apple-style phrase preset: the 1920-wide phrase
// band on the assembly canvas, white bold type with the mandatory stroke and
// shadow, revealed by the apple_phrase_v2 glyph motion.
func canonicalTextPreset() PresetDefinition {
	d := makePreset(CanonicalTextPresetID, textSpec("safe_area", "center", "apple_phrase_v2", "glyph", 72, 6, &StyleShadow{
		Color: "#000000", Opacity: 0.72, Blur: 12, Offset: []float64{0, 4},
	}, canaryGlow()))
	d.Layout.BoxWidth = 1920
	d.Layout.BoxHeight = 220
	d.Style.FontSize = 64
	d.Style.Fill = []float64{1, 1, 1, 1}
	d.Style.Stroke = &StyleStroke{Color: "#111827", Width: 3.5}
	return d
}
