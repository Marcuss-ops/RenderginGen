package overlay

import "testing"

// A captioned entity may select a camera-backed caption motion. Its plate used
// to be skipped because the lift only accepted ScreenSpace text, which left the
// two 3D captions in a chain without the payload card_background. The plate must
// inherit the caption's space, camera requirement and 3D tracks.
func TestTextBackgroundPlatePreserves3DCaptionSpace(t *testing.T) {
	opacity := 1.0
	layer := Layer{
		ID: "cap", Type: "text", StartFrame: 41, DurationFrames: 29,
		ScreenSpace: false, Enable3D: true,
		Size: []float64{300, 88}, Position: []float64{960, 1012},
		Style: &LayerStyle{Background: &LayerBackground{
			Color: "#FF6B35", Opacity: &opacity, Radius: 12,
			Padding: []float64{18, 10}, LiftToShape: true,
		}},
		Animation: &LayerAnimation{Tracks: []AnimationTrack{{
			Property: "position_z",
			Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: 110.0},
				{Frame: 7, Value: 0.0},
			},
		}}},
	}
	plan := &Plan{Layers: []Layer{layer}}

	liftTextBackgroundPlates(plan)

	if len(plan.Layers) != 2 {
		t.Fatalf("layers = %d, want plate + text", len(plan.Layers))
	}
	plate := plan.Layers[0]
	if plate.Type != "shape" || plate.Shape == nil {
		t.Fatalf("plate = %+v, want shape", plate)
	}
	if plate.Enable3D != layer.Enable3D {
		t.Fatalf("plate enable_3d = %v, want %v", plate.Enable3D, layer.Enable3D)
	}
	if plate.ScreenSpace != layer.ScreenSpace {
		t.Fatalf("plate screen_space = %v, want %v", plate.ScreenSpace, layer.ScreenSpace)
	}
	if plate.Animation == nil || len(plate.Animation.Tracks) != 1 || plate.Animation.Tracks[0].Property != "position_z" {
		t.Fatalf("plate animation = %+v, want the caption's 3D track", plate.Animation)
	}
}
