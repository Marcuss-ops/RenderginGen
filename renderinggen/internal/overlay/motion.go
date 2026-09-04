package overlay

import "github.com/Marcuss-ops/RenderginGen/renderinggen/internal/motion"

// The motion registry is the only authoring-motion → renderer-track lowering
// point. Chronon receives properties and keyframes, never motion names/units.
func resolveMotion(m MotionDefinition) []AnimationTrack {
	plugin, err := motion.Registry.Resolve(m.ID)
	if err != nil || plugin == nil {
		// Preset definitions from older callers have Name but no ID.
		m.ID = m.Name
	}
	plugin, err = motion.Registry.Resolve(m.ID)
	if err != nil || plugin == nil {
		return nil
	}
	tracks, err := plugin.Compile(motion.MotionContext{DurationFrames: int64(m.Enter)}, nil)
	if err != nil {
		return nil
	}
	return clampMotionTracks(fromMotionTracks(tracks), int64(m.Enter))
}

// clampMotionTracks keeps preset-authored keyframes inside the concrete
// layer duration. A semantic overlay can be shorter than the catalog's
// default enter duration (for example a brief spoken entity); Chronon rejects
// any keyframe beyond that layer/composition boundary.
func clampMotionTracks(tracks []AnimationTrack, duration int64) []AnimationTrack {
	if duration <= 0 {
		return tracks
	}
	for i := range tracks {
		for j := range tracks[i].Keyframes {
			if tracks[i].Keyframes[j].Frame > duration {
				tracks[i].Keyframes[j].Frame = duration
			}
		}
	}
	return tracks
}

func tracksForMotion(m MotionDefinition) []AnimationTrack {
	return resolveMotion(m)
}

func fromMotionTracks(src []motion.AnimationTrack) []AnimationTrack {
	tracks := make([]AnimationTrack, len(src))
	for i, t := range src {
		tracks[i] = AnimationTrack{Property: t.Property, Easing: t.Easing, Keyframes: make([]AnimationKeyframe, len(t.Keyframes))}
		for j, k := range t.Keyframes {
			tracks[i].Keyframes[j] = AnimationKeyframe{Frame: k.Frame, Value: k.Value}
		}
	}
	return tracks
}

func fromTrackDefinitions(src []motion.TrackDefinition) []AnimationTrack {
	result := make([]AnimationTrack, len(src))
	for i, t := range src {
		result[i] = AnimationTrack{Property: t.Property, Easing: t.Easing, Keyframes: make([]AnimationKeyframe, len(t.Keyframes))}
		for j, k := range t.Keyframes {
			result[i].Keyframes[j] = AnimationKeyframe{Frame: k.Frame, Value: k.Value}
		}
	}
	return result
}

func fromTextMotionDefinitions(src []motion.TextAnimatorDefinition, duration int64) []concreteTextAnimator {
	result := make([]concreteTextAnimator, 0, len(src))
	for _, definition := range src {
		selector := concreteTextSelector{
			ID: definition.ID + "_selector", Unit: definition.Selector.Kind,
			Shape: definition.Selector.Shape, Order: definition.Selector.Order, Combine: "replace",
			ExcludeSpaces: true,
		}
		if definition.Selector.Kind == "" {
			selector.Unit = "glyph"
		}
		if selector.Shape == "" {
			selector.Shape = "smooth"
		}
		if selector.Order == "" {
			selector.Order = "forward"
		}
		// A selector sweep is the renderer-neutral form of stagger: Chronon
		// evaluates the animated range per glyph/word every frame.
		// For reveals (word_reveal, character_cascade), animating start from 0 -> 100
		// with property values (opacity: 0, position_y: offset) makes glyphs start hidden/offset
		// and progressively drop to their baseline as start sweeps past them.
		if definition.Selector.Stagger > 0 {
			sweepDuration := duration
			if sweepDuration > 72 {
				sweepDuration = 72
			}
			selector.Start = &AnimationTrack{Property: "start", Easing: "out_cubic", Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: 0.0}, {Frame: sweepDuration, Value: 100.0},
			}}
		}
		animator := concreteTextAnimator{ID: definition.ID, Selectors: []concreteTextSelector{selector}, Properties: fromTrackDefinitions(definition.Properties)}
		result = append(result, animator)
	}
	return result
}

func resolveLayout(l PresetLayout, boxWidth, boxHeight, canvasWidth, canvasHeight int) []float64 {
	if boxWidth <= 0 {
		boxWidth = 320
	}
	if boxHeight <= 0 {
		boxHeight = 120
	}
	x, y := float64(canvasWidth-boxWidth)/2, float64(canvasHeight-boxHeight)/2
	switch l.Anchor {
	case "lower_third":
		x, y = 0.06*float64(canvasWidth), 0.76*float64(canvasHeight)
	case "safe_area":
		x, y = 0.06*float64(canvasWidth), 0.06*float64(canvasHeight)
	case "image_left":
		x = 0
	case "image_right":
		x = float64(canvasWidth - boxWidth)
	case "bottom_right":
		x, y = float64(canvasWidth-boxWidth), float64(canvasHeight-boxHeight)
	}
	// Image anchors describe the placement of the whole image card. Do not
	// let generic text alignment override an image anchor.
	if l.Alignment == "center" && l.Anchor != "image_left" && l.Anchor != "image_right" && l.Anchor != "bottom_right" {
		x = (float64(canvasWidth) - float64(boxWidth)) / 2
	}
	return []float64{x, y}
}
