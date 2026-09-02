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
		textDefinition("word_reveal", "word", 6, 30),
		textDefinition("character_cascade", "glyph", 2, 34),
		textDefinition("tracking_collapse", "word", 0, 0),
		textDefinition("tracking_expansion", "word", 0, 0),
		textDefinition("blur_focus_in", "glyph", 0, 0),
		layerDefinition("soft_scale_reveal", "scale", 0.94, 1.0, "out_cubic", 24),
		layerDefinition("precision_spring_up", "position_y", 80.0, 0.0, "out_back", 24),
		textWaveDefinition("opacity_wave", "word", "opacity", []AnimationKeyframe{{0, 0.25}, {7, 1.0}, {17, 0.55}}, 3),
		textWaveDefinition("scale_wave", "word", "scale", []AnimationKeyframe{{0, 0.97}, {5, 1.02}, {12, 1.0}}, 3),
		textWaveDefinition("char_wave", "glyph", "position_y", []AnimationKeyframe{{0, 0.0}, {4, -10.0}, {10, 0.0}}, 1),
		textCenterDefinition(),
	} {
		_ = Register(d.ID, DeclarativePlugin{Definition: d})
	}
}

func layerDefinition(id, property string, from, to float64, easing string, enter int) MotionDefinition {
	return MotionDefinition{ID: id, Unit: "layer", Enter: enter, Tracks: []TrackDefinition{
		{Property: property, Easing: easing, Keyframes: []AnimationKeyframe{{Frame: 0, Value: from}, {Frame: int64(enter), Value: to}}},
		{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: int64(enter / 2), Value: 1.0}}},
	}}
}

func textWaveDefinition(id, unit, property string, keyframes []AnimationKeyframe, stagger int64) MotionDefinition {
	return MotionDefinition{ID: id, Unit: unit, Enter: 24, TextAnimators: []TextAnimatorDefinition{{
		ID: id + "_text", Selector: SelectorDefinition{Kind: unit, Stagger: stagger},
		Properties: []TrackDefinition{{Property: property, Easing: "in_out_sine", Keyframes: keyframes}},
	}}}
}

func textCenterDefinition() MotionDefinition {
	return MotionDefinition{ID: "center_expansion", Unit: "glyph", Enter: 24, TextAnimators: []TextAnimatorDefinition{{
		ID: "center_expansion_text", Selector: SelectorDefinition{Kind: "glyph", Shape: "smooth", Order: "from_center"},
		Properties: []TrackDefinition{
			{Property: "scale_x", Easing: "out_expo", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.72}, {Frame: 24, Value: 1.0}}},
			{Property: "tracking", Easing: "out_expo", Keyframes: []AnimationKeyframe{{Frame: 0, Value: -12.0}, {Frame: 24, Value: 2.0}}},
			{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 10, Value: 1.0}}},
		},
	}}}
}

func textDefinition(id, unit string, stagger, offset float64) MotionDefinition {
	if id == "tracking_collapse" || id == "tracking_expansion" {
		from, to := 32.0, 0.0
		if id == "tracking_expansion" {
			from, to = -3.0, 16.0
		}
		return MotionDefinition{ID: id, Unit: unit, Enter: 30, TextAnimators: []TextAnimatorDefinition{{
			ID: id + "_text", Selector: SelectorDefinition{Kind: unit, Stagger: int64(stagger)},
			Properties: []TrackDefinition{{Property: "tracking", Easing: "out_expo", Keyframes: []AnimationKeyframe{{Frame: 0, Value: from}, {Frame: 30, Value: to}}}, {Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 10, Value: 1.0}}}},
		}}}
	}
	if id == "blur_focus_in" {
		return MotionDefinition{ID: id, Unit: unit, Enter: 30, TextAnimators: []TextAnimatorDefinition{{
			ID: id + "_text", Selector: SelectorDefinition{Kind: unit},
			Properties: []TrackDefinition{{Property: "blur", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 18.0}, {Frame: 30, Value: 0.0}}}, {Property: "scale", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 1.04}, {Frame: 30, Value: 1.0}}}, {Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 10, Value: 1.0}}}},
		}}}
	}
	return MotionDefinition{ID: id, Unit: unit, Enter: 30, TextAnimators: []TextAnimatorDefinition{{
		ID: id + "_text", Selector: SelectorDefinition{Kind: unit, Stagger: int64(stagger)},
		Properties: []TrackDefinition{{Property: "position_y", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: offset}, {Frame: 30, Value: 0.0}}}, {Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 18, Value: 1.0}}}},
	}}}
}
