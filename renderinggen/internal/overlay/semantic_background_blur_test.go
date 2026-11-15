package overlay

import (
	"fmt"
	"strings"
	"testing"
)

func backgroundBlurPlan(blur float64) []byte {
	return []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"background-blur",
		"video_id":"background-blur",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":1000,
		"source":{"asset_id":"source","sha256":"%s"},
		"background":{
			"kind":"video",
			"asset_refs":[{"asset_id":"bg","sha256":"%s","url":"assets/semantic/bg.mp4","media_type":"video/mp4"}],
			"fit":"cover","loop":true,"blur_px":%g
		}
	}`, strings.Repeat("a", 64), strings.Repeat("b", 64), blur))
}

// TestBackgroundBlurLowersToGaussianEffect pins the wire contract that makes
// blur_source a real soft backdrop. Before this effect existed the mapper
// emitted a sharp full-frame copy of the source, so the card edge showed a
// differently-scaled duplicate of the foreground.
func TestBackgroundBlurLowersToGaussianEffect(t *testing.T) {
	compiled, err := CompileSemantic(backgroundBlurPlan(24))
	if err != nil {
		t.Fatalf("CompileSemantic: %v", err)
	}
	var background *Layer
	for i := range compiled.Plan.Layers {
		if compiled.Plan.Layers[i].ID == "background" {
			background = &compiled.Plan.Layers[i]
			break
		}
	}
	if background == nil {
		t.Fatalf("compiled layers = %v, want a background layer", layerIDs(compiled.Plan.Layers))
	}
	if len(background.Effects) != 1 || background.Effects[0].Type != "gaussian_blur" {
		t.Fatalf("background effects = %+v, want one gaussian_blur", background.Effects)
	}
	radius, ok := background.Effects[0].Params["radius"].(float64)
	if !ok || radius != 24 {
		t.Fatalf("background blur radius = %v, want 24", background.Effects[0].Params["radius"])
	}
}

func TestBackgroundBlurRejectsOutOfRange(t *testing.T) {
	if _, err := CompileSemantic(backgroundBlurPlan(maxBackgroundBlur + 1)); err == nil {
		t.Fatal("expected an out-of-range background blur to be rejected")
	}
}
