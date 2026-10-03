package overlay

import (
	"encoding/json"
	"strings"
	"testing"
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
	if pin.Type != "text" || pin.Text != "O" || pin.Style == nil || pin.Position[0] != 640 || pin.Position[1] != 360 {
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
	result, err := compileMapTestPlan(t, cameraMapPlan())
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan.Camera == nil || result.Plan.CameraAnimation == nil {
		t.Fatal("fly-to did not lower to a native camera descriptor and animation")
	}
	if len(result.Plan.CameraAnimation.Tracks) != 4 {
		t.Fatalf("camera tracks = %d, want x/y pan, tilt and zoom", len(result.Plan.CameraAnimation.Tracks))
	}
	if got, want := len(result.Plan.Layers), 7; got != want {
		t.Fatalf("camera map layers = %d, want 2 LODs + 2 pins/labels + attribution = %d", got, want)
	}
	coarse, fine := result.Plan.Layers[0], result.Plan.Layers[1]
	if coarse.ID != mapLODLayerID("map", 0) || fine.ID != mapLODLayerID("map", 1) || !coarse.Enable3D || !fine.Enable3D {
		t.Fatalf("offline LOD layers are not ordered 3D map planes: %+v / %+v", coarse, fine)
	}
	if coarse.Animation == nil || fine.Animation == nil || coarse.Animation.Tracks[0].Property != "opacity" || fine.Animation.Tracks[0].Property != "opacity" {
		t.Fatal("LOD pair has no synchronized opacity cross-fade tracks")
	}
	credit := result.Plan.Layers[len(result.Plan.Layers)-1]
	if credit.ID != mapAttributionLayerID("map") || !credit.ScreenSpace || credit.Text != "Map data supplied by operator" {
		t.Fatalf("camera fly-to attribution must remain visible in screen space: %+v", credit)
	}
	for _, layer := range result.Plan.Layers[2:6] {
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
