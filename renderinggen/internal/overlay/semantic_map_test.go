package overlay

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geo"
)

func georeferencedMapPlan(pinLat, pinLon float64) map[string]any {
	return map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "map-plan", "video_id": "map-video",
		"width": 1280, "height": 720, "fps_num": 30, "fps_den": 1,
		"items": []any{map[string]any{
			"id": "map", "kind": "map", "template_id": "MAP",
			"start_ms": 0, "end_ms": 3000,
			"asset_refs": []any{map[string]any{
				"asset_id": "basemap", "sha256": strings.Repeat("a", 64),
				"url": "assets/maps/plate.png", "media_type": "image/png",
			}},
			"map": map[string]any{
				"provider": "local", "source_id": "certified-plate", "source_license": "operator-supplied",
				"center": map[string]any{"latitude": 0.0, "longitude": 0.0},
				"zoom":   8, "width": 1280, "height": 720,
				"attribution": "Map data supplied by operator", "motion_id": "image_fade_reveal",
				"pins": []any{map[string]any{
					"id": "target", "label": "Target", "latitude": pinLat, "longitude": pinLon,
					"color": "#E11D48", "radius_px": 12.0,
				}},
			},
		}},
	}
}

func compileMapTestPlan(t *testing.T, plan map[string]any) (CompileResult, error) {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return CompileSemantic(raw)
}

func TestMapLabelGeometryResolvesStablePlacementsAndDimensions(t *testing.T) {
	window := geo.CenteredOn(0, 0, 8, 1280, 720)
	pins := []SemanticMapPin{
		{ID: "b", Label: "Second", Latitude: 0, Longitude: 0.05, Color: "#E11D48", RadiusPX: 12},
		{ID: "a", Label: "First", Latitude: 0, Longitude: 0, Color: "#E11D48", RadiusPX: 12, LabelPriority: 10},
	}
	placements, err := resolveMapLabelPlacements(pins, window, 1280, 720)
	if err != nil {
		t.Fatalf("resolve map label geometry: %v", err)
	}
	again, err := resolveMapLabelPlacements(pins, window, 1280, 720)
	if err != nil {
		t.Fatalf("repeat map label geometry: %v", err)
	}
	if !reflect.DeepEqual(placements, again) {
		t.Fatalf("map label geometry is not deterministic: %v vs %v", placements, again)
	}
	if len(placements) != len(pins) {
		t.Fatalf("placements = %v, want one per pin", placements)
	}
	width, height := mapPinLabelDimensions(pins[0], 1280, 720)
	if width <= 0 || height <= 0 || width > 1280 || height > 720 {
		t.Fatalf("label geometry size = %vx%v outside canvas", width, height)
	}
}

func TestGeoreferencedMapCompilesGroundedLayers(t *testing.T) {
	result, err := compileMapTestPlan(t, georeferencedMapPlan(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Plan.Layers), 4; got != want {
		t.Fatalf("got %d map layers, want basemap, pin, pin label and attribution", got)
	}
	basemap, pin, label, credit := result.Plan.Layers[0], result.Plan.Layers[1], result.Plan.Layers[2], result.Plan.Layers[3]
	if basemap.Type != "image" || basemap.Size[0] != 1280 || basemap.Size[1] != 720 {
		t.Fatalf("basemap does not preserve the certified raster canvas: %+v", basemap)
	}
	if pin.Type != "shape" || pin.Shape == nil || pin.Shape.Type != "ellipse" || pin.Position[0] != 0 || pin.Position[1] != 0 {
		t.Fatalf("pin at the georeference center did not land at the canvas center: %+v", pin)
	}
	if label.Type != "text" || label.Text != "Target" || credit.Type != "text" || credit.Text != "Map data supplied by operator" {
		t.Fatalf("map label/attribution layers are incomplete: label=%+v credit=%+v", label, credit)
	}
	for _, layer := range result.Plan.Layers {
		if layer.StartFrame != basemap.StartFrame || layer.DurationFrames != basemap.DurationFrames {
			t.Fatalf("map layer %q does not share the basemap lifetime", layer.ID)
		}
	}
}

func TestGeoreferencedMapRejectsPinOutsideCertifiedWindow(t *testing.T) {
	_, err := compileMapTestPlan(t, georeferencedMapPlan(48.8566, 2.3522))
	if err == nil || !strings.Contains(err.Error(), "falls outside the basemap window") {
		t.Fatalf("out-of-window pin should fail closed, got %v", err)
	}
}

func TestMapRequiresVisibleAttributionAndLocalRaster(t *testing.T) {
	plan := georeferencedMapPlan(0, 0)
	item := plan["items"].([]any)[0].(map[string]any)
	mapSpec := item["map"].(map[string]any)
	mapSpec["attribution"] = " \n "
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "attribution") {
		t.Fatalf("blank attribution should fail closed, got %v", err)
	}

	plan = georeferencedMapPlan(0, 0)
	item = plan["items"].([]any)[0].(map[string]any)
	refs := item["asset_refs"].([]any)
	refs[0].(map[string]any)["media_type"] = "application/pdf"
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "image/png") {
		t.Fatalf("non-raster basemap should fail closed, got %v", err)
	}
}

