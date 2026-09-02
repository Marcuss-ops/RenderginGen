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
	return fromMotionTracks(tracks)
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
			Shape: "smooth", Order: "forward", Combine: "replace",
			ExcludeSpaces: true,
		}
		if definition.Selector.Kind == "" {
			selector.Unit = "glyph"
		}
		// A selector sweep is the renderer-neutral form of stagger: Chronon
		// evaluates the animated end range per glyph/word every frame.
		if definition.Selector.Stagger > 0 {
			selector.End = &AnimationTrack{Property: "end", Easing: "out_cubic", Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: 0.0}, {Frame: duration, Value: 100.0},
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
	if l.Alignment == "center" {
		x = (float64(canvasWidth) - float64(boxWidth)) / 2
	}
	return []float64{x, y}
}
