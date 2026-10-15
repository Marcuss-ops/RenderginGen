package overlay

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// TestCameraRollPublishesTheApprovedSceneContract pins the scene-camera
// contract: the whole-scene camera is implemented via the plan-level
// scene_camera block (AE one_node/two_node rig, null-chain lowering), with
// three supported movements. Unknown rig/move/layer references still fail
// closed in the compiler, so no consumer can invent a choice.
func TestCameraRollPublishesTheApprovedSceneContract(t *testing.T) {
	byID := make(map[string]RuntimeCameraDestination)
	for _, destination := range runtimeCameraDestinations() {
		byID[destination.ID] = destination
	}
	for _, id := range []string{"scene", "image", "map"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("camera destination %q missing", id)
		}
	}
	scene := byID["scene"]
	if scene.RequiresProductDecision {
		t.Error("scene camera still requests a product decision after the owner approved it")
	}
	if scene.Status != cameraDestinationImplemented {
		t.Errorf("scene camera status = %q, want %q", scene.Status, cameraDestinationImplemented)
	}
	if strings.TrimSpace(scene.RuntimeSupport) == "" {
		t.Error("scene camera does not state what it implements")
	}
	if scene.MotionTarget != cameraSceneTarget {
		t.Errorf("scene camera target = %q, want %q", scene.MotionTarget, cameraSceneTarget)
	}
	// The motion registry still owns no scene-target motion: scene moves are
	// rig segments inside scene_camera, not selectable motion catalogue IDs.
	for _, id := range motion.Registry.List() {
		if motionAdmitsTarget(id, cameraSceneTarget) {
			t.Errorf("motion %q is admitted for the scene camera target, which has no catalogue motion", id)
		}
	}
	wantMovements := map[string]bool{
		"scene_dolly_through":   false,
		"scene_lateral_handoff": false,
		"scene_crane_reveal":    false,
	}
	if len(scene.Movements) != len(wantMovements) {
		t.Fatalf("scene camera movements = %d, want %d", len(scene.Movements), len(wantMovements))
	}
	for _, m := range scene.Movements {
		seen, ok := wantMovements[m.ID]
		if !ok {
			t.Errorf("scene camera advertises unexpected movement %q", m.ID)
			continue
		}
		if seen {
			t.Errorf("scene camera advertises duplicate movement %q", m.ID)
		}
		wantMovements[m.ID] = true
		if !m.Supported {
			t.Errorf("scene camera movement %q is not supported", m.ID)
		}
		if strings.TrimSpace(m.Description) == "" || strings.TrimSpace(m.FinalPose) == "" {
			t.Errorf("scene camera movement %q has an empty description or final pose", m.ID)
		}
	}
	for id, seen := range wantMovements {
		if !seen {
			t.Errorf("scene camera movement %q missing from the contract", id)
		}
	}
	for _, id := range []string{"image", "map"} {
		if byID[id].Status != cameraDestinationImplemented || len(byID[id].Movements) != 0 {
			t.Errorf("destination %q = %+v, want an implemented destination without a movement contract", id, byID[id])
		}
	}
}
