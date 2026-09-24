package overlay

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// TestAnimationUses3DFromConcreteLayerTracks pins the layer half of the routing
// bit against the closed property set: Z translation and X/Y rotation opt in,
// while scale_z and in-plane rotation_z must NOT, because opting in switches the
// render to the camera-backed path that clears the canvas to black when no
// camera is present.
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
	if animationUses3D(nil) {
		t.Fatal("a motion with no lowered animation was classified as 3D")
	}
}

// TestLayerUses3DScansTextAnimators is the regression guard for the half the
// routing bit used to ignore. Chronon's decoder walks animation.tracks and then
// text_animators[].properties, so a motion whose 3D-ness lives only in a
// per-unit track was routed to the flat path and then refused by the engine with
// "3D transform data requires enable_3d=true; offending track: position_z".
func TestLayerUses3DScansTextAnimators(t *testing.T) {
	animator := func(properties ...string) TextAnimator {
		tracks := make([]AnimationTrack, len(properties))
		for i, property := range properties {
			tracks[i] = AnimationTrack{Property: property}
		}
		return TextAnimator{ID: "unit", Properties: tracks}
	}

	for _, property := range []string{"position_z", "rotation_x", "rotation_y"} {
		animation := &LayerAnimation{
			Tracks:        []AnimationTrack{{Property: "opacity"}},
			TextAnimators: []TextAnimator{animator("opacity", property)},
		}
		if animationUses3D(animation) {
			t.Errorf("animationUses3D saw %s, which only the per-unit half carries", property)
		}
		if !layerUses3D(animation) {
			t.Errorf("a motion whose only %s track is per-unit must still route through the 3D path", property)
		}
	}
	animation := &LayerAnimation{
		Tracks: []AnimationTrack{{Property: "opacity"}},
		TextAnimators: []TextAnimator{
			animator("opacity", "scale_z", "rotation_z"),
			animator("position_y"),
		},
	}
	if layerUses3D(animation) {
		t.Fatal("per-unit in-plane properties must not opt the layer into the 3D path")
	}
	// The layer half is still sufficient on its own.
	if !layerUses3D(&LayerAnimation{Tracks: []AnimationTrack{{Property: "position_z"}}}) {
		t.Fatal("a layer track that needs 3D was missed when the animator half is empty")
	}
	if layerUses3D(nil) {
		t.Fatal("a motion with no lowered animation was classified as 3D")
	}
}

// TestApplyMotionRoutingTransportsBothHalves keeps the single lowering honest:
// the routing bit and the payload it describes are written together, so a caller
// cannot ship an animation without the flag that matches it.
func TestApplyMotionRoutingTransportsBothHalves(t *testing.T) {
	animation := &LayerAnimation{
		Tracks:        []AnimationTrack{{Property: "opacity"}},
		TextAnimators: []TextAnimator{{ID: "unit", Properties: []AnimationTrack{{Property: "position_z"}}}},
	}
	var layer Layer
	applyMotionRouting(&layer, animation)
	if !layer.Enable3D {
		t.Fatal("the routing bit ignored the per-unit 3D track it just transported")
	}
	if layer.Animation != animation || len(layer.TextAnimators) != 1 {
		t.Fatalf("routing did not transport both halves: animation=%v animators=%v", layer.Animation, layer.TextAnimators)
	}
	wire, err := json.Marshal(layer)
	if err != nil {
		t.Fatal(err)
	}
	var emitted struct {
		Enable3D      bool           `json:"enable_3d"`
		TextAnimators []TextAnimator `json:"text_animators"`
	}
	if err := json.Unmarshal(wire, &emitted); err != nil {
		t.Fatal(err)
	}
	if !emitted.Enable3D || len(emitted.TextAnimators) != 1 {
		t.Fatalf("serialized layer = %s, want enable_3d with the per-unit 3D track", wire)
	}

	var untouched Layer
	applyMotionRouting(&untouched, nil)
	if untouched.Enable3D || untouched.Animation != nil || untouched.TextAnimators != nil {
		t.Fatalf("a motion that lowered to nothing changed the layer: %+v", untouched)
	}
}

// TestCameraBacked3DPropertyIsTheSingleAuthority keeps the producer's property
// list in one place. overlay's routing delegates to motion, so the registry's
// 3d family and the compiler's enable_3d can only disagree with Chronon if this
// list changes without the engine's decoder.
func TestCameraBacked3DPropertyIsTheSingleAuthority(t *testing.T) {
	for _, property := range []string{"position_z", "rotation_x", "rotation_y"} {
		if !motion.IsCameraBacked3DProperty(property) {
			t.Errorf("%s must opt a layer into the camera-backed path", property)
		}
	}
	for _, property := range []string{"position_x", "position_y", "scale", "scale_x", "scale_y", "scale_z", "rotation_z", "opacity", "blur", ""} {
		if motion.IsCameraBacked3DProperty(property) {
			t.Errorf("%q must stay on the flat path", property)
		}
	}
}
