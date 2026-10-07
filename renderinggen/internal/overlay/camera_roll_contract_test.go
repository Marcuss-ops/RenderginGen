package overlay

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// TestCameraRollPublishesTheApprovedSceneContract pins the GOAL-2 closure:
// the whole-scene camera is out of scope — published as unsupported with no
// movement contract and no selectable IDs (fail-closed).
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
	if scene.Status != cameraDestinationUnsupported {
		t.Errorf("scene camera status = %q, want %q", scene.Status, cameraDestinationUnsupported)
	}
	if strings.TrimSpace(scene.RuntimeSupport) == "" {
		t.Error("scene camera does not state what is still missing")
	}
	if scene.MotionTarget != cameraSceneTarget {
		t.Errorf("scene camera target = %q, want %q", scene.MotionTarget, cameraSceneTarget)
	}
	// Fail-closed: the out-of-scope destination must not become selectable and
	// carries no movement contract (removed as unimplemented complexity).
	for _, id := range motion.Registry.List() {
		if motionAdmitsTarget(id, cameraSceneTarget) {
			t.Errorf("motion %q is admitted for the scene camera target, which is out of scope", id)
		}
	}
	if len(scene.Movements) != 0 {
		t.Fatalf("scene camera movements = %d, want 0 (out of scope, no contract)", len(scene.Movements))
	}
	for _, id := range []string{"image", "map"} {
		if byID[id].Status != cameraDestinationImplemented || len(byID[id].Movements) != 0 {
			t.Errorf("destination %q = %+v, want an implemented destination without a movement contract", id, byID[id])
		}
	}
}
