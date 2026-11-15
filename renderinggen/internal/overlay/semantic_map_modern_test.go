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
	var paper, graticule, label, credit bool
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
		if !strings.Contains(layer.ID, ":modernmap1:territory:") {
			continue
		}
		// Region-anchored triangle fill; the dense outline carries the silhouette.
		if layer.Type != "shape" || layer.Shape == nil || layer.Shape.Type != "path" || len(layer.Shape.Path) != 4 || layer.Shape.Fill == nil {
			t.Fatalf("ModernMap1 territory %q is not a filled triangle: %+v", layer.ID, layer)
		}
		fills++
	}
	if fills < 40 {
		t.Fatalf("ModernMap1 emitted %d fill bands, want a scanline territory", fills)
	}
	if outlines == 0 || maxOutlineCommands < 100 {
		t.Fatalf("ModernMap1 outlines = %d, max commands = %d, want a smooth editorial outline", outlines, maxOutlineCommands)
	}
	if borders == 0 {
		t.Fatalf("no ModernMap1 context border layer compiled: %+v", result.Plan.Layers)
	}
	if !paper || !graticule || !label || !credit {
		t.Fatalf("ModernMap1 plate incomplete: paper=%v graticule=%v label=%v credit=%v", paper, graticule, label, credit)
	}
}

// The fill must be cut from the exact ring the outline strokes. A coarser fill
// ring is invisible in layer counts but shows as red stopping inside the coast.
func TestModernMap1FillUsesOutlineRing(t *testing.T) {
	result, err := compileMapTestPlan(t, modernMapPlan("Italy", nil))
	if err != nil {
		t.Fatalf("compile ModernMap1 Italy: %v", err)
	}
	outlines := map[string]map[string]bool{}
	for _, layer := range result.Plan.Layers {
		if !strings.Contains(layer.ID, ":modernmap1:outline:") || layer.Shape == nil {
			continue
		}
		parts := strings.Split(layer.ID, ":")
		points := map[string]bool{}
		for _, command := range layer.Shape.Path {
			if len(command.Point) == 2 {
				points[fmt.Sprintf("%.6f,%.6f", command.Point[0], command.Point[1])] = true
			}
		}
		outlines[parts[len(parts)-1]] = points
	}
	fills := 0
	for _, layer := range result.Plan.Layers {
		if !strings.Contains(layer.ID, ":modernmap1:territory:") {
			continue
		}
		if layer.Shape == nil {
			t.Fatalf("ModernMap1 territory %q has no shape", layer.ID)
		}
		parts := strings.Split(layer.ID, ":")
		ring := outlines[parts[len(parts)-2]]
		if ring == nil {
			t.Fatalf("ModernMap1 territory %q has no matching outline", layer.ID)
		}
		for _, command := range layer.Shape.Path {
			if len(command.Point) != 2 {
				continue
			}
			key := fmt.Sprintf("%.6f,%.6f", command.Point[0], command.Point[1])
			if !ring[key] {
				t.Fatalf("ModernMap1 fill vertex %s of %q is not on its outline ring", key, layer.ID)
			}
		}
		fills++
	}
	if fills == 0 {
		t.Fatal("ModernMap1 emitted no triangles to compare with the outline")
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
