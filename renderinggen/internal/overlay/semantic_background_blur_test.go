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

func backgroundTreatmentPlan(blur, zoom float64) []byte {
	return []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"background-zoom",
		"video_id":"background-zoom",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":1000,
		"source":{"asset_id":"source","sha256":"%s"},
		"background":{
			"kind":"video",
			"asset_refs":[{"asset_id":"bg","sha256":"%s","url":"assets/semantic/bg.mp4","media_type":"video/mp4"}],
			"fit":"cover","loop":true,"blur_px":%g,"zoom":%g
		}
	}`, strings.Repeat("a", 64), strings.Repeat("b", 64), blur, zoom))
}

func compiledBackground(t *testing.T, raw []byte) Layer {
	t.Helper()
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic: %v", err)
	}
	for _, layer := range compiled.Plan.Layers {
		if layer.ID == "background" {
			return layer
		}
	}
	t.Fatalf("compiled layers = %v, want a background layer", layerIDs(compiled.Plan.Layers))
	return Layer{}
}

// TestBackgroundZoomLowersToScale pins the companion of the blur control: a
// zoomed plate must actually scale, otherwise the requested crop is silently
// ignored and the background is a different framing than the producer asked
// for.
func TestBackgroundZoomLowersToScale(t *testing.T) {
	background := compiledBackground(t, backgroundTreatmentPlan(24, 1.15))
	if len(background.Scale) != 2 || background.Scale[0] != 1.15 || background.Scale[1] != 1.15 {
		t.Fatalf("background scale = %v, want [1.15,1.15]", background.Scale)
	}
	// The blur effect must survive alongside the scale.
	if len(background.Effects) != 1 || background.Effects[0].Type != "gaussian_blur" {
		t.Fatalf("background effects = %+v, want one gaussian_blur", background.Effects)
	}
}

// TestBackgroundZoomAbsentKeepsHistoricalLayer pins backwards compatibility:
// a plate without the zoom key compiles to exactly the layer every existing
// plan already produced (no scale key on the wire).
func TestBackgroundZoomAbsentKeepsHistoricalLayer(t *testing.T) {
	background := compiledBackground(t, backgroundBlurPlan(24))
	if background.Scale != nil {
		t.Fatalf("background scale = %v, want absent when zoom is undeclared", background.Scale)
	}
}

func TestBackgroundZoomRejectsOutOfRange(t *testing.T) {
	for _, zoom := range []float64{0.5, maxBackgroundZoom + 0.1} {
		if _, err := CompileSemantic(backgroundTreatmentPlan(24, zoom)); err == nil {
			t.Fatalf("expected background zoom %v to be rejected", zoom)
		}
	}
}

func TestBackgroundZoomRejectedForColorPlate(t *testing.T) {
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"background-zoom-color",
		"video_id":"background-zoom-color",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":1000,
		"source":{"asset_id":"source","sha256":"%s"},
		"background":{"kind":"color","color":[0,0,0,1],"zoom":1.2}
	}`, strings.Repeat("a", 64)))
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("expected background zoom on a color plate to be rejected")
	}
}
