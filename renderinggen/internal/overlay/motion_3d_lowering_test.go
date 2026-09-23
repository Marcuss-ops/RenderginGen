package overlay

import "testing"

func TestAnimationUses3DFromConcreteLayerTracks(t *testing.T) {
	flat := &LayerAnimation{Tracks: []AnimationTrack{{Property: "position_x"}}}
	if animationUses3D(flat) {
		t.Fatal("2D track was classified as 3D")
	}
	for _, property := range []string{"position_z", "rotation_x", "rotation_y"} {
		if !animationUses3D(&LayerAnimation{Tracks: []AnimationTrack{{Property: property}}}) {
			t.Fatalf("%s track was not classified as 3D", property)
		}
	}
	for _, property := range []string{"rotation_z", "scale_z"} {
		if animationUses3D(&LayerAnimation{Tracks: []AnimationTrack{{Property: property}}}) {
			t.Fatalf("%s is not a camera-backed 3D property", property)
		}
	}
}
