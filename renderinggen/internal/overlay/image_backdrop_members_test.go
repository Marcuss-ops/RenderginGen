package overlay

import (
	"fmt"
	"strings"
	"testing"
)

// TestImageBackdropProjectsOntoCompositeEntityPortraits certifies that a
// co-present entity portrait composite blurs each member's own photo behind
// its card: one shared item-level block must never leave a sibling's photo
// behind every portrait.
func compositeBackdropAsset(id string) string {
	return fmt.Sprintf(`{"asset_id":"%s","sha256":"%s","url":"https://example.test/%s.jpg","media_type":"image/jpeg"}`,
		id, strings.Repeat("b", 64), id)
}

func TestImageBackdropProjectsOntoCompositeEntityPortraits(t *testing.T) {
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1","legacy_composite_replay":true,
		"plan_id":"composite-backdrop","video_id":"composite-backdrop",
		"width":1280,"height":720,"fps_num":24,"fps_den":1,"duration_ms":3000,
		"items":[{
			"id":"image-pair","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in",
			"start_ms":0,"end_ms":3000,
			"asset_refs":[%s,%s],
			"image_layers":[
				{"id":"left","asset_id":"left","start_ms":0,"end_ms":2000,"preset_id":"image_focus_in","motion_id":"image_focus_reveal","params":{"width":250,"height":230,"position_x":-230,"position_y":0,"fit":"contain"}},
				{"id":"right","asset_id":"right","start_ms":750,"end_ms":3000,"preset_id":"image_focus_in","motion_id":"image_scale_reveal","params":{"width":250,"height":230,"position_x":230,"position_y":0,"fit":"contain"}}
			],
			"params":{"image_backdrop":{"zoom":1.25,"blur_px":34}}
		}]
	}`,
		compositeBackdropAsset("left"), compositeBackdropAsset("right")))
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile composite backdrop plan: %v", err)
	}
	backdrops := map[string]Layer{}
	for _, layer := range compiled.Plan.Layers {
		if strings.HasSuffix(layer.ID, ":image-backdrop") {
			backdrops[layer.Asset] = layer
		}
	}
	if len(backdrops) != 2 {
		t.Fatalf("want one backdrop per portrait asset, got %d: %+v", len(backdrops), compiled.Plan.Layers)
	}
	for _, asset := range []string{"assets/semantic/left.jpg", "assets/semantic/right.jpg"} {
		backdrop, ok := backdrops[asset]
		if !ok {
			t.Fatalf("no backdrop for %q in %+v", asset, compiled.Plan.Layers)
		}
		if len(backdrop.Position) != 2 || backdrop.Position[0] != 0 || backdrop.Position[1] != 0 {
			t.Fatalf("backdrop %q position = %v, want the canvas centre", asset, backdrop.Position)
		}
		if len(backdrop.Size) != 2 || backdrop.Size[0] != 1280 || backdrop.Size[1] != 720 {
			t.Fatalf("backdrop %q size = %v, want the full 1280x720 canvas", asset, backdrop.Size)
		}
		if len(backdrop.Effects) != 1 || backdrop.Effects[0].Type != "gaussian_blur" ||
			backdrop.Effects[0].Params["radius"] != 34.0 {
			t.Fatalf("backdrop %q effects = %+v, want the requested 34px blur", asset, backdrop.Effects)
		}
	}
}

// TestImageBackdropProjectsOntoEntityCard certifies the single-asset entity
// card path: an entity portrait lowers through compileEntityCard, which must
// still blur its own photo behind the card. The composite branch of
// compileImageItem never sees these items, so this call is their only owner.
func TestImageBackdropProjectsOntoEntityCard(t *testing.T) {
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":"entity-backdrop","video_id":"entity-backdrop",
		"width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_ms":3000,
		"items":[{
			"id":"portrait","kind":"entity_image","template_id":"image_popup",
			"preset_id":"image_scale_in","entity_style_id":"premium_below_random_v1",
			"entity_caption":"Lennox Lewis","image_preset_id":"image_scale_in",
			"start_ms":0,"end_ms":3000,
			"asset_refs":[%s],
			"params":{"width":820,"height":820,"image_backdrop":{"zoom":1.25,"blur_px":34}}
		}]
	}`, compositeBackdropAsset("portrait")))
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile entity backdrop plan: %v", err)
	}
	found := false
	for _, layer := range compiled.Plan.Layers {
		if strings.HasSuffix(layer.ID, ":image-backdrop") {
			found = true
			if !strings.HasSuffix(layer.Asset, "portrait.jpg") {
				t.Fatalf("entity backdrop asset = %q, want the portrait photo", layer.Asset)
			}
			if len(layer.Size) != 2 || layer.Size[0] != 1920 || layer.Size[1] != 1080 {
				t.Fatalf("entity backdrop size = %v, want the full canvas", layer.Size)
			}
			if len(layer.Effects) != 1 || layer.Effects[0].Type != "gaussian_blur" {
				t.Fatalf("entity backdrop effects = %+v, want the requested blur", layer.Effects)
			}
		}
	}
	if !found {
		t.Fatalf("entity card got no backdrop layer: %+v", compiled.Plan.Layers)
	}
}

