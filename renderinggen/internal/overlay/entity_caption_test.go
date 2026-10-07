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
	if caption.CaptionForImageID != image.ID {
		t.Fatalf("entity caption image link = %q, want %q", caption.CaptionForImageID, image.ID)
	}
	if caption.ID != "portrait:entity:caption" {
		t.Fatalf("entity caption layer ID = %q, want stable legacy ID", caption.ID)
	}
	if image.StartFrame != caption.StartFrame || image.DurationFrames != caption.DurationFrames {
		t.Fatalf("lifetimes differ: image=(%d,%d) caption=(%d,%d)", image.StartFrame, image.DurationFrames, caption.StartFrame, caption.DurationFrames)
	}
	if caption.Position[1] <= image.Position[1] {
		t.Fatalf("caption center y=%v must be below image center y=%v", caption.Position[1], image.Position[1])
	}
	if caption.Style.Background != nil {
		t.Fatal("caption must avoid a Vulkan-unsupported text background card")
	}
	if caption.Style.Fill != "#F8F5EA" || caption.Style.FontSize < 36 || caption.Style.Stroke == nil || caption.Style.Stroke.Color != "#111827" || caption.Style.Stroke.Width < 1.5 {
		t.Fatalf("caption title style = %+v; want large warm-white lettering with a dark keyline", caption.Style)
	}
	if caption.Style.Shadow == nil || caption.Style.Shadow.Blur < 6 || caption.Style.Shadow.Opacity < 0.6 {
		t.Fatalf("caption shadow = %+v; want a soft cinematic separation from footage", caption.Style.Shadow)
	}
	if caption.Style.Glow == nil || caption.Style.Glow.Color != "#F8F5EA" || caption.Style.Glow.Radius < 10 || caption.Style.Glow.Intensity <= 0 || caption.Style.Glow.Intensity > 0.35 {
		t.Fatalf("caption glow = %+v; want a restrained warm halo", caption.Style.Glow)
	}
}

func TestEntityCaptionLongNameUsesLargerWrappedTitleAndRemainsReadable(t *testing.T) {
	layers := compileEntityCaptionForTest(t, "Alexander Boris de Pfeffel Johnson")
	caption := layers[1]
	if caption.Style == nil || caption.Style.FontSize <= 30 {
		t.Fatalf("wrapped long-name title font size = %+v; want a larger cinematic size after wrapping", caption.Style)
	}
	if len(caption.Size) != 2 || caption.Size[0] > 1280-48 || caption.Size[1] < 100 {
		t.Fatalf("long-name title geometry = %v; want wrapped lines inside safe area", caption.Size)
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
			if caption.Position[1]-caption.Size[1]/2 < 23.99 || caption.Position[1]+caption.Size[1]/2 > 720-24+0.01 {
				t.Fatalf("caption bounds escape vertical safe area: y=%v size=%v", caption.Position[1], caption.Size)
			}
		})
	}
}

func TestEntityStyleTestoSottoWiring(t *testing.T) {
	stylesToTest := []string{
		"testo_sotto",
		"below",
		"Center top",
		"Vertical editorial",
		"Centered signature",
		"premium_random_v1",
	}

	for _, styleID := range stylesToTest {
		t.Run(styleID, func(t *testing.T) {
			item := map[string]any{
				"id":              "portrait",
				"entity_id":       "person:trump",
				"kind":            "entity_card",
				"template_id":     "PERSON",
				"preset_id":       "phrase_default",
				"text":            "Donald Trump",
				"image_preset_id": "image_scale_in",
				"entity_caption":  "Donald Trump",
				"entity_style_id": styleID,
				"start_ms":        0, "end_ms": 2000, "duration_ms": 2000,
				"asset_refs": []any{map[string]any{
					"asset_id":   "portrait-asset",
					"sha256":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					"url":        "https://store.example/portrait.png",
					"media_type": "image/png",
				}},
			}
			plan := map[string]any{
				"schema_version": "renderinggen.overlay-plan.v1",
				"plan_id":        "entity-sotto-plan",
				"video_id":       "entity-sotto-video",
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
				t.Fatalf("CompileSemantic failed for style %q: %v", styleID, err)
			}
			if len(result.Plan.Layers) < 2 {
				t.Fatalf("expected at least 2 layers, got %d", len(result.Plan.Layers))
			}
		})
	}
}
