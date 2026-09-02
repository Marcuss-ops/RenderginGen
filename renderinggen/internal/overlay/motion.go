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
