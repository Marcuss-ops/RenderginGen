package motion

// typewriterMotions is the per-character reveal family. Each id is the same
// mechanism — a glyph selector with a one-character stagger — and differs only
// in the SHAPE of the selector sweep and the property the character is animated
// on (opacity, blur, tracking, position/scale jitter), so the five ids read as
// five distinct "typing" textures rather than one fade with different names.
func typewriterMotions() []MotionDefinition {
	return []MotionDefinition{
		typewriterDefinition("typewriter_clean", "square", []TrackDefinition{
			{Property: "opacity", Easing: "linear", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 72, Value: 0.0}}},
		}),
		typewriterDefinition("typewriter_pop", "square", []TrackDefinition{
			{Property: "opacity", Easing: "linear", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 72, Value: 0.0}}},
			{Property: "position_y", Easing: "out_back", Keyframes: []AnimationKeyframe{{Frame: 0, Value: -24.0}, {Frame: 72, Value: -24.0}}},
			{Property: "scale", Easing: "out_back", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 1.35}, {Frame: 72, Value: 1.35}}},
		}),
		typewriterDefinition("typewriter_neon", "smooth", []TrackDefinition{
			{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 72, Value: 0.0}}},
			{Property: "blur", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 16.0}, {Frame: 72, Value: 16.0}}},
		}),
		typewriterDefinition("typewriter_tracking", "smooth", []TrackDefinition{
			{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 72, Value: 0.0}}},
			{Property: "tracking", Easing: "out_expo", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 16.0}, {Frame: 72, Value: 16.0}}},
			{Property: "position_x", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: -12.0}, {Frame: 72, Value: -12.0}}},
		}),
		typewriterDefinition("typewriter_glitch", "square", []TrackDefinition{
			{Property: "opacity", Easing: "linear", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 72, Value: 0.0}}},
			{Property: "position_x", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 20.0}, {Frame: 72, Value: 20.0}}},
			{Property: "scale_x", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 1.4}, {Frame: 72, Value: 1.4}}},
		}),
	}
}

// typewriterDefinition builds one per-character reveal: a glyph selector with a
// one-character stagger whose sweep is the typing cursor, plus the property
// tracks that give the family its texture.
func typewriterDefinition(id, shape string, properties []TrackDefinition) MotionDefinition {
	return MotionDefinition{
		ID:    id,
		Unit:  "glyph",
		Enter: 72,
		TextAnimators: []TextAnimatorDefinition{{
			ID:         id + "_text",
			Selector:   SelectorDefinition{Kind: "glyph", Shape: shape, Stagger: 1},
			Properties: properties,
		}},
	}
}
