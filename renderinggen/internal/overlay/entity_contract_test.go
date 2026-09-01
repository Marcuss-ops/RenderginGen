package overlay

import (
	"testing"
)

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
		30, 1,
		900,
		"background.mp4",
		overlays,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
