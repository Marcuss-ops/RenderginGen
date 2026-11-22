package overlay

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
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

func TestModernMap1RouteKeepsFullGeometryWithNativeTrim(t *testing.T) {
	plan := modernMapPlan("Italy", nil)
	item := plan["items"].([]any)[0].(map[string]any)
	stops := []any{
		map[string]any{"latitude": 41.9028, "longitude": 12.4964},
		map[string]any{"latitude": 43.7696, "longitude": 11.2558},
		map[string]any{"latitude": 44.4949, "longitude": 11.3426},
		map[string]any{"latitude": 45.4642, "longitude": 9.19},
		map[string]any{"latitude": 45.0703, "longitude": 7.6869},
	}
	item["map"].(map[string]any)["routes"] = []any{
		map[string]any{
			"id": "trimmed", "color": "#38D9C5", "width_px": 5.0,
			"trim_start": 0.40, "trim_end": 0.60, "stops": stops,
		},
	}
	result, err := compileMapTestPlan(t, plan)
	if err != nil {
		t.Fatalf("compile ModernMap1 with a trimmed route: %v", err)
	}
	var route *Layer
	for index := range result.Plan.Layers {
		if result.Plan.Layers[index].ID == "shape-map:modernmap1:route:trimmed" {
			route = &result.Plan.Layers[index]
		}
	}
	if route == nil || route.Shape == nil {
		t.Fatalf("trimmed ModernMap1 route layer missing: %+v", result.Plan.Layers)
	}
	samples, err := mapRouteGeoSamples([]SemanticMapPoint{
		{Latitude: 41.9028, Longitude: 12.4964},
		{Latitude: 43.7696, Longitude: 11.2558},
		{Latitude: 44.4949, Longitude: 11.3426},
		{Latitude: 45.4642, Longitude: 9.19},
		{Latitude: 45.0703, Longitude: 7.6869},
	})
	if err != nil {
		t.Fatalf("route samples: %v", err)
	}
	if len(route.Shape.Path) != len(samples) {
		t.Fatalf("route path has %d commands, want the full %d-sample polyline (no pre-clip)", len(route.Shape.Path), len(samples))
	}
	if len(route.Shape.Operators) != 1 {
		t.Fatalf("route operators = %+v, want one native trim operator", route.Shape.Operators)
	}
	op := route.Shape.Operators[0]
	if op.Kind != "trim" || op.Params.Start != 0.40 || op.Params.End != 0.60 || op.Params.Animation != nil {
		t.Fatalf("route trim operator = %+v, want static [0.40,0.60]", op)
	}
}

