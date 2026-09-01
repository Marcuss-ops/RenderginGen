package overlay

import (
	"testing"
)

func TestBuildPlanFromEntityOverlaysCenteredGeometryAndTracks(t *testing.T) {
	plan, err := BuildPlanFromEntityOverlays("center", 1920, 1080, 24, 1, 24, "", []FastEntityOverlay{{Type: "image", StartFrame: 0, EndFrame: 24, Asset: "assets/x.png", Size: 200, Animation: "fade"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Layers) != 1 {
		t.Fatalf("layers=%d", len(plan.Layers))
	}
	layer := plan.Layers[0]
	if layer.Position[0] != 860 || layer.Position[1] != 440 {
		t.Fatalf("center position=%v, want [860 440]", layer.Position)
	}
	if layer.Preset != "" {
		t.Fatalf("preset leaked into concrete entity plan: %q", layer.Preset)
	}
	if layer.Animation == nil || len(layer.Animation.Tracks) != 1 || layer.Animation.Tracks[0].Property != "opacity" {
		t.Fatalf("generic tracks=%+v", layer.Animation)
	}
}

func TestFastEntityOverlay_BuildPlan(t *testing.T) {
	overlays := []FastEntityOverlay{
		{
			Type:       "text",
			StartFrame: 0,
			EndFrame:   120,
			Position:   "lower_third",
			Text:       "Elon Musk",
			Animation:  "fade",
		},
		{
			Type:       "image",
			StartFrame: 120,
			EndFrame:   240,
			Position:   "image_right",
			Asset:      "apple.png",
			Animation:  "scale",
		},
		{
			Type:       "text",
			StartFrame: 240,
			EndFrame:   360,
			Position:   "safe_area",
			Text:       "Tesla",
			Animation:  "slide",
		},
	}

	plan, err := BuildPlanFromEntityOverlays(
		"test-job-fast-entity",
		1920, 1080,
		24, 1,
		720,
		"background.mp4",
		overlays,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.Canvas.FPSNum != 24 || plan.Canvas.FPSDen != 1 {
		t.Fatalf("expected 24/1 fps canvas, got %d/%d", plan.Canvas.FPSNum, plan.Canvas.FPSDen)
	}

	if len(plan.Layers) != 4 {
		t.Fatalf("expected 4 layers (1 video + 3 overlays), got %d", len(plan.Layers))
	}

	if plan.Layers[0].Type != "video" || plan.Layers[0].Source != "background.mp4" {
		t.Errorf("layer 0 mismatch: %+v", plan.Layers[0])
	}
	if plan.Layers[1].Type != "text" || plan.Layers[1].Text != "Elon Musk" {
		t.Errorf("layer 1 mismatch: %+v", plan.Layers[1])
	}
	if plan.Layers[2].Type != "image" || plan.Layers[2].Asset != "apple.png" {
		t.Errorf("layer 2 mismatch: %+v", plan.Layers[2])
	}
	if plan.Layers[3].Type != "text" || plan.Layers[3].Text != "Tesla" {
		t.Errorf("layer 3 mismatch: %+v", plan.Layers[3])
	}
}
