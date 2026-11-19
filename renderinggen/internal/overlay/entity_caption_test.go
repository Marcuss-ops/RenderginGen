package overlay

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func compileEntityCaptionForTest(t *testing.T, caption string, frame ...bool) []Layer {
	t.Helper()
	useFrame := len(frame) > 0 && frame[0]
	return compileEntityCaptionPlanForTest(t, caption, useFrame, nil)
}

func compileEntityCaptionPlanForTest(t *testing.T, caption string, frame bool, style map[string]any) []Layer {
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
	if frame {
		item["frame"] = map[string]any{"border": map[string]any{
			"width_px": 4.0, "color": "#FFFFFF", "radius_px": 24.0,
		}}
	}
	if style != nil {
		item["params"] = map[string]any{"style": style}
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
	if caption.Style.Glow != nil {
		t.Fatalf("caption glow = %+v; want no full-frame GPU halo", caption.Style.Glow)
	}
}

func TestEntityCaptionLowersPayloadCardBackground(t *testing.T) {
	layers := compileEntityCaptionPlanForTest(t, "Ada Lovelace", false, map[string]any{
		"background": map[string]any{
			"color":   "#1B2A3A",
			"opacity": 0.85,
			"radius":  12.0,
			"padding": []any{18.0, 10.0},
		},
	})
	if len(layers) != 3 {
		t.Fatalf("got %d layers, want image + plate + caption", len(layers))
	}
	caption := layers[2]
	if caption.Type != "text" {
		t.Fatalf("layer 2 = %q, want the caption text", caption.Type)
	}
	if caption.Style == nil || caption.Style.Background != nil {
		t.Fatalf("caption still carries a text box background: %+v", caption.Style)
	}
	plateLayer := layers[1]
	if plateLayer.Type != "shape" || plateLayer.Shape == nil {
		t.Fatalf("payload card_background did not lower to a shape plate: %+v", plateLayer)
	}
	if plateLayer.ID != caption.ID+":plate" {
		t.Fatalf("plate id = %q, want %q", plateLayer.ID, caption.ID+":plate")
	}
	if plateLayer.StartFrame != caption.StartFrame || plateLayer.DurationFrames != caption.DurationFrames {
		t.Fatalf("plate lifetime = [%d,%d), caption = [%d,%d)", plateLayer.StartFrame, plateLayer.DurationFrames, caption.StartFrame, caption.DurationFrames)
	}
	if plateLayer.Shape.Type != "rounded_rect" || plateLayer.Shape.Radius != 12 {
		t.Fatalf("plate shape = %+v", plateLayer.Shape)
	}
	fill, ok := plateLayer.Shape.Fill.([]float64)
	if !ok || len(fill) != 4 || math.Abs(fill[0]-float64(0x1B)/255) > 1e-9 || math.Abs(fill[1]-float64(0x2A)/255) > 1e-9 || math.Abs(fill[2]-float64(0x3A)/255) > 1e-9 || fill[3] != 1 {
		t.Fatalf("plate fill = %#v", plateLayer.Shape.Fill)
	}
	if plateLayer.Opacity == nil || *plateLayer.Opacity != 0.85 {
		t.Fatalf("caption plate opacity = %v, want 0.85", plateLayer.Opacity)
	}
	if len(plateLayer.Size) != 2 || plateLayer.Size[0] != caption.Size[0]+36 || plateLayer.Size[1] != caption.Size[1]+20 {
		t.Fatalf("plate size = %v, want caption size %v plus padding", plateLayer.Size, caption.Size)
	}
	if len(plateLayer.Position) != 2 || len(caption.Position) != 2 ||
		math.Abs(plateLayer.Position[0]-(caption.Position[0]-plateLayer.Size[0]/2)) > 1e-9 ||
		math.Abs(plateLayer.Position[1]-(caption.Position[1]-plateLayer.Size[1]/2)) > 1e-9 {
		t.Fatalf("plate top-left = %v, want caption centre %v minus half plate %v", plateLayer.Position, caption.Position, plateLayer.Size)
	}
}

func TestEntityCaptionRejectsMalformedCardBackground(t *testing.T) {
	for name, background := range map[string]any{
		"missing color": map[string]any{"radius": 8.0},
		"bad color":     map[string]any{"color": "navy"},
		"bad opacity":   map[string]any{"color": "#1B2A3A", "opacity": 2.0},
		"bad radius":    map[string]any{"color": "#1B2A3A", "radius": -1.0},
		"bad padding":   map[string]any{"color": "#1B2A3A", "padding": []any{1.0, 2.0, 3.0}},
	} {
		t.Run(name, func(t *testing.T) {
			item := map[string]any{
				"id": "portrait", "kind": "entity_card", "template_id": "PERSON",
				"preset_id": "phrase_default", "text": "Ada Lovelace",
				"image_preset_id": "image_scale_in", "entity_caption": "Ada Lovelace",
				"start_ms": 0, "end_ms": 1000, "duration_ms": 1000,
				"asset_refs": []any{map[string]any{
					"asset_id": "portrait", "sha256": strings.Repeat("a", 64),
					"url": "https://store.example/portrait.png", "media_type": "image/png",
				}},
				"params": map[string]any{"style": map[string]any{"background": background}},
			}
			plan := map[string]any{
				"schema_version": "renderinggen.overlay-plan.v1", "plan_id": "bad-plate",
				"video_id": "bad-plate", "width": 1280, "height": 720,
				"fps_num": 30, "fps_den": 1, "items": []any{item},
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := CompileSemantic(raw); err == nil {
				t.Fatal("expected a malformed card background to fail closed")
			}
		})
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
			captionX, captionY := textLayerCanvasPosition(caption, 1280, 720)
			if captionX-caption.Size[0]/2 < 24 || captionX+caption.Size[0]/2 > 1280-24 {
				t.Fatalf("caption bounds escape horizontal safe area: x=%v size=%v", captionX, caption.Size)
			}
			if captionY-caption.Size[1]/2 < 23.99 || captionY+caption.Size[1]/2 > 720-24+0.01 {
				t.Fatalf("caption bounds escape vertical safe area: y=%v size=%v", captionY, caption.Size)
			}
		})
	}
}

