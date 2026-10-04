package overlay

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// TestMixedSemanticPlanPreservesMotionDiversityThroughChrononLowering is the
// renderer half of PipelineGen's mixed motion-diversity plan test. It feeds a
// producer-shaped overlay-plan.v1 payload through the real semantic compiler
// and checks that every phrase and generated image keeps distinct concrete
// tracks in the Chronon wire plan.
func TestMixedSemanticPlanPreservesMotionDiversityThroughChrononLowering(t *testing.T) {
	phraseMotions := []string{
		"risograph_offset_print",
		"editorial_line_build",
		"typewriter_glitch",
		"shutter_blade_reveal",
	}
	imageMotions := []string{
		"image_fade_reveal",
		"image_focus_reveal",
		"image_scale_reveal",
		"image_slide_left_reveal",
	}
	items := make([]any, 0, len(phraseMotions)+len(imageMotions))
	for i, motionID := range phraseMotions {
		items = append(items, map[string]any{
			"id": fmt.Sprintf("phrase-%d", i+1), "scene_id": "scene-1",
			"kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
			"preset_id": "phrase_default", "motion_id": motionID,
			"motion_params": map[string]any{"enter_frames": 22},
			"text":          fmt.Sprintf("Grounded phrase number %d", i+1),
			"start_ms":      i * 2_000, "end_ms": i*2_000 + 1_800,
		})
	}
	for i, motionID := range imageMotions {
		assetID := fmt.Sprintf("image-%d", i+1)
		items = append(items, map[string]any{
			"id": assetID, "scene_id": "scene-1", "kind": "image",
			"template_id": "IMAGE_OVERLAY", "preset_id": "image_focus_in",
			"motion_id": motionID,
			"start_ms":  i*2_000 + 1_000, "end_ms": i*2_000 + 1_900,
			"params": map[string]any{"position": "center"},
			"asset_refs": []any{map[string]any{
				"asset_id":   assetID,
				"sha256":     fmt.Sprintf("%064x", i+1),
				"url":        fmt.Sprintf("https://example.test/%s.jpg", assetID),
				"media_type": "image/jpeg",
			}},
		})
	}
	semantic, err := json.Marshal(map[string]any{
		"schema_version": SemanticSchema,
		"plan_id":        "motion-diversity-e2e", "video_id": "motion-diversity-e2e",
		"width": 1280, "height": 720, "fps_num": 24, "fps_den": 1,
		"duration_ms": 8_000, "items": items,
	})
	if err != nil {
		t.Fatalf("marshal semantic plan: %v", err)
	}

	compiled, err := CompileSemantic(semantic)
	if err != nil {
		t.Fatalf("compile mixed semantic plan: %v", err)
	}
	if len(compiled.Plan.Layers) != len(items) {
		t.Fatalf("compiled %d layers, want one per semantic overlay (%d)", len(compiled.Plan.Layers), len(items))
	}

	wire, err := compiled.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal Chronon render plan: %v", err)
	}
	var chronon struct {
		Layers []struct {
			ID        string          `json:"id"`
			Type      string          `json:"type"`
			Animation *LayerAnimation `json:"animation"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(wire, &chronon); err != nil {
		t.Fatalf("decode Chronon render plan: %v", err)
	}
	if len(chronon.Layers) != len(items) {
		t.Fatalf("Chronon wire has %d layers, want %d", len(chronon.Layers), len(items))
	}

	phraseTracks := make(map[string]string, len(phraseMotions))
	imageTracks := make(map[string]string, len(imageMotions))
	for _, layer := range chronon.Layers {
		if layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
			t.Fatalf("layer %q (%s) has no lowered animation tracks", layer.ID, layer.Type)
		}
		encoded, err := json.Marshal(layer.Animation.Tracks)
		if err != nil {
			t.Fatalf("marshal animation tracks for %q: %v", layer.ID, err)
		}
		key := string(encoded)
		seen := phraseTracks
		if layer.Type == "image" {
			seen = imageTracks
		} else if layer.Type != "text" {
			t.Fatalf("unexpected layer type %q for %q", layer.Type, layer.ID)
		}
		if previous, duplicate := seen[key]; duplicate {
			t.Fatalf("layers %q and %q collapsed to identical %s animation tracks: %s", previous, layer.ID, layer.Type, key)
		}
		seen[key] = layer.ID
	}
	if len(phraseTracks) != len(phraseMotions) || len(imageTracks) != len(imageMotions) {
		t.Fatalf("unique lowered tracks = %d phrases / %d images, want %d / %d", len(phraseTracks), len(imageTracks), len(phraseMotions), len(imageMotions))
	}

	// Guard the actual in-memory lowering against accidental aliasing too: the
	// layer-track slices must remain independently authored per item.
	for i := 0; i < len(compiled.Plan.Layers); i++ {
		if compiled.Plan.Layers[i].Animation == nil {
			t.Fatalf("compiled layer %q has no animation", compiled.Plan.Layers[i].ID)
		}
		for j := i + 1; j < len(compiled.Plan.Layers); j++ {
			if compiled.Plan.Layers[j].Animation == nil {
				t.Fatalf("compiled layer %q has no animation", compiled.Plan.Layers[j].ID)
			}
			if compiled.Plan.Layers[i].Type == compiled.Plan.Layers[j].Type &&
				reflect.DeepEqual(compiled.Plan.Layers[i].Animation.Tracks, compiled.Plan.Layers[j].Animation.Tracks) {
				t.Fatalf("compiled layers %q and %q share identical motion tracks", compiled.Plan.Layers[i].ID, compiled.Plan.Layers[j].ID)
			}
		}
	}
}
