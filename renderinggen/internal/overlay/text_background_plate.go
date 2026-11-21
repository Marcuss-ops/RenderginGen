package overlay

// text_background_plate.go closes the gap between the overlay contract and the
// Chronon renderer: the plan's text style.background is lowered by the text
// compiler into `box_style`, but no Chronon renderer path reads
// `box_style.enabled`/`box_style.background` (only per-glyph highlight boxes are
// drawn). A payload that asked for `card_background` therefore validated and
// then rendered nothing.
//
// The supported primitive is a native shape layer. This pass runs once after
// every semantic item has been compiled, so it applies uniformly to a standalone
// phrase, an entity caption and a chain segment. It inserts a rounded rect
// immediately before the text layer, copies the text's lifetime and layer
// animation (opacity/position entrance) onto the plate, and clears the dead
// text-box field.
//
// Only runtime card_background entries carry LiftToShape. Preset-authored text
// backgrounds keep their historical wire form: lifting them would add layers to
// plans that never asked for the new visual and would change their goldens.

import (
	"math"
	"strconv"
)

func liftTextBackgroundPlates(plan *Plan) {
	if plan == nil || len(plan.Layers) == 0 {
		return
	}
	usedIDs := make(map[string]struct{}, len(plan.Layers))
	for _, layer := range plan.Layers {
		usedIDs[layer.ID] = struct{}{}
	}
	out := make([]Layer, 0, len(plan.Layers))
	plateIndex := 0
	for _, layer := range plan.Layers {
		plate, ok := textBackgroundPlate(&layer, plan.Canvas, usedIDs, plateIndex)
		if !ok {
			out = append(out, layer)
			continue
		}
		plateIndex++
		layer.Style.Background = nil
		out = append(out, plate, layer)
		usedIDs[plate.ID] = struct{}{}
	}
	plan.Layers = out
}

