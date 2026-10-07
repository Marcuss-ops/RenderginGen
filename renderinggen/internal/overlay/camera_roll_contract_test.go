package overlay

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// TestCameraRollPublishesTheApprovedSceneContract pins the owner decision of
// 7 October 2026: the camera roll covers the whole scene or source clip, so the
// scene destination stops asking for a product decision and instead publishes
// the movement contract plus the exact reason it is not selectable yet.
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
	if scene.Status != cameraDestinationNeedsLowering {
		t.Errorf("scene camera status = %q, want %q", scene.Status, cameraDestinationNeedsLowering)
	}
	if strings.TrimSpace(scene.RuntimeSupport) == "" {
		t.Error("scene camera does not state what is still missing")
	}
	if scene.MotionTarget != cameraSceneTarget {
		t.Errorf("scene camera target = %q, want %q", scene.MotionTarget, cameraSceneTarget)
	}
	// Fail-closed: the approved destination must not become selectable before a
	// canonical family exists, so no registered motion may be admitted for it.
	for _, id := range motion.Registry.List() {
		if motionAdmitsTarget(id, cameraSceneTarget) {
			t.Errorf("motion %q is admitted for the scene camera target before its family is authored", id)
		}
	}
	wantMovements := []string{"pan", "push_pull", "zoom", "tilt_roll", "parallax"}
	if len(scene.Movements) != len(wantMovements) {
		t.Fatalf("scene camera movements = %d, want %d", len(scene.Movements), len(wantMovements))
	}
	for index, want := range wantMovements {
		movement := scene.Movements[index]
		if movement.ID != want {
			t.Errorf("movement %d = %q, want %q", index, movement.ID, want)
		}
		if movement.Supported {
			t.Errorf("movement %q claims renderer support before the family exists", movement.ID)
		}
		if strings.TrimSpace(movement.UnavailableReason) == "" {
			t.Errorf("movement %q does not explain why it is unavailable", movement.ID)
		}
		for name, value := range map[string]string{
			"direction_parameter": movement.Direction,
			"duration_bounds":     movement.DurationBounds,
			"crop_bounds":         movement.CropBounds,
			"initial_pose":        movement.InitialPose,
			"final_pose":          movement.FinalPose,
		} {
			if strings.TrimSpace(value) == "" {
				t.Errorf("movement %q declares no %s", movement.ID, name)
			}
		}
	}
	for _, id := range []string{"image", "map"} {
		if byID[id].Status != cameraDestinationImplemented || len(byID[id].Movements) != 0 {
			t.Errorf("destination %q = %+v, want an implemented destination without a movement contract", id, byID[id])
		}
	}
}
