package motion

// phraseMotions is the apple_v2 editorial phrase library: every definition here
// declares Category "apple_v2", carries a per-unit (glyph/word/line) text
// animator AND composition-level layer tracks, so the same id renders as a
// visibly different effect on every backend.
//
// The family is split in two files:
//
//	catalog_phrase.go        the helpers and the classic phrase set
//	catalog_phrase_modern.go the additive modern v3 batch
//
// The classic ids are the ones already published and used by the Apple style
// presets (apple_phrase_v2, kinetic_split_word, ...), so they are frozen: the
// modern batch adds NEW ids beside them rather than retuning the ones whose
// renders were already certified.
func phraseMotions() []MotionDefinition {
	return append(classicPhraseMotions(), modernPhraseMotions()...)
}

func classicPhraseMotions() []MotionDefinition {
	return []MotionDefinition{
		phraseAdvancedDefinition("apple_phrase_v2", "glyph", "position_y", []AnimationKeyframe{{0, 18.0}, {24, -2.0}, {72, 0.0}}, 1,
			[]TrackDefinition{phraseLayer("position_y", 24.0, -3.0, 0.0), phraseLayer("scale", 0.97, 1.02, 1.0)}),
		phraseAdvancedDefinition("kinetic_split_word", "word", "position_y", []AnimationKeyframe{{0, 22.0}, {28, -5.0}, {72, 0.0}}, 1,
			[]TrackDefinition{phraseLayer("position_y", 28.0, -6.0, 0.0), phraseLayer("rotation_z", -3.0, 1.0, 0.0)}),
		phraseAdvancedDefinition("dynamic_island_expansion", "line", "scale", []AnimationKeyframe{{0, 0.82}, {28, 1.04}, {72, 1.0}}, 0,
			[]TrackDefinition{phraseLayer("scale", 0.78, 1.04, 1.0)}),
		phraseAdvancedDefinition("masked_upward_reveal", "line", "position_y", []AnimationKeyframe{{0, 48.0}, {22, 0.0}}, 0,
			[]TrackDefinition{phraseLayer("position_y", 48.0, 4.0, 0.0), phraseLayer("rotation_z", 3.0, -1.0, 0.0)}),
		phraseAdvancedDefinition("staggered_char_float", "glyph", "position_y", []AnimationKeyframe{{0, 26.0}, {72, 0.0}}, 2,
			[]TrackDefinition{phraseLayer("position_y", 32.0, -6.0, 0.0), phraseLayer("scale", 0.90, 1.03, 1.0), phraseLayer("rotation_z", -4.0, 2.0, 0.0)}),
		phraseAdvancedDefinition("high_specular_light_sweep", "glyph", "tracking", []AnimationKeyframe{{0, 18.0}, {34, -2.0}, {72, 3.0}}, 1,
			[]TrackDefinition{phraseLayer("position_x", -48.0, 24.0, 0.0), phraseLayer("scale", 1.05, 0.98, 1.0)}),
		phraseAdvancedDefinition("depth_of_field_rack_focus", "glyph", "blur", []AnimationKeyframe{{0, 24.0}, {72, 0.0}}, 0,
			[]TrackDefinition{phraseLayer("scale", 1.06, 1.01, 1.0), phraseLayer("position_y", 10.0, -3.0, 0.0), phraseLayer("opacity", 0.65, 1.0, 1.0)}),
		phraseAdvancedDefinition("micro_tracker_kerning_compression", "glyph", "tracking", []AnimationKeyframe{{0, 28.0}, {72, -2.0}}, 0,
			[]TrackDefinition{phraseLayer("scale", 0.88, 1.03, 1.0), phraseLayer("position_x", 24.0, -12.0, 0.0)}),
		phraseAdvancedDefinition("isometric_3d_fold", "glyph", "position_y", []AnimationKeyframe{{0, 44.0}, {25, -4.0}, {72, 0.0}}, 1,
			[]TrackDefinition{phraseLayer("rotation_z", -8.0, 4.0, 0.0), phraseLayer("position_y", 44.0, -4.0, 0.0), phraseLayer("scale", 0.92, 1.02, 1.0)}),
		phraseAdvancedDefinition("soft_edge_spotlight_dissolve", "glyph", "opacity", []AnimationKeyframe{{0, 0.0}, {24, 1.0}}, 1,
			[]TrackDefinition{phraseLayer("scale", 1.06, 1.01, 1.0), phraseLayer("opacity", 0.0, 1.0, 1.0)}),
		phraseAdvancedDefinition("chromatic_aberration_pop", "glyph", "position_x", []AnimationKeyframe{{0, -12.0}, {12, 8.0}, {28, 0.0}}, 1,
			[]TrackDefinition{phraseLayer("position_x", -40.0, 18.0, 0.0), phraseLayer("rotation_z", -3.0, 1.0, 0.0), phraseLayer("scale", 0.90, 1.04, 1.0)}),
		phraseAdvancedDefinition("fluid_gradient_text_flow", "glyph", "tracking", []AnimationKeyframe{{0, -5.0}, {36, 6.0}, {72, -1.0}}, 1,
			[]TrackDefinition{phraseLayer("position_x", -48.0, 48.0, 0.0), phraseLayer("scale", 0.98, 1.02, 1.0)}),
		phraseAdvancedDefinition("velocity_inertia_snap", "line", "position_x", []AnimationKeyframe{{0, 48.0}, {42, -8.0}, {58, 2.0}, {72, 0.0}}, 0,
			[]TrackDefinition{phraseLayer("position_x", 64.0, -12.0, 2.0, 0.0), phraseLayer("rotation_z", 6.0, -2.0, 1.0, 0.0)}),
		phraseAdvancedDefinition("vertical_rolling_counter", "glyph", "position_y", []AnimationKeyframe{{0, 36.0}, {48, -6.0}, {72, 0.0}}, 1,
			[]TrackDefinition{phraseLayer("position_y", 48.0, -6.0, 0.0), phraseLayer("rotation_z", 4.0, -2.0, 0.0)}),
		phraseAdvancedDefinition("glassmorphism_card_tilt", "glyph", "scale", []AnimationKeyframe{{0, 0.92}, {24, 1.02}, {72, 1.0}}, 1,
			[]TrackDefinition{phraseLayer("rotation_z", -6.0, 4.0, 0.0), phraseLayer("position_x", -28.0, 20.0, 0.0), phraseLayer("scale", 0.96, 1.02, 1.0)}),
		phraseAdvancedDefinition("pixel_grid_alpha_matrix", "glyph", "blur", []AnimationKeyframe{{0, 18.0}, {28, 3.0}, {72, 0.0}}, 1,
			[]TrackDefinition{phraseLayer("scale", 0.86, 1.06, 1.0), phraseLayer("position_y", 16.0, -3.0, 0.0), phraseLayer("opacity", 0.0, 1.0, 1.0)}),
	}
}

