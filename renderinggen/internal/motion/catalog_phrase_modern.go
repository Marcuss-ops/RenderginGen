package motion

// modernPhraseMotions is the additive modern v3 phrase batch: 26 new apple_v2
// ids authored beside the classic set, so nothing already published changes
// meaning.
//
// What makes this batch different from the classic one, in editor's terms:
//
//   - Overshoot is authored explicitly (out_back) instead of relying on the
//     generic out_cubic ease, so a stamp/snap/settle reads as physical.
//   - Effects are layered: a per-unit text curve (what each glyph/word does)
//     plus composition-level tracks (what the whole phrase does), which is what
//     keeps a preset visibly distinct from its neighbours on every backend.
//   - Selector ORDER is used as an effect of its own (from_center, reverse)
//     rather than only as a left-to-right stagger.
//
// Every id keeps the family's invariants: Enter 72 frames, Category "apple_v2",
// and at least one text animator plus layer tracks, so the registry's
// completeness test (every apple_v2 motion compiles with both a layer and a
// text animation) holds for the whole batch.
func modernPhraseMotions() []MotionDefinition {
	return []MotionDefinition{
		phraseAdvancedDefinition("liquid_glass_ripple", "glyph", "position_y", []AnimationKeyframe{{0, 14.0}, {24, -4.0}, {72, 0.0}}, 1,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.03), phraseKey(36, 1.0), phraseKey(72, 1.0)), phraseFadeIn(20)}),

		phraseAdvancedDefinition("kinetic_stamp_impact", "word", "scale", []AnimationKeyframe{{0, 1.45}, {8, 0.94}, {44, 1.0}}, 2,
			[]TrackDefinition{phraseTrack("scale", "out_back", phraseKey(0, 1.18), phraseKey(24, 1.0), phraseKey(72, 1.0)), phraseTrack("rotation_z", "out_back", phraseKey(0, -1.2), phraseKey(12, 0.4), phraseKey(44, 0.0), phraseKey(72, 0.0)), phraseFadeIn(12)}),

		phraseAdvancedDefinition("editorial_push_in", "line", "position_y", []AnimationKeyframe{{0, 36.0}, {30, 0.0}}, 0,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.10), phraseKey(40, 1.0), phraseKey(72, 1.0)), phraseFadeIn(18)}),

		// Opacity is the animated property here, so the primitive does not
		// append its own fade: the flicker pattern IS the reveal. The blur that
		// sells the ignition is a TEXT property — a layer track may not carry
		// blur (its vocabulary is geometric only).
		phraseTextProperty(phraseAdvancedDefinition("neon_flicker_ignite", "glyph", "opacity", []AnimationKeyframe{{0, 0.0}, {6, 0.9}, {10, 0.25}, {16, 1.0}, {72, 1.0}}, 1,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.04), phraseKey(30, 1.0), phraseKey(72, 1.0))}),
			"blur", "out_cubic", phraseKey(0, 16.0), phraseKey(30, 0.0), phraseKey(72, 0.0)),

		phraseAdvancedDefinition("counter_scroll_reveal", "line", "position_y", []AnimationKeyframe{{0, 44.0}, {26, -4.0}, {72, 0.0}}, 0,
			[]TrackDefinition{phraseTrack("position_y", "out_cubic", phraseKey(0, -44.0), phraseKey(26, 4.0), phraseKey(72, 0.0)), phraseTrack("rotation_z", "out_cubic", phraseKey(0, 2.0), phraseKey(26, -0.6), phraseKey(72, 0.0)), phraseFadeIn(16)}),

		phraseSelector(phraseAdvancedDefinition("hologram_scanline_build", "glyph", "position_y", []AnimationKeyframe{{0, 30.0}, {24, 0.0}}, 1,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.02), phraseKey(32, 1.0), phraseKey(72, 1.0)), phraseTrack("position_y", "out_cubic", phraseKey(0, 12.0), phraseKey(32, 0.0), phraseKey(72, 0.0)), phraseFadeIn(24)}), "forward", "ramp_up"),

		phraseAdvancedDefinition("soft_clay_press", "line", "scale", []AnimationKeyframe{{0, 1.08}, {16, 0.97}, {48, 1.0}}, 0,
			[]TrackDefinition{phraseTrack("scale", "out_back", phraseKey(0, 1.06), phraseKey(16, 0.99), phraseKey(48, 1.0), phraseKey(72, 1.0)), phraseTrack("position_y", "out_cubic", phraseKey(0, 8.0), phraseKey(32, 0.0), phraseKey(72, 0.0)), phraseFadeIn(14)}),

		phraseAdvancedDefinition("underline_swipe_bold", "line", "scale_x", []AnimationKeyframe{{0, 0.2}, {30, 1.0}}, 0,
			[]TrackDefinition{phraseTrack("position_x", "out_expo", phraseKey(0, -24.0), phraseKey(30, 2.0), phraseKey(72, 0.0)), phraseTrack("scale", "out_cubic", phraseKey(0, 1.02), phraseKey(30, 1.0), phraseKey(72, 1.0)), phraseFadeIn(12)}),

		phraseSelector(phraseAdvancedDefinition("magnetic_letters_converge", "glyph", "position_x", []AnimationKeyframe{{0, -60.0}, {72, 0.0}}, 0,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.06), phraseKey(36, 1.0), phraseKey(72, 1.0)), phraseFadeIn(24)}), "from_center", "smooth"),

		phraseAdvancedDefinition("spectrum_shimmer_wave", "glyph", "scale", []AnimationKeyframe{{0, 0.86}, {36, 1.12}, {72, 1.0}}, 1,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.02), phraseKey(36, 1.0), phraseKey(72, 1.0)), phraseFadeIn(20)}),

		phraseAdvancedDefinition("letterbox_wipe", "line", "scale_x", []AnimationKeyframe{{0, 0.15}, {36, 1.0}}, 0,
			[]TrackDefinition{phraseTrack("position_y", "out_cubic", phraseKey(0, 6.0), phraseKey(36, 0.0), phraseKey(72, 0.0)), phraseFadeIn(10)}),

		phraseAdvancedDefinition("focus_pull_macro", "glyph", "blur", []AnimationKeyframe{{0, 26.0}, {40, 0.0}}, 0,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.06), phraseKey(40, 1.0), phraseKey(72, 1.0)), phraseFadeIn(20)}),

		phraseAdvancedDefinition("risograph_offset_print", "line", "position_x", []AnimationKeyframe{{0, -18.0}, {18, 6.0}, {44, 0.0}}, 0,
			[]TrackDefinition{phraseTrack("position_x", "out_cubic", phraseKey(0, -10.0), phraseKey(18, 4.0), phraseKey(44, 0.0), phraseKey(72, 0.0)), phraseTrack("rotation_z", "out_cubic", phraseKey(0, -2.0), phraseKey(44, 0.6), phraseKey(72, 0.0)), phraseFadeIn(12)}),

		// The stagger is what makes this an iris rather than a whole-layer fade:
		// the selector sweep opens outwards from the center of the line.
		phraseSelector(phraseAdvancedDefinition("spotlight_iris_open", "glyph", "opacity", []AnimationKeyframe{{0, 0.0}, {40, 1.0}}, 1,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.04), phraseKey(40, 1.0), phraseKey(72, 1.0)), phraseFadeIn(18)}), "from_center", "smooth"),

		phraseSelector(phraseAdvancedDefinition("air_rise_type_on", "glyph", "position_y", []AnimationKeyframe{{0, 26.0}, {60, 0.0}}, 1,
			[]TrackDefinition{phraseTrack("position_y", "out_cubic", phraseKey(0, 10.0), phraseKey(60, 0.0), phraseKey(72, 0.0)), phraseFadeIn(26)}), "forward", "ramp_up"),

		phraseAdvancedDefinition("glitch_slice_band", "line", "position_x", []AnimationKeyframe{{0, 26.0}, {8, -14.0}, {16, 10.0}, {30, -4.0}, {48, 0.0}}, 0,
			[]TrackDefinition{phraseTrack("scale_x", "out_cubic", phraseKey(0, 1.30), phraseKey(30, 1.0), phraseKey(72, 1.0)), phraseFadeIn(8)}),

		phraseAdvancedDefinition("parallax_depth_stack", "line", "position_y", []AnimationKeyframe{{0, 30.0}, {34, 0.0}}, 0,
			[]TrackDefinition{phraseTrack("position_y", "out_cubic", phraseKey(0, -16.0), phraseKey(34, 6.0), phraseKey(72, 0.0)), phraseTrack("rotation_z", "out_cubic", phraseKey(0, 2.0), phraseKey(34, -0.5), phraseKey(72, 0.0)), phraseTrack("scale", "out_cubic", phraseKey(0, 0.94), phraseKey(34, 1.0), phraseKey(72, 1.0))}),

		phraseAdvancedDefinition("cinematic_credits_drift", "line", "position_y", []AnimationKeyframe{{0, 22.0}, {72, -2.0}}, 0,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 0.99), phraseKey(72, 1.0)), phraseFadeIn(24)}),

		phraseAdvancedDefinition("ink_bleed_spread", "glyph", "tracking", []AnimationKeyframe{{0, -8.0}, {30, 4.0}, {72, 0.0}}, 1,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.04), phraseKey(40, 1.0), phraseKey(72, 1.0)), phraseFadeIn(18)}),

		phraseAdvancedDefinition("pulse_emphasis_beat", "line", "scale", []AnimationKeyframe{{0, 0.9}, {12, 1.06}, {24, 1.0}, {40, 1.03}, {72, 1.0}}, 0,
			[]TrackDefinition{phraseTrack("scale", "out_back", phraseKey(0, 0.96), phraseKey(24, 1.0), phraseKey(72, 1.0)), phraseFadeIn(12)}),

		phraseAdvancedDefinition("aurora_gradient_sweep", "glyph", "tracking", []AnimationKeyframe{{0, -6.0}, {36, 7.0}, {72, -0.5}}, 1,
			[]TrackDefinition{phraseTrack("position_x", "in_out_sine", phraseKey(0, -30.0), phraseKey(36, 22.0), phraseKey(72, 0.0)), phraseTrack("scale", "out_cubic", phraseKey(0, 0.98), phraseKey(36, 1.02), phraseKey(72, 1.0)), phraseFadeIn(20)}),

		phraseAdvancedDefinition("vertical_reel_snap", "line", "position_y", []AnimationKeyframe{{0, 60.0}, {26, -12.0}, {44, 4.0}, {72, 0.0}}, 0,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.04), phraseKey(44, 1.0), phraseKey(72, 1.0)), phraseTrack("rotation_z", "out_cubic", phraseKey(0, 1.5), phraseKey(44, -0.4), phraseKey(72, 0.0)), phraseFadeIn(10)}),

		phraseSelector(phraseAdvancedDefinition("weightless_float_settle", "glyph", "position_y", []AnimationKeyframe{{0, -20.0}, {72, 0.0}}, 2,
			[]TrackDefinition{phraseTrack("position_y", "out_cubic", phraseKey(0, -8.0), phraseKey(48, 0.0), phraseKey(72, 0.0)), phraseFadeIn(20)}), "reverse", "smooth"),

		phraseAdvancedDefinition("crisp_mask_center_open", "word", "scale_x", []AnimationKeyframe{{0, 0.35}, {34, 1.0}}, 0,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.02), phraseKey(34, 1.0), phraseKey(72, 1.0)), phraseFadeIn(16)}),

		phraseAdvancedDefinition("duotone_block_rise", "line", "position_y", []AnimationKeyframe{{0, 70.0}, {40, 0.0}}, 0,
			[]TrackDefinition{phraseTrack("position_y", "out_cubic", phraseKey(0, 24.0), phraseKey(40, 0.0), phraseKey(72, 0.0)), phraseTrack("scale", "out_cubic", phraseKey(0, 1.02), phraseKey(40, 1.0), phraseKey(72, 1.0)), phraseFadeIn(14)}),

		phraseSelector(phraseAdvancedDefinition("shutter_blade_reveal", "glyph", "scale_x", []AnimationKeyframe{{0, 0.2}, {30, 1.0}}, 1,
			[]TrackDefinition{phraseTrack("scale", "out_cubic", phraseKey(0, 1.03), phraseKey(30, 1.0), phraseKey(72, 1.0)), phraseFadeIn(12)}), "forward", "square"),
	}
}

// phraseFadeIn is the composition-level fade every modern phrase carries so the
// reveal is visible even on a backend that ignores the per-glyph curve.
func phraseFadeIn(frames int64) TrackDefinition {
	return phraseTrack("opacity", "out_cubic", phraseKey(0, 0.0), phraseKey(frames, 1.0))
}
