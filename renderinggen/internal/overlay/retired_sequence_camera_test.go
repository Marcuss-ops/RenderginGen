package overlay

import (
	"strings"
	"testing"
)

// TestRetiredSequenceCameraFieldIsRejected pins the removal: camera_motion_id
// is no longer a supported wire field, so a plan that still carries it fails
// closed at decode instead of parking images off screen or silently ignoring
// the request. Image carousels use group_motion_id with the camera-pan family.
func TestRetiredSequenceCameraFieldIsRejected(t *testing.T) {
	raw := []byte(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":"retired-sequence","video_id":"retired-sequence",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":1000,
		"items":[{
			"id":"retired","kind":"image","template_id":"IMAGE_OVERLAY",
			"camera_motion_id":"entity_sequence_camera","start_ms":0,"end_ms":1000
		}]
	}`)
	_, err := CompileSemantic(raw)
	if err == nil {
		t.Fatal("plan with the retired camera_motion_id compiled, want a decode error")
	}
	if !strings.Contains(err.Error(), "camera_motion_id") {
		t.Fatalf("retirement error = %v, want it to name camera_motion_id", err)
	}
}

// TestRetiredSequenceCameraFieldIsRejectedInChainSegments keeps the nested
// decode as strict as the item decode: a stale segment field must fail the
// chain, not be dropped while the rest of the segment renders unchanged.
func TestRetiredSequenceCameraFieldIsRejectedInChainSegments(t *testing.T) {
	params := map[string]any{"chain_segments": []any{
		map[string]any{"text": "one", "start_us": 0, "duration_us": 900000, "camera_motion_id": "entity_sequence_camera"},
		map[string]any{"text": "two", "start_us": 1000000, "duration_us": 900000},
	}}
	_, err := decodeChainSegments(params, "retired-chain")
	if err == nil {
		t.Fatal("chain segment with the retired camera_motion_id decoded, want an error")
	}
	if !strings.Contains(err.Error(), "camera_motion_id") {
		t.Fatalf("chain retirement error = %v, want it to name camera_motion_id", err)
	}
}
