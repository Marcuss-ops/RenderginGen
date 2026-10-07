package overlay

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestMapRouteCompilesSphericalNativeTrimPath(t *testing.T) {
	route := SemanticMapRoute{ID: "atlantic", Stops: []SemanticMapPoint{
		{Latitude: 40.7, Longitude: -74.0}, {Latitude: 51.5, Longitude: -0.1},
	}, Color: "#EE8844", WidthPX: 4}
	layers, err := compileMapRoutes("map", []SemanticMapRoute{route}, SemanticMapPoint{Latitude: 0, Longitude: 0}, 3, 1280, 720, 12, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 || !layers[0].Enable3D || layers[0].Position[0] != 0 || layers[0].Position[1] != 0 {
		t.Fatalf("route is not aligned to the native map camera plane: %+v", layers)
	}
	shape := layers[0].Shape
	if shape == nil || shape.Type != "path" || len(shape.Path) < 2 || len(shape.Path) > maxMapRouteSamples || shape.Path[0].Type != "move_to" {
		t.Fatalf("route path did not lower to a bounded open path: %+v", shape)
	}
	if math.Abs(shape.Path[1].Point[1]-shape.Path[0].Point[1]) < 1 {
		t.Fatal("transatlantic Great Circle should curve north in Web Mercator, not be a straight chord")
	}
	op := shape.Operators[0]
	if op.Kind != "trim" || op.Params.Start != 0 || op.Params.End != 0 || op.Params.Animation == nil {
		t.Fatalf("trim should start empty and reveal its requested range: %+v", op)
	}
	keys := op.Params.Animation.Keyframes
	if len(keys) != 2 || keys[0].Frame != 0 || keys[1].Frame != 60 || !sameFloatVector(keys[0].Value, []float64{0, 0, 0}) || !sameFloatVector(keys[1].Value, []float64{0, 1, 0}) {
		t.Fatalf("Chronon trim keyframes must encode [start,end,offset]: %+v", keys)
	}
	wire, err := json.Marshal(layers[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(wire)
	for _, expected := range []string{`"kind":"trim"`, `"start":0`, `"end":0`, `"offset":0`, `"value":[0,0,0]`, `"value":[0,1,0]`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("route wire missing native trim payload %s: %s", expected, text)
		}
	}
}

func TestMapRouteTrimDefaultsAndRejectsInvalidRange(t *testing.T) {
	stops := []SemanticMapPoint{{Latitude: 0, Longitude: -1}, {Latitude: 0, Longitude: 1}}
	layer, err := compileMapRoutes("map", []SemanticMapRoute{{ID: "default", Stops: stops, Color: "#FFFFFF", WidthPX: 1}}, SemanticMapPoint{}, 4, 512, 256, 0, 30)
	if err != nil {
		t.Fatal(err)
	}
	if layer[0].Shape.Operators[0].Params.Start != 0 || layer[0].Shape.Operators[0].Params.End != 0 {
		t.Fatal("animated route's static trim must start empty")
	}
	invalid := 1.0
	if _, err := compileMapRoutes("map", []SemanticMapRoute{{ID: "bad", Stops: stops, Color: "#FFFFFF", WidthPX: 1, TrimStart: &invalid}}, SemanticMapPoint{}, 4, 512, 256, 0, 30); err == nil || !strings.Contains(err.Error(), "trim range") {
		t.Fatalf("trim_start=1 must fail closed, got %v", err)
	}
}

func TestMapRouteRequiresEverySampleInsideEveryLOD(t *testing.T) {
	plan := cameraMapPlan()
	item := plan["items"].([]any)[0].(map[string]any)
	mapSpec := item["map"].(map[string]any)
	mapSpec["routes"] = []any{map[string]any{
		"id": "local", "stops": []any{
			map[string]any{"latitude": 0.0, "longitude": 0.0},
			map[string]any{"latitude": 0.0, "longitude": 0.2},
		}, "color": "#E11D48", "width_px": 4.0,
	}}
	result, err := compileMapTestPlan(t, plan)
	if err != nil {
		t.Fatalf("covered route should compile: %v", err)
	}
	var routeLayer *Layer
	for index := range result.Plan.Layers {
		if result.Plan.Layers[index].ID == "map:map_route:local" {
			routeLayer = &result.Plan.Layers[index]
			break
		}
	}
	if routeLayer == nil || routeLayer.Shape == nil || !routeLayer.Enable3D {
		t.Fatalf("map compiler did not emit camera-grounded route layer: %+v", result.Plan.Layers)
	}
	if routeLayer.StartFrame != result.Plan.Layers[0].StartFrame || routeLayer.DurationFrames != result.Plan.Layers[0].DurationFrames {
		t.Fatal("route did not share the map lifetime")
	}

	plan = cameraMapPlan()
	item = plan["items"].([]any)[0].(map[string]any)
	mapSpec = item["map"].(map[string]any)
	mapSpec["routes"] = []any{map[string]any{
		"id": "leaves-second-lod", "stops": []any{
			map[string]any{"latitude": 0.0, "longitude": 0.0},
			map[string]any{"latitude": 60.0, "longitude": 60.0},
		}, "color": "#E11D48", "width_px": 4.0,
	}}
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "falls outside certified raster LOD") {
		t.Fatalf("route escaping a certified LOD must be rejected, got %v", err)
	}
}

func TestGreatCircleRouteHandlesDateLineAndBoundsWork(t *testing.T) {
	points, err := mapRouteGeoSamples([]SemanticMapPoint{{Latitude: 10, Longitude: 179}, {Latitude: 10, Longitude: -179}})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || math.Abs(math.Abs(points[1].Longitude)-179) > 1e-9 {
		t.Fatalf("short date-line route unexpectedly crossed the world: %+v", points)
	}
	if _, err := mapRouteGeoSamples([]SemanticMapPoint{{Latitude: 0, Longitude: 0}, {Latitude: 0, Longitude: 180}}); err == nil || !strings.Contains(err.Error(), "antipodal") {
		t.Fatalf("ambiguous antipodal path must fail closed, got %v", err)
	}
	stops := make([]SemanticMapPoint, maxMapRouteStops+1)
	if _, err := mapRouteGeoSamples(stops); err == nil {
		t.Fatal("oversized route stop list must fail closed")
	}
}

func TestStaticMapRouteFailsClosedWithoutCameraCoverage(t *testing.T) {
	plan := georeferencedMapPlan(0, 0)
	item := plan["items"].([]any)[0].(map[string]any)
	item["map"].(map[string]any)["routes"] = []any{map[string]any{
		"id": "no-camera", "stops": []any{
			map[string]any{"latitude": 0.0, "longitude": 0.0},
			map[string]any{"latitude": 0.0, "longitude": 0.1},
		}, "color": "#E11D48", "width_px": 2.0,
	}}
	if _, err := compileMapTestPlan(t, plan); err == nil || !strings.Contains(err.Error(), "require a certified camera_move/LOD") {
		t.Fatalf("route without local multi-LOD camera coverage must fail closed, got %v", err)
	}
}

func sameFloatVector(value any, expected []float64) bool {
	switch got := value.(type) {
	case []float64:
		if len(got) != len(expected) {
			return false
		}
		for index := range got {
			if got[index] != expected[index] {
				return false
			}
		}
		return true
	case []any:
		if len(got) != len(expected) {
			return false
		}
		for index := range got {
			number, ok := got[index].(float64)
			if !ok || number != expected[index] {
				return false
			}
		}
		return true
	default:
		return false
	}
}