// phraseAdvancedDefinition is the renderer-neutral phrase preset primitive.
// The names describe the editorial effect; the implementation intentionally
// stays inside Chronon's supported per-glyph property set so renders remain
// deterministic and frame-addressable.
//
// layerTracks are the composition-level tracks that make the effect visible on
// all backends. They are passed in by the caller (one row per preset, right
// next to its animator definition), so the phrase vocabulary is declared once
// here and never re-switched by a lookup that could silently return nil for an
// unknown id.
func phraseAdvancedDefinition(id, unit, property string, keyframes []AnimationKeyframe, stagger int64, layerTracks []TrackDefinition) MotionDefinition {
	properties := []TrackDefinition{{Property: property, Easing: "out_cubic", Keyframes: keyframes}}
	if property != "opacity" {
		properties = append(properties, TrackDefinition{
			Property: "opacity", Easing: "out_cubic",
			Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 18, Value: 1.0}},
		})
	}
	return MotionDefinition{ID: id, Category: "apple_v2", Targets: []string{"text", "phrase"}, Unit: unit, Enter: 72, Tracks: layerTracks, TextAnimators: []TextAnimatorDefinition{{
		ID: id + "_text", Selector: SelectorDefinition{Kind: unit, Stagger: stagger}, Properties: properties,
	}}}
}

// phraseSelector overrides the selector of a single-animator phrase motion.
// Every phrase definition owns exactly one text animator, so the sweep's ORDER
// (forward, reverse, from_center, to_center, random) and SHAPE (square,
// ramp_up, ramp_down, triangle, round, smooth — the renderer's closed
// vocabulary, see chronon.render-plan.v2 / $defs/text_selector) are properties
// of that one selector rather than extra definition fields.
func phraseSelector(d MotionDefinition, order, shape string) MotionDefinition {
	if len(d.TextAnimators) == 0 {
		return d
	}
	d.TextAnimators[0].Selector.Order = order
	d.TextAnimators[0].Selector.Shape = shape
	return d
}

// phraseTextProperty appends one more per-unit property track to a phrase
// motion's single text animator.
//
// It exists because the property enums of a text animator and of a layer are NOT
// the same set: an animator may animate blur and tracking, a layer may not (its
// tracks are geometric only). An effect that needs one of those two therefore
// belongs on the animator, and this is how a definition says so without
// hand-rolling an animator literal.
func phraseTextProperty(d MotionDefinition, property, easing string, keyframes ...AnimationKeyframe) MotionDefinition {
	if len(d.TextAnimators) == 0 {
		return d
	}
	d.TextAnimators[0].Properties = append(d.TextAnimators[0].Properties, TrackDefinition{Property: property, Easing: easing, Keyframes: keyframes})
	return d
}

// phraseLayer builds one composition-level track for a phrase preset: keyframes
// are spaced 24 frames apart with the last pinned at frame 72.
func phraseLayer(property string, values ...any) TrackDefinition {
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

// phraseTrack and phraseKey are the explicit-keyframe form the modern batch is
// authored with: phraseLayer spaces its frames on a fixed 24-frame grid, which
// cannot express an overshoot that peaks at frame 12 or a two-beat settle.
func phraseTrack(property, easing string, keyframes ...AnimationKeyframe) TrackDefinition {
	return TrackDefinition{Property: property, Easing: easing, Keyframes: keyframes}
}

func phraseKey(frame int64, value any) AnimationKeyframe {
	return AnimationKeyframe{Frame: frame, Value: value}
}
