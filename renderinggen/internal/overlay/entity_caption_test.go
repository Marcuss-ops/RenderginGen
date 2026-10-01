package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

func compileEntityCaptionForTest(t *testing.T, caption string, frame ...bool) []Layer {
	t.Helper()
	item := map[string]any{
		"id":              "portrait",
		"entity_id":       "person:ada-lovelace",
		"kind":            "entity_card",
		"template_id":     "PERSON",
		"preset_id":       "phrase_default",
		"text":            caption,
		"image_preset_id": "image_scale_in",
		"entity_caption":  caption,
		"start_ms":        500, "end_ms": 2500, "duration_ms": 2000,
		"asset_refs": []any{map[string]any{
			"asset_id":   "portrait-asset",
			"sha256":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"url":        "https://store.example/portrait.png",
			"media_type": "image/png",
		}},
	}
	if len(frame) > 0 && frame[0] {
		item["frame"] = map[string]any{"border": map[string]any{
			"width_px": 4.0, "color": "#FFFFFF", "radius_px": 24.0,
		}}
	}
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "entity-caption-plan",
		"video_id":       "entity-caption-video",
		"width":          1280, "height": 720,
		"fps_num": 30, "fps_den": 1,
		"items": []any{item},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic: %v", err)
	}
	return result.Plan.Layers
}

func TestEntityImageFrameLowersRoundedStrokePlate(t *testing.T) {
	layers := compileEntityCaptionForTest(t, "Ada Lovelace", true)
	image := layers[0]
	if image.Style == nil || image.Style.Background == nil {
		t.Fatal("image border did not lower to the renderer's media frame background")
	}
	frame := image.Style.Background
	if frame.Color != "#FFFFFF" || frame.Radius != 24 || len(frame.Padding) != 2 || frame.Padding[0] != 4 || frame.Padding[1] != 4 {
		t.Fatalf("frame background = %+v", frame)
	}
	if image.Radius != 20 {
		t.Fatalf("inner image radius = %v, want 20 to nest in the 24px outer frame", image.Radius)
	}
}

func TestEntityCaptionEmitsImageAndCaptionWithSharedLifetime(t *testing.T) {
	layers := compileEntityCaptionForTest(t, "Ada Lovelace")
	if len(layers) != 2 {
		t.Fatalf("got %d layers, want image plus caption: %+v", len(layers), layers)
	}
	image, caption := layers[0], layers[1]
	if image.Type != "image" || caption.Type != "text" {
		t.Fatalf("layer types = %q, %q; want image, text", image.Type, caption.Type)
	}
	if caption.Text != "Ada Lovelace" {
		t.Fatalf("caption text = %q", caption.Text)
	}
	if image.StartFrame != caption.StartFrame || image.DurationFrames != caption.DurationFrames {
		t.Fatalf("lifetimes differ: image=(%d,%d) caption=(%d,%d)", image.StartFrame, image.DurationFrames, caption.StartFrame, caption.DurationFrames)
	}
	if caption.Position[1] <= image.Position[1] {
		t.Fatalf("caption center y=%v must be below image center y=%v", caption.Position[1], image.Position[1])
	}
	if caption.Style.Background == nil || caption.Style.Background.Radius <= 0 {
		t.Fatal("caption must use the shared rounded background style")
	}
}

func TestEntityCaptionKeepsLongAndUnicodeNamesInsideSafeArea(t *testing.T) {
	for _, name := range []string{"Alexander Boris de Pfeffel Johnson", "Xi Jinping"} {
		t.Run(name, func(t *testing.T) {
			layers := compileEntityCaptionForTest(t, name)
			caption := layers[1]
			if strings.Join(strings.Fields(caption.Text), " ") != name {
				t.Fatalf("caption text = %q, want all name text %q", caption.Text, name)
			}
			if len(caption.Size) != 2 || caption.Size[0] > 1280-48 || caption.Size[1] <= 0 {
				t.Fatalf("caption size %v exceeds the horizontal safe area", caption.Size)
			}
			if caption.Position[0]-caption.Size[0]/2 < 24 || caption.Position[0]+caption.Size[0]/2 > 1280-24 {
				t.Fatalf("caption bounds escape horizontal safe area: x=%v size=%v", caption.Position[0], caption.Size)
			}
			if caption.Position[1]-caption.Size[1]/2 < 24 || caption.Position[1]+caption.Size[1]/2 > 720-24 {
				t.Fatalf("caption bounds escape vertical safe area: y=%v size=%v", caption.Position[1], caption.Size)
			}
		})
	}
}
