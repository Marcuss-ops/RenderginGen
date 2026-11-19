package overlay

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

// jsonInt renders i as a stable decimal string for unique plan ids.
func jsonInt(i int) string { return fmt.Sprintf("%d", i) }

// styledEntityImagePlan builds a renderinggen.overlay-plan.v1 document with
// one PipelineGen-shaped entity card: an image-behavior kind (entity_image)
// carrying entity_style_id, one portrait asset and an entity_caption.
func styledEntityImagePlan(t *testing.T, planID, styleID string) []byte {
	t.Helper()
	item := map[string]any{
		"id":              "portrait",
		"entity_id":       "person:ada-lovelace",
		"kind":            "entity_image",
		"template_id":     "image_popup",
		"preset_id":       "image_scale_in",
		"image_preset_id": "image_scale_in",
		"entity_caption":  "Ada Lovelace",
		"entity_style_id": styleID,
		"start_ms":        0, "end_ms": 5000, "duration_ms": 5000,
		"asset_refs": []any{map[string]any{
			"asset_id":   "portrait-asset",
			"sha256":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"url":        "https://store.example/portrait.png",
			"media_type": "image/png",
		}},
	}
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        planID,
		"video_id":       planID,
		"width":          1920, "height": 1080,
		"fps_num": 30, "fps_den": 1,
		"items": []any{item},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEntityImageStyleRandomCompilesAndKeepsCaption(t *testing.T) {
	result, err := CompileSemantic(styledEntityImagePlan(t, "entity-image-random", "random"))
	if err != nil {
		t.Fatalf("CompileSemantic with entity_style_id=random: %v", err)
	}
	var hasImage, hasCaption bool
	for _, layer := range result.Plan.Layers {
		if layer.Type == "image" {
			hasImage = true
		}
		if layer.Type == "text" && strings.TrimSpace(layer.Text) == "Ada Lovelace" {
			hasCaption = true
		}
	}
	if !hasImage || !hasCaption {
		t.Fatalf("styled entity image plan lost its image or caption: %+v", result.Plan.Layers)
	}
}

func TestEntityCaptionPayloadBadgeColorCompilesPlateAndContrast(t *testing.T) {
	for _, tc := range []struct {
		color string
		text  string
	}{
		{color: "#F0D020", text: "#0A0B0E"},
		{color: "#17304A", text: "#FFFFFF"},
	} {
		raw := styledEntityImagePlan(t, "entity-badge-"+tc.color[1:], "premium_below_random_v1")
		var plan map[string]any
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		items := plan["items"].([]any)
		item := items[0].(map[string]any)
		item["style"] = map[string]any{"style": map[string]any{
			"background": map[string]any{"color": tc.color, "radius": 0, "padding": []any{12, 24}},
		}}
		raw, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		result, err := CompileSemantic(raw)
		if err != nil {
			t.Fatalf("CompileSemantic badge color %s: %v", tc.color, err)
		}
		found := false
		for _, layer := range result.Plan.Layers {
			if layer.Type != "text" || strings.TrimSpace(layer.Text) != "Ada Lovelace" {
				continue
			}
			found = true
			if layer.Style == nil || layer.Style.Background != nil {
				t.Fatalf("caption still carries a dead text box: %+v", layer.Style)
			}
			if layer.Style.Fill != tc.text {
				t.Fatalf("caption fill = %q, want contrast color %q", layer.Style.Fill, tc.text)
			}
			var plate *Layer
			for i := range result.Plan.Layers {
				if result.Plan.Layers[i].ID == layer.ID+":plate" {
					plate = &result.Plan.Layers[i]
				}
			}
			if plate == nil || plate.Type != "shape" || plate.Shape == nil {
				t.Fatalf("badge color %s produced no shape plate", tc.color)
			}
			want, err := parseHexColor(tc.color)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := plate.Shape.Fill.([]float64)
			if !ok || len(got) != 4 || math.Abs(got[0]-want[0]) > 1e-9 || math.Abs(got[1]-want[1]) > 1e-9 || math.Abs(got[2]-want[2]) > 1e-9 {
				t.Fatalf("caption plate fill = %#v, want %s", plate.Shape.Fill, tc.color)
			}
		}
		if !found {
			t.Fatalf("badge color %s produced no caption layer", tc.color)
		}
	}
}

func TestEntityImageStylePreservesExplicitProducerPosition(t *testing.T) {
	raw := styledEntityImagePlan(t, "entity-image-explicit-position", "01_entity_pitch_lift_text_below")
	plan := strings.Replace(string(raw), `"entity_style_id":"01_entity_pitch_lift_text_below"`, `"entity_style_id":"01_entity_pitch_lift_text_below","params":{"position_x":-720,"position_y":0}`, 1)
	result, err := CompileSemantic([]byte(plan))
	if err != nil {
		t.Fatalf("CompileSemantic with explicit entity position: %v", err)
	}
	for _, layer := range result.Plan.Layers {
		if layer.Type == "image" {
			if len(layer.Position) < 2 || layer.Position[0] != -720 || layer.Position[1] != 0 {
				t.Fatalf("explicit producer position was overridden by entity style: %v", layer.Position)
			}
			return
		}
	}
	t.Fatal("styled entity plan emitted no portrait image")
}

func TestEntityImageStyleRandomReachesAll25AppleSpatialStyles(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 256; i++ {
		planID := "entity-style-sweep-" + string(rune('a'+i%26)) + "-" + jsonInt(i)
		style, ok := ResolveEntityStyle("random", planID, planID, "portrait")
		if !ok {
			t.Fatalf("ResolveEntityStyle(random) failed for %q", planID)
		}
		seen[style.ID] = struct{}{}
	}
	for _, style := range premiumEntityStyles {
		if _, ok := seen[style.ID]; !ok {
			t.Fatalf("entity_style_id=random never sampled %q after 256 identities", style.ID)
		}
	}
}

func TestEntityImageStyleBadgeKeepsCaptionWithoutGPUShape(t *testing.T) {
	for i := 0; i < 8; i++ {
		planID := "entity-style-badge-" + jsonInt(i)
		raw := []byte(strings.Replace(string(styledEntityImagePlan(t, planID, "badge")),
			"\"entity_style_id\":\"badge\"", "\"entity_style_id\":\"badge\"", 1))
		result, err := CompileSemantic(raw)
		if err != nil {
			t.Fatalf("CompileSemantic badge plan %q: %v", planID, err)
		}
		var captionFound bool
		for _, layer := range result.Plan.Layers {
			if layer.Type == "text" {
				captionFound = true
			}
			if layer.ID == "portrait:entity_badge" {
				t.Fatalf("badge style emitted a GPU shape")
			}
		}
		if !captionFound {
			t.Fatalf("badge plan %q lost its entity caption", planID)
		}
	}
}

func TestEntityImageStyleCameraBuildsSceneCameraPlan(t *testing.T) {
	result, err := CompileSemantic(styledEntityImagePlan(t, "entity-style-camera", "camera"))
	if err != nil {
		t.Fatalf("CompileSemantic camera plan: %v", err)
	}
	if result.Plan.Camera == nil || result.Plan.CameraAnimation == nil || len(result.Plan.CameraAnimation.Tracks) == 0 {
		t.Fatalf("entity_style_id=camera produced no scene camera animation: camera=%+v", result.Plan.Camera)
	}
}

func TestEntityImageStyleRejectedWithoutCaption(t *testing.T) {
	raw := strings.Replace(string(styledEntityImagePlan(t, "entity-style-bad", "random")),
		`"entity_caption":"Ada Lovelace"`, "", 1)
	if _, err := CompileSemantic([]byte(raw)); err == nil {
		t.Fatal("entity_style_id on an item without entity_caption must fail closed")
	}
}
