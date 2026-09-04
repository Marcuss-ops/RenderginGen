package overlay

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/motion"
)

// TestResolveMotionPropagatesRegistryError pins the fail-closed motion fix:
// a registry miss must return an error, not nil tracks that silently turn the
// overlay static while the job reports success.
func TestResolveMotionPropagatesRegistryError(t *testing.T) {
	_, err := resolveMotion(MotionDefinition{ID: "no_such_motion_plugin", Enter: 24})
	if err == nil {
		t.Fatal("unknown motion id must produce an error, got nil")
	}
	if !strings.Contains(err.Error(), "no_such_motion_plugin") {
		t.Fatalf("error must name the motion id, got: %v", err)
	}
}

// TestAnimationForDefinitionPropagatesError wires the same guarantee into the
// preset path used by compileSemantic for official preset animations.
func TestAnimationForDefinitionPropagatesError(t *testing.T) {
	anim, err := animationForDefinition(OfficialPresetDefinition{
		Motion: MotionDefinition{ID: "definitely_missing_motion", Name: "definitely_missing_motion", Enter: 24},
	})
	if err == nil {
		t.Fatalf("preset motion resolution error must propagate, got animation=%+v", anim)
	}
	if anim != nil {
		t.Fatalf("error case must not return tracks, got %+v", anim)
	}
}

// TestAnimationForDefinitionEmptyMotionIsNil keeps the legitimate no-motion
// preset case working (preset without an animation definition).
func TestAnimationForDefinitionEmptyMotionIsNil(t *testing.T) {
	anim, err := animationForDefinition(OfficialPresetDefinition{})
	if err != nil {
		t.Fatalf("empty motion must not error: %v", err)
	}
	if anim != nil {
		t.Fatalf("empty motion must produce nil animation, got %+v", anim)
	}
}

// TestResolveMotionKnownPluginStillCompiles guards against overcorrection: a
// known plugin must still lower to tracks through the error-returning path.
func TestResolveMotionKnownPluginStillCompiles(t *testing.T) {
	// motion_id "character_cascade" is exercised elsewhere via the full
	// compiler; here we use the registry itself to pick any resolvable id.
	ids := motion.Registry.List()
	if len(ids) == 0 {
		t.Skip("motion registry exposes no ids")
	}
	tracks, err := resolveMotion(MotionDefinition{ID: ids[0], Enter: 24})
	if err != nil {
		t.Fatalf("known motion %q must compile: %v", ids[0], err)
	}
	if tracks == nil {
		t.Fatalf("known motion %q produced nil tracks", ids[0])
	}
}