func TestMapMotionMustResolveAndKeepBasemapCentered(t *testing.T) {
	plan := georeferencedMapPlan(0, 0)
	item := plan["items"].([]any)[0].(map[string]any)
	mapSpec := item["map"].(map[string]any)
	mapSpec["motion_id"] = "unknown_map_motion"
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "registered centered image motion") {
		t.Fatalf("unknown map motion should fail through the canonical registry, got %v", err)
	}

	plan = georeferencedMapPlan(0, 0)
	item = plan["items"].([]any)[0].(map[string]any)
	mapSpec = item["map"].(map[string]any)
	mapSpec["motion_id"] = "web_card_slide_up"
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "registered centered image motion") {
		t.Fatalf("a translating image motion must not detach pins from the raster, got %v", err)
	}
}

func TestMapCanCompileWithoutOptionalLayerMotion(t *testing.T) {
	plan := cameraMapPlan()
	item := plan["items"].([]any)[0].(map[string]any)
	mapSpec := item["map"].(map[string]any)
	delete(mapSpec, "motion_id")
	result, err := compileMapTestPlan(t, plan)
	if err != nil {
		t.Fatalf("camera_move map should not require a separate layer motion: %v", err)
	}
	if result.Plan.CameraAnimation == nil || result.Plan.Layers[0].Animation != nil {
		t.Fatal("camera_move should own fly-to while the map plate stays free of a layer animation")
	}

	plan = georeferencedMapPlan(0, 0)
	item = plan["items"].([]any)[0].(map[string]any)
	mapSpec = item["map"].(map[string]any)
	delete(mapSpec, "motion_id")
	result, err = compileMapTestPlan(t, plan)
	if err != nil {
		t.Fatalf("static map without an optional motion should compile: %v", err)
	}
	if result.Plan.Layers[0].Animation != nil {
		t.Fatal("static map without motion_id unexpectedly received an animation")
	}
}

func TestMapCompositionIDValidatesPlanMapCount(t *testing.T) {
	plan := georeferencedMapPlan(0, 0)
	plan["map_composition_id"] = "two_maps"
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), `requires 2 map items, got 1`) {
		t.Fatalf("one map cannot satisfy two_maps, got %v", err)
	}

	first := plan["items"].([]any)[0].(map[string]any)
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var second map[string]any
	if err := json.Unmarshal(encoded, &second); err != nil {
		t.Fatal(err)
	}
	second["id"] = "map-two"
	secondAsset := second["asset_refs"].([]any)[0].(map[string]any)
	secondAsset["asset_id"] = "basemap-two"
	secondAsset["url"] = "assets/maps/plate-two.png"
	plan["items"] = append(plan["items"].([]any), second)
	if _, err := compileMapTestPlan(t, plan); err != nil {
		t.Fatalf("two map items should satisfy two_maps: %v", err)
	}

	delete(plan, "map_composition_id")
	if _, err := compileMapTestPlan(t, plan); err != nil {
		t.Fatalf("legacy plans without map_composition_id should remain valid: %v", err)
	}

	plan["map_composition_id"] = "unsupported_map_group"
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "unsupported map_composition_id") {
		t.Fatalf("unknown map composition should fail clearly, got %v", err)
	}
}

