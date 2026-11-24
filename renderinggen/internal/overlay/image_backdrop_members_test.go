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
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"composite-backdrop","video_id":"composite-backdrop",
		"width":1280,"height":720,"fps_num":24,"fps_den":1,"duration_ms":3000,
		"items":[{
			"id":"image-pair","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in","group_motion_id":"image_group_slide_right",
			"start_ms":0,"end_ms":3000,
			"asset_refs":[%s,%s],
			"image_layers":[
				{"id":"left","asset_id":"left","start_ms":0,"end_ms":2000,"preset_id":"image_focus_in","params":{"width":250,"height":230,"position_x":-230,"position_y":0,"fit":"contain"}},
				{"id":"right","asset_id":"right","start_ms":750,"end_ms":3000,"preset_id":"image_focus_in","params":{"width":250,"height":230,"position_x":230,"position_y":0,"fit":"contain"}}
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

// TestImageBackdropProjectsOntoChainSegmentsAndStaysCentred certifies that a
// camera-pan chain drops the requested backdrop: Chronon's software blur pass
// leaves the blurred surface bound to the next image, so the incoming card
// would render the backdrop's asset. The strip is the set.
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
	for _, layer := range result.Plan.Layers {
		if strings.HasSuffix(layer.ID, ":image-backdrop") {
			t.Fatalf("pan chain emitted a backdrop despite the strip-only contract: %+v", layer)
		}
	}
	for index, id := range []string{"ada", "grace", "alan"} {
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

// TestImageBackdropGridIsEmittedOnceForComposite pins the unified entry point:
// two member photos produce two backdrops, the grid is appended exactly once,
// and every backdrop precedes the photo layers (draw order).
func TestImageBackdropGridIsEmittedOnceForComposite(t *testing.T) {
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"composite-backdrop-grid","video_id":"composite-backdrop-grid",
		"width":1280,"height":720,"fps_num":24,"fps_den":1,"duration_ms":3000,
		"items":[{
			"id":"image-pair","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in","group_motion_id":"image_group_slide_right",
			"start_ms":0,"end_ms":3000,
			"asset_refs":[%s,%s],
			"image_layers":[
				{"id":"left","asset_id":"left","start_ms":0,"end_ms":2000,"preset_id":"image_focus_in","params":{"width":250,"height":230,"position_x":-230,"position_y":0,"fit":"contain"}},
				{"id":"right","asset_id":"right","start_ms":750,"end_ms":3000,"preset_id":"image_focus_in","params":{"width":250,"height":230,"position_x":230,"position_y":0,"fit":"contain"}}
			],
			"params":{"image_backdrop":{"zoom":1.25,"blur_px":34,"grid":{"style":"lines","color":"#7FA8C8","spacing_px":64,"line_width_px":1,"opacity":0.12}}}
		}]
	}`, compositeBackdropAsset("left"), compositeBackdropAsset("right")))
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile composite backdrop grid plan: %v", err)
	}
	var backdrops, grids, firstPhoto int = 0, 0, -1
	for index, layer := range compiled.Plan.Layers {
		switch {
		case strings.HasSuffix(layer.ID, ":image-backdrop"):
			backdrops++
			if firstPhoto >= 0 {
				t.Fatalf("backdrop %q appears after the first photo at index %d", layer.ID, firstPhoto)
			}
		case strings.HasSuffix(layer.ID, ":image-grid"):
			grids++
		case layer.Type == "image" && firstPhoto < 0:
			firstPhoto = index
		}
	}
	if backdrops != 2 {
		t.Fatalf("backdrops = %d, want one per member photo: %+v", backdrops, compiled.Plan.Layers)
	}
	if grids != 1 {
		t.Fatalf("backdrop grids = %d, want exactly one: %+v", grids, compiled.Plan.Layers)
	}
}
