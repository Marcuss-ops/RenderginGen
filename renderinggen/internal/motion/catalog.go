package motion

func init() {
	for _, d := range []MotionDefinition{
		LegacyDefinition("fade", "layer", 1, 0),
		LegacyDefinition("fade_in", "layer", 10, 8),
		LegacyDefinition("reveal_from_bottom", "layer", 8, 6),
		LegacyDefinition("slide_in", "layer", 8, 6),
		LegacyDefinition("scale_drop", "layer", 8, 6),
		LegacyDefinition("soft_pop", "layer", 8, 6),
		LegacyDefinition("focus_in", "layer", 8, 6),
		LegacyDefinition("fade_out", "layer", 10, 8),
		LegacyDefinition("slide_up", "layer", 8, 6),
		LegacyDefinition("slide_down", "layer", 8, 6),
		LegacyDefinition("slide_left", "layer", 8, 6),
		LegacyDefinition("slide_right", "layer", 8, 6),
		LegacyDefinition("scale_in", "layer", 8, 6),
		LegacyDefinition("scale_out", "layer", 8, 6),
		LegacyDefinition("elastic_pop", "layer", 14, 8),
		LegacyDefinition("bounce_in", "layer", 15, 8),
		LegacyDefinition("pulse", "layer", 8, 8),
		LegacyDefinition("shake", "layer", 8, 8),
		LegacyDefinition("word_reveal", "word", 10, 6),
		LegacyDefinition("character_cascade", "glyph", 10, 6),
	} {
		_ = Register(d.ID, DeclarativePlugin{Definition: d})
	}
}
