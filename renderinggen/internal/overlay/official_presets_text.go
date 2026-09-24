package overlay

// official_presets_text.go owns the text presets. Phrase appearance is a
// single stable default; animation remains independently selectable through
// motion_id and the canonical motion registry.

const (
	// StaticTextSmokePresetID is the deliberately static smoke/canary preset:
	// it proves text pixels without requiring an animation window.
	StaticTextSmokePresetID = "static_text_smoke"
	// PhraseDefaultPresetID is the sole official animated phrase preset.
	PhraseDefaultPresetID = "phrase_default"
)

// officialFontPath is the canonical default font. Plans may select another
// bundled font family through the closed runtime font-family vocabulary.
const officialFontPath = "assets/fonts/Poppins-Bold.ttf"

func textSpec(anchor, align, anim, unit string, enter, exit int, shadow *StyleShadow, glow *StyleGlow) presetSpec {
	return presetSpec{family: PresetText, anchor: anchor, align: align, anim: anim, unit: unit, enter: enter, exit: exit, shadow: shadow, glow: glow}
}

func staticTextSmokeSpec() presetSpec {
	return textSpec("safe_area", "center", "", "line", 0, 0, nil, nil)
}

func phraseGlow() *StyleGlow {
	return &StyleGlow{Radius: 42, Intensity: 0.25, Color: "#FFFFFF"}
}

// phraseDefaultPreset is intentionally visual-only apart from its initial
// motion choice. Callers can replace that motion per item without selecting a
// second phrase style.
func phraseDefaultPreset() PresetDefinition {
	d := makePreset(PhraseDefaultPresetID, textSpec("safe_area", "center", "phrase_apple_clean_01_blur_soft_reveal", "glyph", 60, 12, &StyleShadow{
		Color: "#000000", Opacity: 0.68, Blur: 14, Offset: []float64{0, 5},
	}, phraseGlow()))
	d.Layout.BoxWidth = 1920
	d.Layout.BoxHeight = 260
	d.Style.FontSize = 64
	d.Style.Fill = []float64{1, 1, 1, 1}
	d.Style.Stroke = &StyleStroke{Color: "#111827", Width: 3.5}
	return d
}
