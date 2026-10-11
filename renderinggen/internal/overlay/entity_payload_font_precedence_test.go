package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEntityCardKeepsPayloadTypefaceOverPresetDefault is the end-of-chain
// proof for the payload font: a plan carrying caption_font_family (and the
// same family in params) plus an entity_style_id must lower its caption to
// the payload's bundled face — never to the preset's Inter default — with
// the payload's glow and stroke intact.
func TestEntityCardKeepsPayloadTypefaceOverPresetDefault(t *testing.T) {
	item := map[string]any{
		"id":                  "portrait",
		"entity_id":           "person:ada-lovelace",
		"kind":                "entity_image",
		"template_id":         "image_popup",
		"preset_id":           "image_scale_in",
		"image_preset_id":     "image_scale_in",
		"entity_caption":      "Ada Lovelace",
		"entity_style_id":     "01_entity_pitch_lift_text_below",
		"caption_font_family": "bricolage_grotesque",
		// NOTE: no params.font_family on purpose. The caption compiler
		// prefers parent params over caption_font_family, so carrying the
		// family only in caption_font_family is the case that exercises
		// the entity-style precedence rule under test.
		"params": map[string]any{
			"glow_size":   10.0,
			"stroke_size": 2.0,
		},
		"start_ms": 0, "end_ms": 5000, "duration_ms": 5000,
		"asset_refs": []any{map[string]any{
			"asset_id":   "portrait-asset",
			"sha256":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"url":        "https://store.example/portrait.png",
			"media_type": "image/png",
		}},
	}
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "entity-payload-font",
		"video_id":       "entity-payload-font",
		"width":          1920, "height": 1080,
		"fps_num": 30, "fps_den": 1,
		"items": []any{item},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic entity payload-font plan: %v", err)
	}
	var caption *Layer
	for i := range result.Plan.Layers {
		layer := &result.Plan.Layers[i]
		if layer.Type == "text" && strings.TrimSpace(layer.Text) == "Ada Lovelace" {
			caption = layer
		}
	}
	if caption == nil {
		t.Fatalf("no caption layer for Ada Lovelace: %+v", result.Plan.Layers)
	}
	if caption.Style == nil || caption.Style.Font != "assets/fonts/Bricolage-Grotesque.ttf" {
		t.Errorf("caption font = %+v, want the payload Bricolage face, not the preset Inter default", caption.Style)
	}
	if caption.Style == nil || caption.Style.Stroke == nil || caption.Style.Stroke.Width != 2 {
		t.Errorf("caption stroke = %+v, want the payload 2px keyline", caption.Style)
	}
	if caption.Style == nil || caption.Style.Glow == nil || caption.Style.Glow.Radius != 10 {
		t.Errorf("caption glow = %+v, want the payload 10px halo", caption.Style)
	}
}