func TestFeatureFlyToLowersByNaturalEarthNameUsingOfflineLODs(t *testing.T) {
	plan := cameraMapPlan()
	item := plan["items"].([]any)[0].(map[string]any)
	mapSpec := item["map"].(map[string]any)
	mapSpec["center"] = map[string]any{"latitude": 51.0, "longitude": -5.0}
	mapSpec["width"], mapSpec["height"] = 4096, 4096
	mapSpec["lods"].([]any)[0].(map[string]any)["center"] = mapSpec["center"]
	mapSpec["zoom"] = 4
	lods := mapSpec["lods"].([]any)
	lods[0].(map[string]any)["zoom"] = 4
	lods[0].(map[string]any)["width"], lods[0].(map[string]any)["height"] = 4096, 4096
	// The offline LOD range must be able to satisfy the feature fly-to: the
	// resolver requires finalLODZoom > startZoom and finalLODZoom at or below
	// the zoom at which the feature still fits, so a second level is required.
	lods[1].(map[string]any)["zoom"] = 5
	lods[1].(map[string]any)["center"] = map[string]any{"latitude": 51.0, "longitude": 0.0}
	lods[1].(map[string]any)["width"], lods[1].(map[string]any)["height"] = 4096, 4096
	mapSpec["camera_move"] = map[string]any{"from": map[string]any{"latitude": 51.0, "longitude": -5.0}, "to": map[string]any{"latitude": 51.0, "longitude": 0.0}, "start_zoom": 4.0, "end_zoom": 4.0, "start_tilt_deg": 0.0, "end_tilt_deg": 0.0, "bearing_deg": 0.0}
	delete(mapSpec, "motion_id")
	mapSpec["fly_to_feature"] = map[string]any{"feature": "United Kingdom", "padding": 0.12}
	delete(mapSpec, "camera_move")
	result, err := compileMapTestPlan(t, plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan.Camera == nil || result.Plan.CameraAnimation == nil || len(result.Plan.CameraAnimation.Tracks) != 4 {
		t.Fatalf("feature selector did not lower to the native camera animation: %+v", result.Plan.CameraAnimation)
	}
	if len(result.Plan.Layers) != 6 || result.Plan.Layers[0].Asset != "assets/maps/coarse.png" {
		t.Fatalf("feature fly-to should retain the certified first LOD and map layers: %+v", result.Plan.Layers)
	}
	if result.Plan.Schema != RenderPlanSchemaV3 {
		t.Fatalf("feature fly-to wire schema = %q, want V3", result.Plan.Schema)
	}
}

func TestFeatureFlyToRejectsUnknownNameAndInsufficientOfflineZoom(t *testing.T) {
	plan := cameraMapPlan()
	item := plan["items"].([]any)[0].(map[string]any)
	mapSpec := item["map"].(map[string]any)
	delete(mapSpec, "motion_id")
	mapSpec["fly_to_feature"] = map[string]any{"feature": "Unknown place", "padding": 0.12}
	delete(mapSpec, "camera_move")
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "feature not found") {
		t.Fatalf("unknown feature must fail closed, got %v", err)
	}

	plan = cameraMapPlan()
	item = plan["items"].([]any)[0].(map[string]any)
	mapSpec = item["map"].(map[string]any)
	delete(mapSpec, "motion_id")
	mapSpec["fly_to_feature"] = map[string]any{"feature": "United Kingdom", "padding": 0.12}
	delete(mapSpec, "camera_move")
	lods := mapSpec["lods"].([]any)
	lods[1].(map[string]any)["zoom"] = 18
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "would crop its bounds") {
		t.Fatalf("feature fit exceeding certified LOD resolution must fail, got %v", err)
	}
}

func TestFeatureFlyToRejectsCompetingCameraDeclarations(t *testing.T) {
	plan := cameraMapPlan()
	item := plan["items"].([]any)[0].(map[string]any)
	mapSpec := item["map"].(map[string]any)
	delete(mapSpec, "motion_id")
	mapSpec["fly_to_feature"] = map[string]any{"feature": "United Kingdom", "padding": 0.12}
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "must not combine") {
		t.Fatalf("two competing camera declarations should be rejected, got %v", err)
	}
}