// TestImageBackdropProjectsOntoChainSegmentsAndStaysCentred certifies the
// chain contract: a chain-level block blurs each segment's own photo, and a
// camera-pan strip moves the photos without dragging the blurred set
// off-canvas.
func TestImageBackdropProjectsOntoChainSegmentsAndStaysCentred(t *testing.T) {
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-backdrop","video_id":"chain-backdrop",
		"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_ms":9000,
		"items":[{
			"id":"chain-backdrop-1","kind":"image_chain","template_id":"IMAGE_CHAIN",
			"start_ms":0,"end_ms":9000,
			"asset_refs":[%s,%s,%s],
			"params":{
				"chain_camera":"image_group_camera_pan_right","chain_count":3,
				"image_backdrop":{"zoom":1.25,"blur_px":34},
				"chain_segments":[
					%s,
					%s,
					%s
				]
			}
		}]
	}`,
		panTestAsset("ada"), panTestAsset("grace"), panTestAsset("alan"),
		panTestSegment("ada", "Ada", 0),
		panTestSegment("grace", "Grace", 3_000_000),
		panTestSegment("alan", "Alan", 6_000_000),
	))
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic chain backdrop: %v", err)
	}
	backdrops := map[string]Layer{}
	for _, layer := range result.Plan.Layers {
		if strings.HasSuffix(layer.ID, ":image-backdrop") {
			backdrops[layer.Asset] = layer
		}
	}
	if len(backdrops) != 3 {
		t.Fatalf("want one backdrop per segment asset, got %d: %+v", len(backdrops), result.Plan.Layers)
	}
	for index, id := range []string{"ada", "grace", "alan"} {
		backdrop, ok := backdrops[fmt.Sprintf("assets/semantic/asset-%s.jpg", id)]
		if !ok {
			t.Fatalf("no backdrop for segment %s in %+v", id, result.Plan.Layers)
		}
		if len(backdrop.Position) != 2 || backdrop.Position[0] != 0 || backdrop.Position[1] != 0 {
			t.Fatalf("segment %s backdrop position = %v, want it pinned to the canvas", id, backdrop.Position)
		}
		if backdrop.Animation != nil || len(backdrop.ChainPanOffset) != 0 {
			t.Fatalf("segment %s backdrop moves with the strip: animation=%+v offset=%v", id, backdrop.Animation, backdrop.ChainPanOffset)
		}
		image := findChainPanLayer(t, result.Plan.Layers, fmt.Sprintf("seg%d", index), "image")
		if !strings.HasSuffix(image.ID, ":image") {
			// findChainPanLayer returns the first image-type match, which is now
			// the backdrop; the photo is the layer ending in ":image".
			for i := range result.Plan.Layers {
				if strings.Contains(result.Plan.Layers[i].ID, fmt.Sprintf("seg%d", index)) &&
					strings.HasSuffix(result.Plan.Layers[i].ID, ":image") {
					image = &result.Plan.Layers[i]
					break
				}
			}
		}
		if image.Position[0] != float64(index)*1280 {
			t.Fatalf("segment %s image = %s at x=%v, want the strip slot %v", id, image.ID, image.Position[0], float64(index)*1280)
		}
	}
}