// TestModernMap1RouteTrimRendersOnVulkan is the pixel gate for the retired
// pre-clip workaround (TICKET-WORKAROUNDS #1). It renders one frame through the
// real native path into the pre-encode raw RGBA surface (the PNG lane is not a
// pixel-truth surface) and proves the static trim range is applied: green route
// pixels exist in the middle of the polyline and are absent at both endpoints.
//
// Opt-in: CHRONON_BIN must name chronon3d_cli and
// RENDERINGGEN_MODERNMAP_TRIM_RENDER=1. Set
// RENDERINGGEN_TRIM_EVIDENCE_DIR to preserve the plan, raw frame, PNG and log.
func TestModernMap1RouteTrimRendersOnVulkan(t *testing.T) {
	bin := os.Getenv("CHRONON_BIN")
	if bin == "" {
		t.Skip("set CHRONON_BIN to a chronon3d_cli binary to run the native trim pixel gate")
	}
	if os.Getenv("RENDERINGGEN_MODERNMAP_TRIM_RENDER") != "1" {
		t.Skip("set RENDERINGGEN_MODERNMAP_TRIM_RENDER=1 to run the native trim pixel gate")
	}

	plan := modernMapPlan("Italy", nil)
	item := plan["items"].([]any)[0].(map[string]any)
	stops := []any{
		map[string]any{"latitude": 41.9028, "longitude": 12.4964},
		map[string]any{"latitude": 43.7696, "longitude": 11.2558},
		map[string]any{"latitude": 44.4949, "longitude": 11.3426},
		map[string]any{"latitude": 45.4642, "longitude": 9.19},
		map[string]any{"latitude": 45.0703, "longitude": 7.6869},
	}
	item["map"].(map[string]any)["routes"] = []any{map[string]any{
		"id": "trimmed", "color": "#00FF00", "width_px": 8.0,
		"trim_start": 0.40, "trim_end": 0.60, "stops": stops,
	}}
	result, err := compileMapTestPlan(t, plan)
	if err != nil {
		t.Fatalf("compile ModernMap1 trimmed route: %v", err)
	}
	var route *Layer
	for index := range result.Plan.Layers {
		if result.Plan.Layers[index].ID == "shape-map:modernmap1:route:trimmed" {
			route = &result.Plan.Layers[index]
		}
	}
	if route == nil || route.Shape == nil || len(route.Shape.Path) < 5 {
		t.Fatalf("trimmed route layer missing or too short: %+v", route)
	}
	// The production plan places the route at -size/2 in the centered map
	// plane, while a path primitive uses position as a direct translation. The
	// gate recenters only this layer so the authored path stays inside the raw
	// canvas; geometry and trim parameters are untouched.
	route.Position = []float64{0, 0}

	root := t.TempDir()
	writeValidationAssets(t, root, result.Plan.Layers)
	planBytes, err := result.Plan.MarshalIndent("", "  ")
	if err != nil {
		t.Fatalf("marshal render plan: %v", err)
	}
	planPath := filepath.Join(root, "modernmap-trim.plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
		t.Fatalf("write render plan: %v", err)
	}
	evidenceDir := os.Getenv("RENDERINGGEN_TRIM_EVIDENCE_DIR")
	saveEvidence := func(name string, data []byte) {
		t.Helper()
		if evidenceDir == "" {
			return
		}
		if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
			t.Fatalf("create evidence dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, name), data, 0o644); err != nil {
			t.Fatalf("write evidence %s: %v", name, err)
		}
	}
	saveEvidence("modernmap-trim.plan.json", planBytes)

	rawPath := filepath.Join(root, "modernmap-trim.raw")
	cmd := exec.Command(bin, "render", "--plan", planPath, "--assets-root", root,
		"--start-frame", "0", "--end-frame", "0", "--backend", "vulkan",
		"--video-sink", "raw", "--pipe-pixfmt", "rgba", "--encoder-backend", "pipe",
		"-o", rawPath)
	renderOut, err := cmd.CombinedOutput()
	saveEvidence("render.log", renderOut)
	if err != nil {
		t.Fatalf("chronon raw render failed: %v\n%s", err, renderOut)
	}

	const canvasWidth, canvasHeight = 1920, 1080
	const frameBytes = canvasWidth * canvasHeight * 4
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("read raw frame: %v", err)
	}
	if len(raw) != frameBytes {
		t.Fatalf("raw frame is %d bytes, want exactly %d (one 1920x1080 RGBA frame)\n%s", len(raw), frameBytes, renderOut)
	}
	saveEvidence("modernmap-trim.raw", raw)
	frame := image.NewRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))
	copy(frame.Pix, raw)
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, frame); err != nil {
		t.Fatalf("encode evidence PNG: %v", err)
	}
	saveEvidence("modernmap-trim.png", pngBytes.Bytes())

	canvasPoint := func(point []float64) (float64, float64) {
		return point[0] + route.Position[0], point[1] + route.Position[1]
	}
	polylinePointAt := func(fraction float64) (float64, float64) {
		cumulative := make([]float64, len(route.Shape.Path))
		for index := 1; index < len(route.Shape.Path); index++ {
			dx := route.Shape.Path[index].Point[0] - route.Shape.Path[index-1].Point[0]
			dy := route.Shape.Path[index].Point[1] - route.Shape.Path[index-1].Point[1]
			cumulative[index] = cumulative[index-1] + math.Hypot(dx, dy)
		}
		target := cumulative[len(cumulative)-1] * fraction
		for index := 1; index < len(cumulative); index++ {
			if cumulative[index] < target {
				continue
			}
			segment := cumulative[index] - cumulative[index-1]
			t := 0.0
			if segment > 0 {
				t = (target - cumulative[index-1]) / segment
			}
			from := route.Shape.Path[index-1].Point
			to := route.Shape.Path[index].Point
			return canvasPoint([]float64{
				from[0] + t*(to[0]-from[0]),
				from[1] + t*(to[1]-from[1]),
			})
		}
		return canvasPoint(route.Shape.Path[len(route.Shape.Path)-1].Point)
	}
	greenPixels := func(centerX, centerY float64, radius int) int {
		count := 0
		for y := int(centerY) - radius; y <= int(centerY)+radius; y++ {
			for x := int(centerX) - radius; x <= int(centerX)+radius; x++ {
				if x < 0 || y < 0 || x >= canvasWidth || y >= canvasHeight {
					continue
				}
				offset := (y*canvasWidth + x) * 4
				if raw[offset+1] > 180 && raw[offset] < 100 && raw[offset+2] < 100 {
					count++
				}
			}
		}
		return count
	}
	firstX, firstY := canvasPoint(route.Shape.Path[0].Point)
	lastX, lastY := canvasPoint(route.Shape.Path[len(route.Shape.Path)-1].Point)
	midX, midY := polylinePointAt(0.5)
	firstGreen := greenPixels(firstX, firstY, 24)
	lastGreen := greenPixels(lastX, lastY, 24)
	midGreen := greenPixels(midX, midY, 24)
	t.Logf("raw trim gate: first=(%.1f,%.1f,%d green) mid=(%.1f,%.1f,%d green) last=(%.1f,%.1f,%d green)",
		firstX, firstY, firstGreen, midX, midY, midGreen, lastX, lastY, lastGreen)
	if firstGreen != 0 {
		t.Fatalf("trimmed route still paints its first endpoint: %d green pixels", firstGreen)
	}
	if lastGreen != 0 {
		t.Fatalf("trimmed route still paints its last endpoint: %d green pixels", lastGreen)
	}
	if midGreen == 0 {
		t.Fatalf("trimmed route does not paint its middle: 0 green pixels")
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
