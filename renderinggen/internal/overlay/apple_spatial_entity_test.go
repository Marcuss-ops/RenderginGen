package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestAll25AppleSpatialEntityStylesCompile(t *testing.T) {
	for i := 1; i <= 25; i++ {
		styleRef := fmt.Sprintf("%02d", i)
		t.Run("style_"+styleRef, func(t *testing.T) {
			item := map[string]any{
				"id":              "portrait-" + styleRef,
				"entity_id":       "person:trump",
				"kind":            "entity_card",
				"template_id":     "PERSON",
				"preset_id":       "phrase_default",
				"text":            "Donald J. Trump",
				"image_preset_id": "image_scale_in",
				"entity_caption":  "Donald J. Trump",
				"entity_style_id": styleRef,
				"start_ms":        0, "end_ms": 3000, "duration_ms": 3000,
				"asset_refs": []any{map[string]any{
					"asset_id":   "trump-portrait",
					"sha256":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					"url":        "https://store.example/trump.png",
					"media_type": "image/png",
				}},
			}
			plan := map[string]any{
				"schema_version": "renderinggen.overlay-plan.v1",
				"plan_id":        "plan-" + styleRef,
				"video_id":       "video-" + styleRef,
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
				t.Fatalf("CompileSemantic failed for style %s: %v", styleRef, err)
			}
			if len(result.Plan.Layers) < 2 {
				t.Fatalf("style %s: expected at least 2 layers, got %d", styleRef, len(result.Plan.Layers))
			}

			// Group 4 (16..20) keeps the entity caption without a separate
			// GPU badge shape, which can poison Vulkan during image-card export.
			if i >= 16 && i <= 20 {
				foundCaption := false
				for _, layer := range result.Plan.Layers {
					if layer.Type == "text" {
						foundCaption = true
					}
					if strings.HasSuffix(layer.ID, ":entity_badge") {
						t.Fatalf("style %s emitted unsupported entity badge shape", styleRef)
					}
				}
				if !foundCaption {
					t.Fatalf("style %s has no entity caption", styleRef)
				}
			}

			// Group 5 (21..25) must compile camera animation
			if i >= 21 && i <= 25 {
				if result.Plan.Camera == nil {
					t.Fatalf("style %s is Group 5 camera movement but plan.Camera is nil", styleRef)
				}
				if result.Plan.CameraAnimation == nil || len(result.Plan.CameraAnimation.Tracks) == 0 {
					t.Fatalf("style %s is Group 5 camera movement but plan.CameraAnimation has no tracks", styleRef)
				}
			}
		})
	}
}

func TestBadgeRuntimeYellowRedRandomization(t *testing.T) {
	// The runtime badge color must always be Yellow or Red, deterministically distributed
	colors := make(map[string]int)
	for i := 0; i < 20; i++ {
		itemID := fmt.Sprintf("badge-item-%d", i)
		style, ok := ResolveEntityStyle("16", "plan-1", "video-1", itemID)
		if !ok {
			t.Fatalf("failed to resolve badge style")
		}
		if !style.IsBadge {
			t.Fatalf("expected is_badge=true")
		}
		if style.BadgeColor != "#FFDE00" && style.BadgeColor != "#EB1E2D" {
			t.Fatalf("unexpected badge color %s, want #FFDE00 or #EB1E2D", style.BadgeColor)
		}
		if style.BadgeColor == "#FFDE00" && style.CaptionColor != "#0A0B0E" {
			t.Fatalf("yellow badge must have dark caption color #0A0B0E, got %s", style.CaptionColor)
		}
		if style.BadgeColor == "#EB1E2D" && style.CaptionColor != "#FFFFFF" {
			t.Fatalf("red badge must have white caption color #FFFFFF, got %s", style.CaptionColor)
		}
		colors[style.BadgeColor]++
	}

	// Over 20 distinct items, both yellow and red must be hit
	if colors["#FFDE00"] == 0 || colors["#EB1E2D"] == 0 {
		t.Fatalf("runtime randomization did not produce both yellow and red: %+v", colors)
	}
}

func TestGroupSelectors(t *testing.T) {
	for _, selector := range []string{"testo_sotto", "below", "badge", "camera", "side", "typewriter"} {
		style, ok := ResolveEntityStyle(selector, "plan-1", "video-1", "item-1")
		if !ok {
			t.Fatalf("failed to resolve selector %s", selector)
		}
		if style.Name == "" || style.ImageMotionID == "" {
			t.Fatalf("selector %s resolved incomplete style: %+v", selector, style)
		}
	}
}
