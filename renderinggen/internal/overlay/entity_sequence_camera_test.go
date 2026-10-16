package overlay

import "testing"

func TestEntitySequenceCameraStopsBeforeSlidingToNextViewport(t *testing.T) {
	item := semanticItem{ImageLayers: []SemanticImageLayer{
		{StartMS: 0, Params: map[string]any{"position_x": -120.0, "position_y": 0.0}},
		{StartMS: 2000, Params: map[string]any{"position_x": 120.0, "position_y": 0.0}},
	}}
	tracks := entitySequenceCameraTracks(item, 10, 200, 1920, 1080, 24, 1)
	if len(tracks) != 2 || tracks[0].Property != "camera_position_x" || tracks[1].Property != "camera_position_y" {
		t.Fatalf("camera tracks = %+v", tracks)
	}
	keys := tracks[0].Keyframes
	if len(keys) != 4 || keys[0].Frame != 10 || keys[1].Frame != 46 || keys[2].Frame != 58 || keys[3].Frame != 200 {
		t.Fatalf("camera timing = %+v; expected initial focus, 1.5 s hold, 0.5 s slide, final hold", keys)
	}
	if keys[0].Value != -120.0 || keys[1].Value != -120.0 || keys[2].Value != 120.0 || keys[3].Value != 120.0 {
		t.Fatalf("camera positions = %+v", keys)
	}
}
