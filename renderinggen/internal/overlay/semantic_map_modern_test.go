package overlay

import (
	"fmt"
	"strings"
	"testing"
)

func modernMapPlan(place string, extra map[string]any) map[string]any {
	params := map[string]any{"map_type": MapTypeModernMap1}
	if place != "" {
		params["map_place"] = place
	}
	for key, value := range extra {
		params[key] = value
	}
	return modernMapPlanWithAssets(params, nil)
}

func modernMapPlanWithAssets(params map[string]any, assets []any) map[string]any {
	item := map[string]any{
		"id": "shape-map", "kind": "map", "template_id": "MAP",
		"start_ms": 0, "end_ms": 8000,
		"params": params,
		"map": map[string]any{
			"provider": "local", "source_id": "natural-earth", "source_license": "public-domain",
			"center": map[string]any{"latitude": 0.0, "longitude": 0.0},
			"zoom":   0, "width": 1920, "height": 1080,
			"attribution": "Natural Earth", "pins": []any{},
		},
	}
	if assets != nil {
		item["asset_refs"] = assets
	}
	return map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "modernmap1-plan", "video_id": "modernmap1-video",
		"width": 1920, "height": 1080, "fps_num": 30, "fps_den": 1,
		"background": map[string]any{"kind": "color", "color": []float64{0.03, 0.05, 0.08, 1}},
		"items":      []any{item},
	}
}

func modernMapLogoAsset() map[string]any {
	return map[string]any{
		"asset_id": "italy-flag", "sha256": strings.Repeat("a", 64),
		"url": "assets/logos/italy-flag.png", "media_type": "image/png",
	}
}

func TestModernMap1CompilesEditorialPlate(t *testing.T) {
	result, err := compileMapTestPlan(t, modernMapPlan("Italy", nil))
	if err != nil {
		t.Fatalf("compile ModernMap1 Italy: %v", err)
	}
	var fills, outlines, borders, maxOutlineCommands int
	var paper, graticule, label, credit, wipe bool
	for _, layer := range result.Plan.Layers {
		switch {
		case layer.ID == "shape-map:modernmap1:paper":
			paper = layer.Shape != nil && layer.Shape.Type == "rect" && layer.Shape.Fill != nil
		case layer.ID == "shape-map:modernmap1:graticule":
			graticule = layer.Shape != nil && layer.Shape.Stroke != nil && layer.Shape.Fill == nil
		case strings.HasPrefix(layer.ID, "shape-map:modernmap1:borders:"):
			if layer.Shape == nil || layer.Shape.Stroke == nil || layer.Shape.Fill != nil {
				t.Fatalf("ModernMap1 border layer %q is not a stroke-only path", layer.ID)
			}
			borders++
		case layer.ID == "shape-map:modernmap1:label":
			label = layer.Type == "text" && layer.Text != ""
		case layer.ID == "shape-map:modernmap1:credit":
			credit = layer.Type == "text"
		case layer.ID == "shape-map:modernmap1:fillwipe":
			// The reveal is one paper cover with a soft leading edge sweeping
			// west to east, so the fill itself can stay static.
			wipe = layer.Type == "shape" && layer.Shape != nil && layer.Shape.Type == "rect" &&
				layer.Animation != nil && len(layer.Animation.Tracks) == 1 &&
				layer.Animation.Tracks[0].Property == "position_x" &&
				len(layer.Animation.Tracks[0].Keyframes) == 2
		}
		if strings.Contains(layer.ID, ":modernmap1:outline:") {
			if layer.Shape == nil || layer.Shape.Stroke == nil || layer.Shape.Fill != nil {
				t.Fatalf("ModernMap1 outline %q is not a stroke-only path: %+v", layer.ID, layer)
			}
			outlines++
			if len(layer.Shape.Path) > maxOutlineCommands {
				maxOutlineCommands = len(layer.Shape.Path)
			}
			continue
		}
		if !strings.Contains(layer.ID, ":modernmap1:territory") {
			continue
		}
		// One plate-wide even-odd fill: every ring is a subpath of a single
		// closed path, so the native renderer needs one dispatch instead of
		// dozens of per-triangle layers with visible seams.
		if layer.Type != "shape" || layer.Shape == nil || layer.Shape.Type != "path" ||
			layer.Shape.Fill == nil || layer.Shape.Stroke != nil {
			t.Fatalf("ModernMap1 territory %q is not a fill-only path: %+v", layer.ID, layer)
		}
		closes := 0
		for _, command := range layer.Shape.Path {
			if command.Type == "close" {
				closes++
			}
		}
		if closes < 2 {
			t.Fatalf("ModernMap1 territory %q has %d closed subpaths, want the whole plate in one path", layer.ID, closes)
		}
		fills++
	}
	if fills != 1 {
		t.Fatalf("ModernMap1 emitted %d territory fills, want exactly one native plate fill", fills)
	}
	if outlines == 0 || maxOutlineCommands < 100 {
		t.Fatalf("ModernMap1 outlines = %d, max commands = %d, want a smooth editorial outline", outlines, maxOutlineCommands)
	}
	if borders == 0 {
		t.Fatalf("no ModernMap1 context border layer compiled: %+v", result.Plan.Layers)
	}
	if !paper || !graticule || !label || !credit || !wipe {
		t.Fatalf("ModernMap1 plate incomplete: paper=%v graticule=%v label=%v credit=%v wipe=%v", paper, graticule, label, credit, wipe)
	}
}