func textBackgroundPlate(layer *Layer, canvas Canvas, usedIDs map[string]struct{}, plateIndex int) (Layer, bool) {
	// A captioned entity can select a camera-backed caption motion. That text
	// layer is still a text card and still owns style.background; skipping it
	// here left the first 3D captions with the dead box_style field while the
	// flat captions after them showed their plate. Lift both, preserving the
	// layer's space and camera requirement on the plate.
	if layer.Type != "text" || layer.Style == nil || layer.Style.Background == nil {
		return Layer{}, false
	}
	background := layer.Style.Background
	if !background.LiftToShape {
		return Layer{}, false
	}
	color, err := parseHexColor(background.Color)
	if err != nil || len(color) != 4 {
		return Layer{}, false
	}
	width, height := textPlateBox(*layer)
	if width <= 0 || height <= 0 {
		return Layer{}, false
	}
	padX, padY := textPlatePadding(background.Padding)
	width += 2 * padX
	height += 2 * padY
	if !finite(width) || !finite(height) || width <= 0 || height <= 0 {
		return Layer{}, false
	}
	radius := background.Radius
	if !finite(radius) || radius < 0 {
		radius = 0
	}
	if maxRadius := math.Min(width, height) / 2; radius > maxRadius {
		radius = maxRadius
	}
	// Text positions are canvas-absolute centres; shape positions are
	// canvas-absolute top-left corners. Convert once here. Subtracting half the
	// canvas instead lands the plate on the left edge (r18).
	position := []float64{0, 0}
	if len(layer.Position) >= 2 {
		position = []float64{layer.Position[0] - width/2, layer.Position[1] - height/2}
	}
	opacity := 1.0
	if background.Opacity != nil && finite(*background.Opacity) && *background.Opacity >= 0 && *background.Opacity <= 1 {
		opacity = *background.Opacity
	}
	// The plate must fade with its text when a producer set a static layer
	// opacity (for example a documentary phrase split into 0.88-opacity runs).
	// Animation tracks are copied below; the static value is not an animation
	// track and would otherwise leave an opaque plate behind transparent text.
	if layer.Opacity != nil && finite(*layer.Opacity) && *layer.Opacity >= 0 && *layer.Opacity <= 1 {
		opacity *= *layer.Opacity
	}
	shapeType := "rect"
	if radius > 0 {
		shapeType = "rounded_rect"
	}
	plate := Layer{
		ID:             uniquePlateID(layer.ID+":plate", usedIDs),
		Type:           "shape",
		Size:           []float64{width, height},
		Position:       position,
		StartFrame:     layer.StartFrame,
		DurationFrames: layer.DurationFrames,
		ScreenSpace:    layer.ScreenSpace,
		Enable3D:       layer.Enable3D,
		Opacity:        floatPointer(opacity),
		Shape:          &LayerShape{Type: shapeType, Fill: []float64{color[0], color[1], color[2], color[3]}, Radius: radius, Stroke: clonePlateStroke(background.Stroke)},
		Parent:         layer.Parent,
	}
	if background.Shadow != nil {
		shadow := *background.Shadow
		shadow.Offset = append([]float64(nil), background.Shadow.Offset...)
		plate.Style = &LayerStyle{Shadow: &shadow}
	}
	if motion := plateMotionForIndex(background, plateIndex); motion != "" {
		enter := int64(plateMotionDefaultEnterFrames)
		if layer.DurationFrames > 0 && enter > layer.DurationFrames/2 {
			enter = layer.DurationFrames / 2
		}
		if enter < 1 {
			enter = 1
		}
		if animation, ok := plateEntranceAnimation(motion, width, enter, layer.StartFrame); ok {
			plate.Animation = animation
		}
		// The plate entrance owns the card. Starting the label on the frame the
		// plate settles keeps it (and its shadow/glow) from hanging outside a
		// half-drawn or half-scaled rectangle. A short local fade softens the
		// arrival without re-introducing a card track that fights the plate.
		if layer.DurationFrames > enter {
			layer.StartFrame += enter
			layer.DurationFrames -= enter
		}
		revealPlateTextAfterEntrance(layer)
	} else if layer.Animation != nil {
		plate.Animation = &LayerAnimation{Tracks: cloneAnimationTracks(layer.Animation.Tracks)}
	}
	if layer.TransitionIn != nil {
		transition := *layer.TransitionIn
		plate.TransitionIn = &transition
	}
	return plate, true
}

func textPlateBox(layer Layer) (float64, float64) {
	if len(layer.Size) == 2 && finite(layer.Size[0]) && finite(layer.Size[1]) {
		return layer.Size[0], layer.Size[1]
	}
	return float64(layer.BoxWidth), float64(layer.BoxHeight)
}

func textPlatePadding(padding []float64) (float64, float64) {
	switch len(padding) {
	case 0:
		return 0, 0
	case 1:
		if finite(padding[0]) && padding[0] > 0 {
			return padding[0], padding[0]
		}
	default:
		padX, padY := padding[0], padding[1]
		if !finite(padX) || padX < 0 {
			padX = 0
		}
		if !finite(padY) || padY < 0 {
			padY = 0
		}
		return padX, padY
	}
	return 0, 0
}

func clonePlateStroke(stroke *LayerStroke) *LayerStroke {
	if stroke == nil {
		return nil
	}
	value := *stroke
	return &value
}

func cloneAnimationTracks(tracks []AnimationTrack) []AnimationTrack {
	if len(tracks) == 0 {
		return nil
	}
	out := make([]AnimationTrack, len(tracks))
	for i, track := range tracks {
		out[i] = track
		out[i].Keyframes = append([]AnimationKeyframe(nil), track.Keyframes...)
	}
	return out
}

func uniquePlateID(base string, used map[string]struct{}) string {
	if _, exists := used[base]; !exists {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := base + "-" + strconv.Itoa(suffix)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}
