package motion

// textMotions is the per-word/glyph vocabulary: staggered reveals, tracking
// (letter-spacing) collapses and expansions, a blur focus-in, the looping wave
// family, and the center-out expansion.
//
// A definition here lowers to a SelectorDefinition (which glyphs/words are
// animated, in which order) plus per-selector property tracks. That is why the
// family lives apart from the layer family: a layer motion has no selector and
// an id from this family that silently resolved to a layer fade would be the
// "distinct preset renders as one fade" regression.
//
// The converse gap is the one the generated phrase planner hit: a selector-only
// motion has NO composition-level dynamics, so wherever the selector is not
// prepared it renders as a static line. The ids that planner can select
// therefore carry both halves (see textEnvelope).
func textMotions() []MotionDefinition {
	return []MotionDefinition{
		// The five ids the generated phrase planner can select (word_reveal,
		// character_cascade, char_wave, opacity_wave, center_expansion) are
		// animator-only by construction: their whole effect is what the per-unit
		// selector does, and a motion whose only dynamics ride a selector renders
		// as a static line wherever the selector is not prepared. Each therefore
		// also carries a composition-level entrance, so the phrase animates with
		// or without it. This is the same rule the apple_v2 phrase library states
		// for its own definitions ("a per-unit text animator AND composition-level
		// layer tracks, so the same id renders as a visibly different effect on
		// every backend").
		textEnvelope(textDefinition("word_reveal", "word", 1, 40), "position_y", 20.0, 0.0, 18),
		textEnvelope(textDefinition("character_cascade", "glyph", 1, 40), "position_y", 20.0, 0.0, 18),
		textDefinition("tracking_collapse", "word", 0, 0),
		textDefinition("tracking_expansion", "word", 0, 0),
		textDefinition("blur_focus_in", "glyph", 0, 0),
		textEnvelope(textWaveDefinition("opacity_wave", "word", "opacity", []AnimationKeyframe{{0, 0.15}, {36, 1.0}, {72, 0.5}}, 1), "", 0, 0, 16),
		textWaveDefinition("scale_wave", "glyph", "scale", []AnimationKeyframe{{0, 0.75}, {36, 1.25}, {72, 1.0}}, 1),
		textEnvelope(textWaveDefinition("char_wave", "glyph", "position_y", []AnimationKeyframe{{0, 0.0}, {24, -30.0}, {48, 15.0}, {72, 0.0}}, 1), "position_y", 12.0, 0.0, 16),
		textEnvelope(textCenterDefinition(), "scale", 0.94, 1.0, 16),
	}
}

// textEnvelope appends the composition-level entrance that keeps an
// animator-only text motion visible on every backend: a settle on the motion's
// OWN axis (from -> to, keyframed inside the family's 72-frame entrance window)
// plus a layer fade-in.
//
// property is empty when the motion's own axis already IS opacity, so no
// geometry is invented for it. The magnitudes stay deliberately smaller than
// the per-unit motion's own offsets: the envelope is the floor of visibility,
// not a second competing effect — the per-unit curve stays the thing an editor
// chose.
func textEnvelope(d MotionDefinition, property string, from, to float64, fadeFrames int64) MotionDefinition {
	if property != "" {
		d.Tracks = append(d.Tracks, TrackDefinition{Property: property, Easing: "out_cubic", Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: from}, {Frame: 60, Value: to}, {Frame: 72, Value: to},
		}})
	}
	d.Tracks = append(d.Tracks, TrackDefinition{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{
		{Frame: 0, Value: 0.0}, {Frame: fadeFrames, Value: 1.0},
	}})
	return d
}

// textDefinition covers the reveal/retime family: three ids share the same
// shape (per-unit selector + one property track) and differ only in their
// property and endpoints.
func textDefinition(id, unit string, stagger, offset float64) MotionDefinition {
	if id == "tracking_collapse" || id == "tracking_expansion" {
		from, to := 40.0, 0.0
		if id == "tracking_expansion" {
			from, to = -4.0, 16.0
		}
		return MotionDefinition{ID: id, Unit: unit, Enter: 72, TextAnimators: []TextAnimatorDefinition{{
			ID: id + "_text", Selector: SelectorDefinition{Kind: unit, Stagger: int64(stagger)},
			Properties: []TrackDefinition{
				{Property: "tracking", Easing: "out_expo", Keyframes: []AnimationKeyframe{{Frame: 0, Value: from}, {Frame: 72, Value: to}}},
				{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 28, Value: 1.0}}},
			},
		}}}
	}
	if id == "blur_focus_in" {
		return MotionDefinition{ID: id, Unit: unit, Enter: 72, TextAnimators: []TextAnimatorDefinition{{
			ID: id + "_text", Selector: SelectorDefinition{Kind: unit},
			Properties: []TrackDefinition{
				{Property: "blur", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 24.0}, {Frame: 72, Value: 0.0}}},
				{Property: "scale", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 1.08}, {Frame: 72, Value: 1.0}}},
				{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 36, Value: 1.0}}},
			},
		}}}
	}
	return MotionDefinition{ID: id, Unit: unit, Enter: 72, TextAnimators: []TextAnimatorDefinition{{
		ID: id + "_text", Selector: SelectorDefinition{Kind: unit, Stagger: int64(stagger)},
		Properties: []TrackDefinition{
			{Property: "position_y", Easing: "linear", Keyframes: []AnimationKeyframe{{Frame: 0, Value: offset}, {Frame: 72, Value: offset}}},
			{Property: "opacity", Easing: "linear", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 72, Value: 0.0}}},
		},
	}}}
}

// textWaveDefinition is a looping per-unit property wave: the keyframes ARE the
// oscillation, so the caller owns the whole curve.
func textWaveDefinition(id, unit, property string, keyframes []AnimationKeyframe, stagger int64) MotionDefinition {
	return MotionDefinition{ID: id, Unit: unit, Enter: 72, TextAnimators: []TextAnimatorDefinition{{
		ID: id + "_text", Selector: SelectorDefinition{Kind: unit, Stagger: stagger},
		Properties: []TrackDefinition{{Property: property, Easing: "in_out_sine", Keyframes: keyframes}},
	}}}
}

// textCenterDefinition is the only reveal that animates its selector ORDER
// (from_center) rather than a stagger: the expansion literally starts at the
// middle of the line.
func textCenterDefinition() MotionDefinition {
	return MotionDefinition{ID: "center_expansion", Unit: "glyph", Enter: 72, TextAnimators: []TextAnimatorDefinition{{
		ID: "center_expansion_text", Selector: SelectorDefinition{Kind: "glyph", Shape: "smooth", Order: "from_center"},
		Properties: []TrackDefinition{
			{Property: "scale_x", Easing: "out_expo", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.5}, {Frame: 72, Value: 1.0}}},
			{Property: "tracking", Easing: "out_expo", Keyframes: []AnimationKeyframe{{Frame: 0, Value: -12.0}, {Frame: 72, Value: 2.0}}},
			{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 36, Value: 1.0}}},
		},
	}}}
}
