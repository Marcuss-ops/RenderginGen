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
		// Image preset reveals use larger travel and scale changes than the
		// generic legacy motions, so each entrance stays visible across its
		// 37-frame (1.5s at 24fps) preset window.
		imageLayerDefinition("image_focus_reveal", "scale", 1.32, 1.0, "in_out_sine"),
		imageLayerDefinition("image_scale_reveal", "scale", 0.62, 1.0, "in_out_sine"),
		imageLayerDefinition("image_slide_left_reveal", "position_x", -360.0, 0.0, "in_out_sine"),
		imageLayerDefinition("image_slide_right_reveal", "position_x", 360.0, 0.0, "in_out_sine"),
		imageFadeDefinition("image_fade_reveal"),
	}
}

// Image presets keep their transform and fade active across the whole preset
// entrance window. The linear fade reaches full opacity at the end frame,
// while in_out_sine keeps visible travel through the full 1.5-second entrance.
func imageLayerDefinition(id, property string, from, to float64, easing string) MotionDefinition {
	const enter = 60
	return MotionDefinition{ID: id, Unit: "layer", Enter: enter, Tracks: []TrackDefinition{
		{Property: property, Easing: easing, Keyframes: []AnimationKeyframe{{Frame: 0, Value: from}, {Frame: enter, Value: to}}},
		{Property: "opacity", Easing: "linear", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: enter, Value: 1.0}}},
	}}
}

func imageFadeDefinition(id string) MotionDefinition {
	const enter = 60
	return MotionDefinition{ID: id, Unit: "layer", Enter: enter, Tracks: []TrackDefinition{
		{Property: "opacity", Easing: "linear", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: enter, Value: 1.0}}},
	}}
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