// The fill must be cut from the exact rings the outlines stroke. A coarser fill
// ring is invisible in layer counts but shows as red stopping inside the coast.
func TestModernMap1FillUsesOutlineRing(t *testing.T) {
	result, err := compileMapTestPlan(t, modernMapPlan("Italy", nil))
	if err != nil {
		t.Fatalf("compile ModernMap1 Italy: %v", err)
	}
	// Every outline ring is authored in its own bbox-local space; the plate fill
	// uses the union bbox. Compare in canvas space: local + position + canvas/2.
	canvasOffset := func(layer Layer) (float64, float64) {
		return layer.Position[0] + 1920.0/2, layer.Position[1] + 1080.0/2
	}
	ringPoints := map[string]bool{}
	outlines := 0
	for _, layer := range result.Plan.Layers {
		if !strings.Contains(layer.ID, ":modernmap1:outline:") || layer.Shape == nil {
			continue
		}
		outlines++
		dx, dy := canvasOffset(layer)
		for _, command := range layer.Shape.Path {
			if len(command.Point) == 2 {
				ringPoints[fmt.Sprintf("%.3f,%.3f", command.Point[0]+dx, command.Point[1]+dy)] = true
			}
		}
	}
	if outlines == 0 {
		t.Fatal("ModernMap1 emitted no outlines to compare with the fill")
	}
	fills := 0
	for _, layer := range result.Plan.Layers {
		if !strings.Contains(layer.ID, ":modernmap1:territory") {
			continue
		}
		if layer.Shape == nil {
			t.Fatalf("ModernMap1 territory %q has no shape", layer.ID)
		}
		dx, dy := canvasOffset(layer)
		for _, command := range layer.Shape.Path {
			if len(command.Point) != 2 {
				continue
			}
			key := fmt.Sprintf("%.3f,%.3f", command.Point[0]+dx, command.Point[1]+dy)
			if !ringPoints[key] {
				t.Fatalf("ModernMap1 fill vertex %s of %q is not on an outline ring", key, layer.ID)
			}
		}
		fills++
	}
	if fills != 1 {
		t.Fatalf("ModernMap1 emitted %d plate fills, want exactly one", fills)
	}
}

func TestModernMap1PlacesLogoInsideTerritory(t *testing.T) {
	params := map[string]any{"map_type": MapTypeModernMap1, "map_place": "Italy", "map_logo": "italy-flag"}
	result, err := compileMapTestPlan(t, modernMapPlanWithAssets(params, []any{modernMapLogoAsset()}))
	if err != nil {
		t.Fatalf("compile ModernMap1 with logo: %v", err)
	}
	var logo *Layer
	for index := range result.Plan.Layers {
		if result.Plan.Layers[index].ID == "shape-map:modernmap1:logo" {
			logo = &result.Plan.Layers[index]
		}
	}
	if logo == nil {
		t.Fatalf("ModernMap1 logo layer missing: %+v", result.Plan.Layers)
	}
	if logo.Type != "image" || logo.Asset != "assets/logos/italy-flag.png" || logo.BoxWidth <= 0 || logo.BoxHeight <= 0 {
		t.Fatalf("ModernMap1 logo layer = %+v, want a sized image of the declared asset", logo)
	}
	if float64(logo.BoxWidth) < modernMapLogoMinSizePX || float64(logo.BoxWidth) > modernMapLogoMaxSizePX {
		t.Fatalf("ModernMap1 logo width %d outside the editorial badge range [%v,%v]", logo.BoxWidth, modernMapLogoMinSizePX, modernMapLogoMaxSizePX)
	}
	if logo.Position[0] < -960 || logo.Position[0] > 960 || logo.Position[1] < -540 || logo.Position[1] > 540 {
		t.Fatalf("ModernMap1 logo position %v is outside the canvas", logo.Position)
	}
}

func TestModernMap1ClassicDefaultStillRequiresRaster(t *testing.T) {
	plan := modernMapPlan("Italy", map[string]any{"map_type": MapTypeClassic})
	item := plan["items"].([]any)[0].(map[string]any)
	delete(item["params"].(map[string]any), "map_place")
	// A classic map item must still carry its PNG plate; dropping the raster
	// without switching family has to fail closed.
	if _, err := compileMapTestPlan(t, plan); err == nil {
		t.Fatal("classic map without a raster asset must fail closed")
	}
}

func TestModernMap1RejectsUnknownTypeAndMissingPlace(t *testing.T) {
	if _, err := compileMapTestPlan(t, modernMapPlan("Italy", map[string]any{"map_type": "modernmap1"})); err == nil ||
		!strings.Contains(err.Error(), "unsupported map_type") {
		t.Fatalf("unknown map_type error = %v, want unsupported map_type", err)
	}
	if _, err := compileMapTestPlan(t, modernMapPlan("", nil)); err == nil ||
		!strings.Contains(err.Error(), "map_place") {
		t.Fatalf("missing map_place error = %v, want map_place", err)
	}
	if _, err := compileMapTestPlan(t, modernMapPlan("Atlantis", nil)); err == nil {
		t.Fatal("unknown Natural Earth place must fail closed")
	}
	if _, err := compileMapTestPlan(t, modernMapPlan("Italy", map[string]any{"map_logo": "italy-flag"})); err == nil ||
		!strings.Contains(err.Error(), "map_logo") {
		t.Fatalf("logo without asset error = %v, want map_logo", err)
	}
}