func TestCaptionedEntityImageChainKeepsCaptionBelowEachPortrait(t *testing.T) {
	portrait := func(id string) map[string]any {
		return map[string]any{
			"asset_id": id, "sha256": strings.Repeat("a", 64),
			"url": "https://store.example/" + id + ".png", "media_type": "image/png",
		}
	}
	segment := func(id, caption string, startUS, durationUS int64) map[string]any {
		return map[string]any{
			"kind": "entity_image", "template_id": "image_popup", "entity_id": id,
			"entity_caption": caption, "caption_layout": "below",
			"caption_motion_id": "entity_yaw_caption", "caption_motion_params": map[string]any{"enter_frames": 8},
			"entity_style_id": "premium_below_random_v1",
			"motion_id":       "image_focus_reveal", "image_preset_id": "image_scale_in",
			"start_us": startUS, "duration_us": durationUS,
			"assets": []any{portrait(id)},
			"params": map[string]any{
				"position_x": 0.0, "position_y": 0.0, "width": 820.0, "height": 820.0,
			},
		}
	}
	item := map[string]any{
		"id": "entity-chain", "kind": "image_chain", "template_id": "IMAGE_CHAIN",
		"start_ms": 0, "end_ms": 6000,
		"asset_refs": []any{portrait("person:ada"), portrait("person:grace")},
		"params": map[string]any{"chain_segments": []any{
			segment("person:ada", "Ada Lovelace", 0, 3_000_000),
			segment("person:grace", "Grace Hopper", 3_400_000, 2_600_000),
		}},
	}
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "entity-chain-caption-plan",
		"video_id":       "entity-chain-caption-video",
		"width":          1920, "height": 1080,
		"fps_num": 24, "fps_den": 1,
		"items": []any{item},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic captioned entity chain: %v", err)
	}
	captions := map[string]*Layer{}
	images := map[string]*Layer{}
	for i := range result.Plan.Layers {
		layer := &result.Plan.Layers[i]
		switch layer.Text {
		case "Ada Lovelace", "Grace Hopper":
			captions[layer.Text] = layer
		}
		if layer.Type == "image" {
			images[layer.ID] = layer
		}
	}
	for _, caption := range []string{"Ada Lovelace", "Grace Hopper"} {
		layer := captions[caption]
		if layer == nil {
			t.Fatalf("missing caption %q in entity chain: %+v", caption, result.Plan.Layers)
		}
		if layer.CaptionLayout != "below" {
			t.Fatalf("caption %q layout = %q, want below", caption, layer.CaptionLayout)
		}
		if layer.Type != "text" || len(layer.Position) < 2 || len(layer.Size) < 2 {
			t.Fatalf("caption %q did not lower to a positioned text layer: %+v", caption, layer)
		}
		if layer.CaptionForImageID == "" {
			t.Fatalf("caption %q is not linked to its portrait", caption)
		}
		if !layer.Enable3D {
			t.Fatalf("caption %q did not take the 3D caption-motion path", caption)
		}
		if math.Abs(layer.Position[0]-960) > 1e-6 {
			t.Fatalf("3D caption %q position x = %v, want absolute canvas centre 960", caption, layer.Position[0])
		}
	}
	if len(images) != 2 {
		t.Fatalf("entity chain lowered %d image layers, want two portraits", len(images))
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
