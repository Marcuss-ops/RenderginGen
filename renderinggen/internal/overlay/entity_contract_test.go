package overlay

import (
	"testing"
)

func TestCompileFastEntityOverlaysCenteredGeometryAndTracks(t *testing.T) {
	plan, err := CompileFastEntityOverlays("center", 1920, 1080, 24, 1, 24, "", []FastEntityOverlay{{Type: "image", StartFrame: 0, EndFrame: 24, Asset: "assets/x.png", Size: 200, Animation: "fade"}})
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
	if layer.BoxWidth != 200 || layer.BoxHeight != 200 {
		t.Fatalf("center size=%dx%d, want 200x200", layer.BoxWidth, layer.BoxHeight)
	}
	if layer.Animation == nil || len(layer.Animation.Tracks) != 1 || layer.Animation.Tracks[0].Property != "opacity" {
		t.Fatalf("generic tracks=%+v", layer.Animation)
	}
}

func TestCompileOfficialPresetPreservesTextAnimatorAndLayout(t *testing.T) {
	def, err := ResolveOfficialPreset("active_word_pop")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := CompileFastEntityOverlays("word", 1920, 1080, 24, 1, 125, "color:#EEF1E7", []FastEntityOverlay{{
		Type: "text", PresetID: def.ID, Text: "Una due tre", Font: "fonts/Poppins-Bold.ttf",
		StartFrame: 0, EndFrame: 125,
	}})
	if err != nil {
		t.Fatal(err)
	}
	layer := plan.Layers[1]
	if len(layer.TextAnimators) != 1 {
		t.Fatalf("word preset lost text animator: animation=%+v animators=%d", layer.Animation, len(layer.TextAnimators))
	}
	if got := layer.TextAnimators[0].Selectors[0].Unit; got != "word" {
		t.Fatalf("selector unit=%q, want word", got)
	}
	if layer.Position[1] <= 0 || layer.Style == nil || layer.Style.Font == "" {
		t.Fatalf("preset layout/style not applied: position=%v style=%+v", layer.Position, layer.Style)
	}
}

func TestCompileImagePresetUsesCatalogGeometryAndDirection(t *testing.T) {
	plan, err := CompileFastEntityOverlays("image", 1920, 1080, 24, 1, 125, "color:#EEF1E7", []FastEntityOverlay{{
		Type: "image", PresetID: "image_slide_right", Asset: "gerard_butler.jpg", StartFrame: 0, EndFrame: 125,
	}})
	if err != nil {
		t.Fatal(err)
	}
	layer := plan.Layers[1]
	if layer.Asset != "gerard_butler.jpg" || layer.Position[0] != 830 || layer.BoxWidth != 260 {
		t.Fatalf("image preset geometry=%+v position=%v", layer, layer.Position)
	}
	if layer.Animation != nil && len(layer.Animation.Tracks) != 0 {
		t.Fatalf("image preset must avoid Chronon image primitive tracks: %+v", layer.Animation)
	}
}

func TestCompileImagePresetHonorsExplicitCenterAndSize(t *testing.T) {
	plan, err := CompileFastEntityOverlays("image-center", 1920, 1080, 24, 1, 125, "color:#EEF1E7", []FastEntityOverlay{{
		Type: "image", PresetID: "image_scale_in", Asset: "gerard_butler.jpg", StartFrame: 0, EndFrame: 125,
		Position: "center", Size: 600,
	}})
	if err != nil {
		t.Fatal(err)
	}
	layer := plan.Layers[1]
	if layer.Position[0] != 0 || layer.Position[1] != 0 || layer.BoxWidth != 600 || layer.BoxHeight != 600 {
		t.Fatalf("explicit center geometry=%+v position=%v", layer, layer.Position)
	}
}

func TestFastEntityOverlay_BuildPlan(t *testing.T) {
	overlays := []FastEntityOverlay{
		{
			Type:       "text",
			Font:       "assets/fonts/Poppins-Bold.ttf",
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
			Font:       "assets/fonts/Poppins-Bold.ttf",
			Text:       "Tesla",
			Animation:  "slide",
		},
	}

	plan, err := CompileFastEntityOverlays(
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
