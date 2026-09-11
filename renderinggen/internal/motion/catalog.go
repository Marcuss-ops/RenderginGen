package motion

func init() {
	for _, d := range []MotionDefinition{
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
		textDefinition("word_reveal", "word", 1, 40),
		textDefinition("character_cascade", "glyph", 1, 40),
		textDefinition("tracking_collapse", "word", 0, 0),
		textDefinition("tracking_expansion", "word", 0, 0),
		textDefinition("blur_focus_in", "glyph", 0, 0),
		layerDefinition("soft_scale_reveal", "scale", 0.88, 1.0, "out_cubic", 60),
		layerDefinition("precision_spring_up", "position_y", 120.0, 0.0, "out_back", 60),
		textWaveDefinition("opacity_wave", "word", "opacity", []AnimationKeyframe{{0, 0.15}, {36, 1.0}, {72, 0.5}}, 1),
		textWaveDefinition("scale_wave", "glyph", "scale", []AnimationKeyframe{{0, 0.75}, {36, 1.25}, {72, 1.0}}, 1),
		textWaveDefinition("char_wave", "glyph", "position_y", []AnimationKeyframe{{0, 0.0}, {24, -30.0}, {48, 15.0}, {72, 0.0}}, 1),
		textCenterDefinition(),
		phraseAdvancedDefinition("kinetic_split_word", "word", "position_y", []AnimationKeyframe{{0, 22.0}, {28, -5.0}, {72, 0.0}}, 1),
		phraseAdvancedDefinition("dynamic_island_expansion", "line", "scale", []AnimationKeyframe{{0, 0.82}, {28, 1.04}, {72, 1.0}}, 0),
		phraseAdvancedDefinition("masked_upward_reveal", "line", "position_y", []AnimationKeyframe{{0, 120.0}, {22, 0.0}}, 0),
		phraseAdvancedDefinition("staggered_char_float", "glyph", "position_y", []AnimationKeyframe{{0, 26.0}, {72, 0.0}}, 2),
		phraseAdvancedDefinition("high_specular_light_sweep", "glyph", "tracking", []AnimationKeyframe{{0, 18.0}, {34, -2.0}, {72, 3.0}}, 1),
		phraseAdvancedDefinition("depth_of_field_rack_focus", "glyph", "blur", []AnimationKeyframe{{0, 24.0}, {72, 0.0}}, 0),
		phraseAdvancedDefinition("micro_tracker_kerning_compression", "glyph", "tracking", []AnimationKeyframe{{0, 28.0}, {72, -2.0}}, 0),
		phraseAdvancedDefinition("isometric_3d_fold", "glyph", "position_y", []AnimationKeyframe{{0, 90.0}, {25, -4.0}, {72, 0.0}}, 1),
		phraseAdvancedDefinition("soft_edge_spotlight_dissolve", "glyph", "opacity", []AnimationKeyframe{{0, 0.0}, {24, 1.0}}, 1),
		phraseAdvancedDefinition("chromatic_aberration_pop", "glyph", "position_x", []AnimationKeyframe{{0, -12.0}, {12, 8.0}, {28, 0.0}}, 1),
		phraseAdvancedDefinition("fluid_gradient_text_flow", "glyph", "tracking", []AnimationKeyframe{{0, -5.0}, {36, 6.0}, {72, -1.0}}, 1),
		phraseAdvancedDefinition("velocity_inertia_snap", "line", "position_x", []AnimationKeyframe{{0, 620.0}, {42, -10.0}, {58, 3.0}, {72, 0.0}}, 0),
		phraseAdvancedDefinition("vertical_rolling_counter", "glyph", "position_y", []AnimationKeyframe{{0, 80.0}, {48, -8.0}, {72, 0.0}}, 1),
		phraseAdvancedDefinition("glassmorphism_card_tilt", "glyph", "scale", []AnimationKeyframe{{0, 0.92}, {24, 1.02}, {72, 1.0}}, 1),
		phraseAdvancedDefinition("pixel_grid_alpha_matrix", "glyph", "blur", []AnimationKeyframe{{0, 18.0}, {28, 3.0}, {72, 0.0}}, 1),
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
	} {
		_ = Register(d.ID, DeclarativePlugin{Definition: d})
	}
}

// phraseAdvancedDefinition is the renderer-neutral phrase preset primitive.
// The names describe the editorial effect; the implementation intentionally
// stays inside Chronon's supported per-glyph property set so renders remain
// deterministic and frame-addressable.
func phraseAdvancedDefinition(id, unit, property string, keyframes []AnimationKeyframe, stagger int64) MotionDefinition {
	properties := []TrackDefinition{{Property: property, Easing: "out_cubic", Keyframes: keyframes}}
	if property != "opacity" {
		properties = append(properties, TrackDefinition{
			Property: "opacity", Easing: "out_cubic",
			Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 18, Value: 1.0}},
		})
	}
	return MotionDefinition{ID: id, Unit: unit, Enter: 72, Tracks: phraseLayerTracks(id), TextAnimators: []TextAnimatorDefinition{{
		ID: id + "_text", Selector: SelectorDefinition{Kind: unit, Stagger: stagger}, Properties: properties,
	}}}
}

