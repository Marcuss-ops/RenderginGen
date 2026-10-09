package overlay

import (
	"encoding/json"
	"fmt"
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