func TestMapRoutesCompileWithDefaultAndExplicitTrimRanges(t *testing.T) {
	for _, tc := range []struct {
		name      string
		trimStart any
		trimEnd   any
		wantStart float64
		wantEnd   float64
	}{
		{name: "default full route", wantStart: 0, wantEnd: 1},
		{name: "explicit partial route", trimStart: 0.2, trimEnd: 0.8, wantStart: 0.2, wantEnd: 0.8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := cameraMapPlan()
			item := plan["items"].([]any)[0].(map[string]any)
			mapSpec := item["map"].(map[string]any)
			mapSpec["routes"] = []any{map[string]any{
				"id": "journey", "stops": []any{
					map[string]any{"latitude": 0.0, "longitude": 0.0},
					map[string]any{"latitude": 0.0, "longitude": 0.2},
				},
				"color": "#22AAFF", "width_px": 6.0,
			}}
			route := mapSpec["routes"].([]any)[0].(map[string]any)
			if tc.trimStart != nil {
				route["trim_start"] = tc.trimStart
				route["trim_end"] = tc.trimEnd
			}

			result, err := compileMapTestPlan(t, plan)
			if err != nil {
				t.Fatalf("compile route: %v", err)
			}
			var got Layer
			found := false
			for _, layer := range result.Plan.Layers {
				if layer.ID == "map:map_route:journey" {
					got = layer
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("route layer map:map_route:journey not found: %+v", result.Plan.Layers)
			}
			if got.ID != "map:map_route:journey" || got.Shape == nil || got.Shape.Type != "path" || len(got.Shape.Path) < 2 {
				t.Fatalf("route was not lowered as a path layer: %+v", got)
			}
			if len(got.Shape.Operators) != 1 || got.Shape.Operators[0].Kind != "trim" {
				t.Fatalf("route should use one trim operator: %+v", got.Shape.Operators)
			}
			trim := got.Shape.Operators[0].Params
			if trim.Start != tc.wantStart || trim.End != tc.wantStart || trim.Animation == nil || len(trim.Animation.Keyframes) != 2 {
				t.Fatalf("initial trim state = %+v, want start/end %v and two animated keyframes", trim, tc.wantStart)
			}
			last, ok := trim.Animation.Keyframes[1].Value.([]float64)
			if !ok || len(last) != 3 || last[0] != tc.wantStart || last[1] != tc.wantEnd {
				t.Fatalf("final trim keyframe = %v, want [%v %v 0]", trim.Animation.Keyframes[1].Value, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

func TestMapRoutesRejectInvalidTrimRange(t *testing.T) {
	plan := cameraMapPlan()
	item := plan["items"].([]any)[0].(map[string]any)
	mapSpec := item["map"].(map[string]any)
	mapSpec["routes"] = []any{map[string]any{
		"id": "bad-trim", "stops": []any{
			map[string]any{"latitude": 0.0, "longitude": 0.0},
			map[string]any{"latitude": 0.0, "longitude": 0.2},
		},
		"color": "#22AAFF", "width_px": 6.0, "trim_start": 0.8, "trim_end": 0.2,
	}}
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "trim range") {
		t.Fatalf("reversed route trim must fail closed, got %v", err)
	}
}

func cameraMapPlan() map[string]any {
	plan := georeferencedMapPlan(0, 0)
	item := plan["items"].([]any)[0].(map[string]any)
	item["asset_refs"] = []any{
		map[string]any{"asset_id": "coarse", "sha256": strings.Repeat("b", 64), "url": "assets/maps/coarse.png", "media_type": "image/png"},
		map[string]any{"asset_id": "fine", "sha256": strings.Repeat("c", 64), "url": "assets/maps/fine.png", "media_type": "image/png"},
	}
	mapSpec := item["map"].(map[string]any)
	mapSpec["width"], mapSpec["height"], mapSpec["zoom"] = 2048, 2048, 4
	mapSpec["center"] = map[string]any{"latitude": 0.0, "longitude": 0.0}
	mapSpec["pins"] = []any{
		map[string]any{"id": "first", "label": "First", "latitude": 0.0, "longitude": 0.0, "color": "#E11D48", "radius_px": 12.0},
		map[string]any{"id": "last", "label": "Last", "latitude": 0.0, "longitude": 0.2, "color": "#E11D48", "radius_px": 12.0},
	}
	mapSpec["lods"] = []any{
		map[string]any{
			"asset_id": "coarse", "source_id": "plate-coarse", "source_license": "operator-supplied", "attribution": "Map data supplied by operator",
			"center": map[string]any{"latitude": 0.0, "longitude": 0.0}, "zoom": 4, "width": 2048, "height": 2048,
		},
		map[string]any{
			"asset_id": "fine", "source_id": "plate-fine", "source_license": "operator-supplied", "attribution": "Map data supplied by operator",
			"center": map[string]any{"latitude": 0.0, "longitude": 0.0}, "zoom": 6, "width": 4096, "height": 4096,
		},
	}
	mapSpec["camera_move"] = map[string]any{
		"from":       map[string]any{"latitude": 0.0, "longitude": 0.0},
		"to":         map[string]any{"latitude": 0.0, "longitude": 0.2},
		"start_zoom": 4.0, "end_zoom": 6.0, "start_tilt_deg": 0.0, "end_tilt_deg": 0.0, "bearing_deg": 0.0,
	}
	return plan
}

func TestGeoreferencedFlyToCompilesNativeCameraAndOfflineLODs(t *testing.T) {
	plan := cameraMapPlan()
	item := plan["items"].([]any)[0].(map[string]any)
	item["map"].(map[string]any)["motion_id"] = "map_image_dark_map_italy_radar_lock"
	result, err := compileMapTestPlan(t, plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan.Camera == nil || result.Plan.CameraAnimation == nil {
		t.Fatal("fly-to did not lower to a native camera descriptor and animation")
	}
	if len(result.Plan.CameraAnimation.Tracks) != 4 {
		t.Fatalf("camera tracks = %d, want x/y pan, tilt and zoom", len(result.Plan.CameraAnimation.Tracks))
	}
	if got, want := len(result.Plan.Layers), 6; got != want {
		t.Fatalf("camera map layers = %d, want one basemap + 2 pins/labels + attribution = %d", got, want)
	}
	coarse := result.Plan.Layers[0]
	if coarse.ID != mapBasemapLayerID("map") || !coarse.Enable3D {
		t.Fatalf("base map plate is not a 3D plane: %+v", coarse)
	}
	if coarse.Animation == nil {
		t.Fatal("camera-moved map dropped its declared map_image_v1 treatment")
	}
	properties := map[string]bool{}
	for _, track := range coarse.Animation.Tracks {
		properties[track.Property] = true
	}
	if !properties["scale"] || !properties["opacity"] {
		t.Fatalf("camera-moved map tracks = %v, want map recipe scale and opacity", properties)
	}
	credit := result.Plan.Layers[len(result.Plan.Layers)-1]
	if credit.ID != mapAttributionLayerID("map") || !credit.ScreenSpace || credit.Text != "Map data supplied by operator" {
		t.Fatalf("camera fly-to attribution must remain visible in screen space: %+v", credit)
	}
	for _, layer := range result.Plan.Layers[1:5] {
		if !layer.Enable3D {
			t.Fatalf("grounded marker/label %q does not follow the native camera", layer.ID)
		}
	}
	wire, err := result.Plan.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"schema":"chronon.render-plan.v3"`) || !strings.Contains(string(wire), `"screen_space":true`) {
		t.Fatalf("camera map must use V3 and serialize the screen-space credit: %s", wire)
	}
}

func TestSceneCameraRejectsMultipleControllers(t *testing.T) {
	entity := map[string]any{
		"id": "camera-portrait", "entity_id": "person:ada", "kind": "entity_card",
		"template_id": "PERSON", "preset_id": PhraseDefaultPresetID, "text": "Ada Lovelace",
		"image_preset_id": "image_scale_in", "entity_caption": "Ada Lovelace",
		"entity_style_id": "camera", "start_ms": 0, "end_ms": 3000, "duration_ms": 3000,
		"asset_refs": []any{map[string]any{
			"asset_id": "portrait", "sha256": strings.Repeat("d", 64),
			"url": "assets/semantic/portrait.png", "media_type": "image/png",
		}},
	}
	mapCamera := cameraMapPlan()["items"].([]any)[0].(map[string]any)
	secondMapCamera := make(map[string]any, len(mapCamera))
	for key, value := range mapCamera {
		secondMapCamera[key] = value
	}
	secondMapCamera["id"] = "camera-map-2"
	for _, tc := range []struct {
		name  string
		items []any
		want  string
	}{
		{name: "map camera then entity camera", items: []any{cameraMapPlan()["items"].([]any)[0], entity}, want: "camera motion conflicts with map item"},
		{name: "entity camera then map camera", items: []any{entity, cameraMapPlan()["items"].([]any)[0]}, want: "scene camera move conflicts with entity item"},
		{name: "two map camera controllers", items: []any{mapCamera, secondMapCamera}, want: "only one scene camera controller"},
		{name: "two entity camera controllers", items: []any{entity, func() map[string]any {
			duplicate := make(map[string]any, len(entity))
			for key, value := range entity {
				duplicate[key] = value
			}
			duplicate["id"] = "camera-portrait-2"
			duplicate["entity_id"] = "person:grace"
			return duplicate
		}()}, want: "only one scene camera controller"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := map[string]any{
				"schema_version": "renderinggen.overlay-plan.v1", "plan_id": "camera-conflict",
				"video_id": "camera-conflict", "width": 1280, "height": 720,
				"fps_num": 24, "fps_den": 1, "items": tc.items,
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := CompileSemantic(raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("camera conflict error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestMapCameraAllowsEntityStyleWithoutCameraMotion(t *testing.T) {
	entity := map[string]any{
		"id": "badge-portrait", "entity_id": "person:ada", "kind": "entity_card",
		"template_id": "PERSON", "preset_id": PhraseDefaultPresetID, "text": "Ada Lovelace",
		"image_preset_id": "image_scale_in", "entity_caption": "Ada Lovelace",
		"entity_style_id": "badge", "start_ms": 0, "end_ms": 3000, "duration_ms": 3000,
		"asset_refs": []any{map[string]any{
			"asset_id": "portrait", "sha256": strings.Repeat("d", 64),
			"url": "assets/semantic/portrait.png", "media_type": "image/png",
		}},
	}
	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1", "plan_id": "camera-compatible",
		"video_id": "camera-compatible", "width": 1280, "height": 720,
		"fps_num": 24, "fps_den": 1,
		"items": []any{cameraMapPlan()["items"].([]any)[0], entity},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("map camera with non-camera entity style should compile: %v", err)
	}
	if result.Plan.Camera == nil {
		t.Fatal("map camera controller was not compiled")
	}
}

func TestGeoreferencedFlyToRejectsUncoveredOrUnlicensedLOD(t *testing.T) {
	plan := cameraMapPlan()
	item := plan["items"].([]any)[0].(map[string]any)
	mapSpec := item["map"].(map[string]any)
	lods := mapSpec["lods"].([]any)
	lods[1].(map[string]any)["center"] = map[string]any{"latitude": 80.0, "longitude": 80.0}
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "does not cover active camera viewport") {
		t.Fatalf("LOD outside the camera route should be rejected, got %v", err)
	}

	plan = cameraMapPlan()
	item = plan["items"].([]any)[0].(map[string]any)
	mapSpec = item["map"].(map[string]any)
	lods = mapSpec["lods"].([]any)
	lods[1].(map[string]any)["source_license"] = "unverified-license"
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "provenance and attribution") {
		t.Fatalf("LOD with mismatched provenance must be rejected, got %v", err)
	}

	plan = cameraMapPlan()
	item = plan["items"].([]any)[0].(map[string]any)
	refs := item["asset_refs"].([]any)
	refs[1].(map[string]any)["url"] = "https://tiles.example/6/1/1.png"
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "local content-addressed PNG") {
		t.Fatalf("network-backed LOD must be rejected, got %v", err)
	}
}
