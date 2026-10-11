package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEntityCardKeepsPayloadTypefaceOverPresetDefault proves the canonical
// item runtime font wins over both caption_font_family and entity defaults.
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
		"params": map[string]any{
			"font_family": "inter",
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
	if caption.Style == nil || caption.Style.Font != "assets/fonts/Inter.ttf" {
		t.Errorf("caption font = %+v, want the canonical item Inter face over child and preset defaults", caption.Style)
	}
	if caption.Style == nil || caption.Style.Stroke == nil || caption.Style.Stroke.Width != 2 {
		t.Errorf("caption stroke = %+v, want the payload 2px keyline", caption.Style)
	}
	if caption.Style == nil || caption.Style.Glow == nil || caption.Style.Glow.Radius != 10 {
		t.Errorf("caption glow = %+v, want the payload 10px halo", caption.Style)
	}
}

func TestEntityCaptionWithoutPayloadFontUsesLanguageDefault(t *testing.T) {
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1", "plan_id": "entity-language-font", "video_id": "v",
		"language": "ru", "width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"items": []any{map[string]any{
			"id": "portrait", "entity_id": "person:ada", "kind": "entity_image",
			"template_id": "image_popup", "preset_id": "image_scale_in", "image_preset_id": "image_scale_in",
			"entity_caption": "Ada Lovelace", "entity_style_id": "01_entity_pitch_lift_text_below",
			"start_ms": 0, "end_ms": 5000,
			"asset_refs": []any{map[string]any{
				"asset_id": "portrait", "sha256": strings.Repeat("a", 64),
				"url": "https://store.example/portrait.png", "media_type": "image/png",
			}},
		}},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic language-default plan: %v", err)
	}
	for _, layer := range result.Plan.Layers {
		if layer.Type == "text" && strings.TrimSpace(layer.Text) == "Ada Lovelace" {
			if layer.Style == nil || layer.Style.Font != "assets/fonts/DejaVuSans.ttf" {
				t.Fatalf("caption font = %+v, want the single language fallback DejaVu Sans", layer.Style)
			}
			return
		}
	}
	t.Fatalf("no caption layer for Ada Lovelace: %+v", result.Plan.Layers)
}

func TestCompositeCaptionsUseCanonicalParentTypographyOverChildAnimationStyles(t *testing.T) {
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1", "plan_id": "composite-caption-style", "video_id": "video",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"items": []any{map[string]any{
			"id": "pair", "kind": "image", "template_id": "IMAGE_OVERLAY", "preset_id": "image_scale_in",
			"params":   map[string]any{"font_family": "inter", "glow_size": 4.0, "style": map[string]any{"fill": "#111111"}},
			"start_ms": 0, "end_ms": 5000,
			"asset_refs": []any{
				map[string]any{"asset_id": "a", "sha256": strings.Repeat("a", 64), "url": "https://store.example/a.png", "media_type": "image/png"},
				map[string]any{"asset_id": "b", "sha256": strings.Repeat("b", 64), "url": "https://store.example/b.png", "media_type": "image/png"},
			},
			"image_layers": []any{
				map[string]any{"id": "a", "asset_id": "a", "start_ms": 0, "end_ms": 5000, "preset_id": "image_scale_in", "caption": "First", "caption_font_family": "bricolage_grotesque", "caption_color": "#AABBCC", "params": map[string]any{"position_x": -200.0, "position_y": 0.0, "width": 500.0, "height": 500.0}},
				map[string]any{"id": "b", "asset_id": "b", "start_ms": 0, "end_ms": 5000, "preset_id": "image_scale_in", "caption": "Second", "caption_font_family": "poppins", "caption_color": "#DDEEFF", "params": map[string]any{"position_x": 200.0, "position_y": 0.0, "width": 500.0, "height": 500.0}},
			},
		}},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic composite caption plan: %v", err)
	}
	want := map[string]string{"First": "assets/fonts/Inter.ttf", "Second": "assets/fonts/Inter.ttf"}
	seen := map[string]bool{}
	for _, layer := range result.Plan.Layers {
		font, ok := want[layer.Text]
		if !ok {
			continue
		}
		if layer.Type != "text" || layer.Style == nil || layer.Style.Font != font {
			t.Errorf("caption %q style = %+v type=%q, want child font %q", layer.Text, layer.Style, layer.Type, font)
		}
		if layer.Style != nil && layer.Style.Fill != "#111111" {
			t.Errorf("caption %q fill = %q, want canonical parent fill #111111", layer.Text, layer.Style.Fill)
		}
		if layer.Style != nil && (layer.Style.Glow == nil || layer.Style.Glow.Radius != 4) {
			t.Errorf("caption %q glow = %+v, want canonical parent glow 4", layer.Text, layer.Style.Glow)
		}
		seen[layer.Text] = true
	}
	for text := range want {
		if !seen[text] {
			t.Errorf("missing compiled caption %q", text)
		}
	}
}
