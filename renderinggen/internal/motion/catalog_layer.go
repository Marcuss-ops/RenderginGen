package motion

// layerMotions is the whole-layer vocabulary: transforms that animate the
// composed text layer as one object (fade, slide, scale, pop, shake) plus the
// two spring-style reveals authored with explicit keyframes.
//
// These are the legacy preset ids PipelineGen already emits, so their ids,
// units and enter durations are a compatibility surface: the ids stay, the
// implementations only move.
func layerMotions() []MotionDefinition {
	return []MotionDefinition{
		LegacyDefinition("fade", "layer", 1, 0),
		LegacyDefinition("fade_in", "layer", 48, 8),
		LegacyDefinition("reveal_from_bottom", "layer", 48, 6),
		LegacyDefinition("slide_in", "layer", 48, 6),
		LegacyDefinition("slide_from_right", "layer", 48, 6),
		LegacyDefinition("scale_drop", "layer", 48, 6),
		LegacyDefinition("soft_pop", "layer", 48, 6),
		LegacyDefinition("focus_in", "layer", 48, 6),
		LegacyDefinition("fade_out", "layer", 48, 8),
		LegacyDefinition("slide_up", "layer", 48, 6),
		LegacyDefinition("slide_down", "layer", 48, 6),
		LegacyDefinition("slide_left", "layer", 48, 6),
		LegacyDefinition("slide_right", "layer", 48, 6),
		LegacyDefinition("scale_in", "layer", 48, 6),
		LegacyDefinition("scale_out", "layer", 48, 6),
		LegacyDefinition("elastic_pop", "layer", 54, 8),
		LegacyDefinition("bounce_in", "layer", 54, 8),
		LegacyDefinition("pulse", "layer", 48, 8),
		LegacyDefinition("shake", "layer", 48, 8),
		layerDefinition("soft_scale_reveal", "scale", 0.88, 1.0, "out_cubic", 60),
		layerDefinition("precision_spring_up", "position_y", 120.0, 0.0, "out_back", 60),
	}
}

// layerDefinition builds a whole-layer reveal: one property track from → to on
// the given easing, plus the shared opacity fade that keeps the reveal visible
// on every backend.
func layerDefinition(id, property string, from, to float64, easing string, enter int) MotionDefinition {
	return MotionDefinition{ID: id, Unit: "layer", Enter: enter, Tracks: []TrackDefinition{
		{Property: property, Easing: easing, Keyframes: []AnimationKeyframe{{Frame: 0, Value: from}, {Frame: int64(enter), Value: to}}},
		{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: int64(enter * 2 / 3), Value: 1.0}}},
	}}
}