// phraseLayerTracks guarantees a visible composition-level motion for every
// phrase preset. Text animators remain attached for glyph/word detail, while
// these tracks make the first three seconds observable on all backends.
func phraseLayerTracks(id string) []TrackDefinition {
	track := func(property string, values ...any) TrackDefinition {
		keyframes := make([]AnimationKeyframe, 0, len(values))
		for i, value := range values {
			frame := int64(i * 24)
			if i == len(values)-1 {
				frame = 72
			}
			keyframes = append(keyframes, AnimationKeyframe{Frame: frame, Value: value})
		}
		return TrackDefinition{Property: property, Easing: "out_cubic", Keyframes: keyframes}
	}
	switch id {
	case "kinetic_split_word":
		return []TrackDefinition{track("position_y", 80.0, -12.0, 0.0), track("rotation_z", -7.0, 2.0, 0.0)}
	case "dynamic_island_expansion":
		return []TrackDefinition{track("scale", 0.38, 1.12, 1.0)}
	case "masked_upward_reveal":
		return []TrackDefinition{track("position_y", 260.0, 18.0, 0.0), track("rotation_z", 9.0, -1.0, 0.0)}
	case "staggered_char_float":
		return []TrackDefinition{track("position_y", 110.0, -10.0, 0.0), track("scale", 0.72, 1.08, 1.0), track("rotation_z", -12.0, 3.0, 0.0)}
	case "high_specular_light_sweep":
		return []TrackDefinition{track("position_x", -420.0, 140.0, 0.0), track("scale", 1.18, 0.96, 1.0)}
	case "depth_of_field_rack_focus":
		return []TrackDefinition{track("scale", 1.28, 1.04, 1.0), track("position_y", 26.0, -7.0, 0.0), track("opacity", 0.12, 0.72, 1.0)}
	case "micro_tracker_kerning_compression":
		return []TrackDefinition{track("scale", 0.42, 1.10, 1.0), track("position_x", 70.0, -10.0, 0.0)}
	case "isometric_3d_fold":
		return []TrackDefinition{track("rotation_z", -28.0, 8.0, 0.0), track("position_y", 180.0, -18.0, 0.0), track("scale", 0.82, 1.03, 1.0)}
	case "soft_edge_spotlight_dissolve":
		return []TrackDefinition{track("scale", 1.34, 1.04, 1.0), track("opacity", 0.0, 0.45, 1.0)}
	case "chromatic_aberration_pop":
		return []TrackDefinition{track("position_x", -180.0, 34.0, 0.0), track("rotation_z", -5.0, 2.0, 0.0), track("scale", 0.70, 1.10, 1.0)}
	case "fluid_gradient_text_flow":
		return []TrackDefinition{track("position_x", -260.0, 260.0, 0.0), track("scale", 0.92, 1.08, 1.0)}
	case "velocity_inertia_snap":
		return []TrackDefinition{track("position_x", 760.0, -22.0, 8.0, 0.0), track("rotation_z", 18.0, -4.0, 1.0, 0.0)}
	case "vertical_rolling_counter":
		return []TrackDefinition{track("position_y", 360.0, -24.0, 0.0), track("rotation_z", 20.0, -6.0, 0.0)}
	case "glassmorphism_card_tilt":
		return []TrackDefinition{track("rotation_z", -16.0, 8.0, 0.0), track("position_x", -70.0, 34.0, 0.0), track("scale", 0.90, 1.04, 1.0)}
	case "pixel_grid_alpha_matrix":
		return []TrackDefinition{track("scale", 0.34, 1.18, 1.0), track("position_y", 34.0, -4.0, 0.0), track("opacity", 0.0, 0.55, 1.0)}
	default:
		return nil
	}
}

func layerDefinition(id, property string, from, to float64, easing string, enter int) MotionDefinition {
	return MotionDefinition{ID: id, Unit: "layer", Enter: enter, Tracks: []TrackDefinition{
		{Property: property, Easing: easing, Keyframes: []AnimationKeyframe{{Frame: 0, Value: from}, {Frame: int64(enter), Value: to}}},
		{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: int64(enter * 2 / 3), Value: 1.0}}},
	}}
}

func textWaveDefinition(id, unit, property string, keyframes []AnimationKeyframe, stagger int64) MotionDefinition {
	return MotionDefinition{ID: id, Unit: unit, Enter: 72, TextAnimators: []TextAnimatorDefinition{{
		ID: id + "_text", Selector: SelectorDefinition{Kind: unit, Stagger: stagger},
		Properties: []TrackDefinition{{Property: property, Easing: "in_out_sine", Keyframes: keyframes}},
	}}}
}

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
