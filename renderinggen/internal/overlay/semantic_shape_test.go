package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompileShapeItem_ThreeRecipes(t *testing.T) {
	// 1. Recipe 1: Crime documentary background + grid
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "crime-doc-001",
		"video_id": "vid-001",
		"width": 1920,
		"height": 1080,
		"fps_num": 30,
		"fps_den": 1,
		"duration_ms": 240000,
		"items": [
			{
				"id": "bg",
				"kind": "shape",
				"template_id": "shape_static",
				"start_ms": 0,
				"end_ms": 240000,
				"params": {
					"shape": "rect",
					"width": 1920,
					"height": 1080,
					"fill_gradient": {
						"type": "linear",
						"stops": [["#0a0e1a", 0], ["#141c2c", 0.7], ["#0a0e1a", 1]],
						"start": [0, 0],
						"end": [0, 1]
					},
					"effects": [
						{"type": "fractal_noise", "params": {"intensity": 0.06}},
						{"type": "vignette", "params": {}},
						{"type": "noise", "params": {"intensity": 0.03}}
					]
				}
			},
			{
				"id": "bg-grid",
				"kind": "shape",
				"template_id": "shape_static",
				"start_ms": 0,
				"end_ms": 240000,
				"params": {
					"shape": "grid",
					"width": 1920,
					"height": 1080,
					"grid_spacing": 96,
					"grid_line_width": 1,
					"stroke": {"color": "#1e2a3f", "width": 1},
					"opacity": 0.35
				}
			}
		]
	}`)

	res, err := CompileSemantic(rawPlan)
	if err != nil {
		t.Fatalf("failed to compile Recipe 1: %v", err)
	}
	if len(res.Plan.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(res.Plan.Layers))
	}

	// Verify layer "bg"
	bgLayer := res.Plan.Layers[0]
	if bgLayer.ID != "bg" || bgLayer.Type != "shape" {
		t.Errorf("bg layer ID/Type mismatch: ID=%s, Type=%s", bgLayer.ID, bgLayer.Type)
	}
	if bgLayer.Shape == nil || bgLayer.Shape.Type != "rect" {
		t.Fatalf("expected shape type rect, got %v", bgLayer.Shape)
	}
	if bgLayer.Shape.Gradient == nil || bgLayer.Shape.Gradient.Type != "linear" {
		t.Fatalf("expected linear gradient, got %v", bgLayer.Shape.Gradient)
	}
	if len(bgLayer.Shape.Gradient.ColorStops) != 3 {
		t.Errorf("expected 3 color stops, got %d", len(bgLayer.Shape.Gradient.ColorStops))
	}
	if len(bgLayer.Effects) != 3 {
		t.Fatalf("expected 3 effects, got %d", len(bgLayer.Effects))
	}
	if bgLayer.Effects[0].Type != "fractal_noise" || bgLayer.Effects[0].Params["amplitude"] != 0.06 {
		t.Errorf("fractal_noise effect mismatch: %v", bgLayer.Effects[0])
	}
	if bgLayer.Effects[1].Type != "vignette" {
		t.Errorf("vignette effect mismatch: %v", bgLayer.Effects[1])
	}
	if bgLayer.Effects[2].Type != "noise" || bgLayer.Effects[2].Params["amount"] != 0.03 {
		t.Errorf("noise effect mismatch: %v", bgLayer.Effects[2])
	}

	// Verify layer "bg-grid"
	gridLayer := res.Plan.Layers[1]
	if gridLayer.ID != "bg-grid" || gridLayer.Type != "shape" {
		t.Errorf("bg-grid layer ID/Type mismatch: ID=%s, Type=%s", gridLayer.ID, gridLayer.Type)
	}
	if gridLayer.Shape.Type != "grid" || gridLayer.Shape.GridSpacing != 96 || gridLayer.Shape.GridLineWidth != 1 {
		t.Errorf("grid parameters mismatch: %+v", gridLayer.Shape)
	}
	if gridLayer.Shape.Stroke == nil || gridLayer.Shape.Stroke.Color != "#1e2a3f" || gridLayer.Shape.Stroke.Width != 1 {
		t.Errorf("stroke mismatch: %+v", gridLayer.Shape.Stroke)
	}
	if gridLayer.Opacity == nil || *gridLayer.Opacity != 0.35 {
		t.Errorf("opacity mismatch: %v", gridLayer.Opacity)
	}

	// Verify serialization to JSON wire format
	marshaled, err := res.Plan.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal plan: %v", err)
	}
	wireJSON := string(marshaled)
	if !strings.Contains(wireJSON, `"spacing":96`) && !strings.Contains(wireJSON, `"grid_spacing":96`) {
		t.Errorf("marshaled json missing spacing: %s", wireJSON)
	}
	if !strings.Contains(wireJSON, `"amplitude":0.06`) {
		t.Errorf("marshaled json missing inlined effect param amplitude: %s", wireJSON)
	}

	// 2. Recipe 2: Browser/web with device frame
	rawPlan2 := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "browser-001",
		"video_id": "vid-002",
		"width": 1920,
		"height": 1080,
		"fps_num": 30,
		"fps_den": 1,
		"duration_ms": 10000,
		"items": [
			{
				"id": "browser-frame",
				"kind": "shape",
				"start_ms": 0,
				"end_ms": 10000,
				"params": {
					"shape": "device_frame",
					"width": 1600,
					"height": 900,
					"device_frame_kind": "browser"
				}
			}
		]
	}`)
	res2, err := CompileSemantic(rawPlan2)
	if err != nil {
		t.Fatalf("failed to compile Recipe 2: %v", err)
	}
	frameLayer := res2.Plan.Layers[0]
	if frameLayer.Shape.Type != "device_frame" || frameLayer.Shape.DeviceFrame != "browser" {
		t.Errorf("device frame shape mismatch: %+v", frameLayer.Shape)
	}
	// Check centering: (1920-1600)/2 = 160, (1080-900)/2 = 90
	if len(frameLayer.Position) != 2 || frameLayer.Position[0] != 160 || frameLayer.Position[1] != 90 {
		t.Errorf("expected centered position [160, 90], got %v", frameLayer.Position)
	}

	// 3. Recipe 3: World map
	rawPlan3 := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "world-map-001",
		"video_id": "vid-003",
		"width": 1920,
		"height": 1080,
		"fps_num": 30,
		"fps_den": 1,
		"duration_ms": 10000,
		"items": [
			{
				"id": "crime-map",
				"kind": "shape",
				"start_ms": 0,
				"end_ms": 10000,
				"params": {
					"shape": "world_map",
					"width": 1920,
					"height": 1080,
					"world_map": {
						"projection": "mercator",
						"center": [-46.63, -23.55],
						"zoom": 5,
						"highlight": ["BRA"],
						"route": [[-46.63, -23.55], [-43.2, -22.9]]
					}
				}
			}
		]
	}`)
	res3, err := CompileSemantic(rawPlan3)
	if err != nil {
		t.Fatalf("failed to compile Recipe 3: %v", err)
	}
	mapLayer := res3.Plan.Layers[0]
	if mapLayer.Shape.Type != "world_map" || mapLayer.Shape.WorldMap == nil {
		t.Fatalf("expected world_map payload, got %+v", mapLayer.Shape)
	}
	wm := mapLayer.Shape.WorldMap
	if wm.Projection != "mercator" || wm.Zoom != 5 || len(wm.Highlight) != 1 || wm.Highlight[0] != "BRA" {
		t.Errorf("world_map parameters mismatch: %+v", wm)
	}
	if len(wm.Route) != 2 || wm.Route[0][0] != -46.63 || wm.Route[1][1] != -22.9 {
		t.Errorf("world_map route mismatch: %v", wm.Route)
	}
}

func TestCompileShapeItem_FailClosedBounds(t *testing.T) {
	testCases := []struct {
		name        string
		params      map[string]any
		errContains string
	}{
		{
			name:        "grid_spacing too small",
			params:      map[string]any{"shape": "grid", "grid_spacing": 2.0},
			errContains: "grid_spacing",
		},
		{
			name:        "grid_spacing too large",
			params:      map[string]any{"shape": "grid", "grid_spacing": 600.0},
			errContains: "grid_spacing",
		},
		{
			name:        "grid_line_width too small",
			params:      map[string]any{"shape": "grid", "grid_line_width": 0.1},
			errContains: "grid_line_width",
		},
		{
			name:        "dot_radius too small",
			params:      map[string]any{"shape": "dot_grid", "dot_radius": 0.2},
			errContains: "dot_radius",
		},
		{
			name:        "dot_radius too large",
			params:      map[string]any{"shape": "dot_grid", "dot_radius": 70.0},
			errContains: "dot_radius",
		},
		{
			name:        "star points too small",
			params:      map[string]any{"shape": "star", "points": 2},
			errContains: "points",
		},
		{
			name:        "star points too large",
			params:      map[string]any{"shape": "star", "points": 100},
			errContains: "points",
		},
		{
			name:        "inner_radius_ratio too small",
			params:      map[string]any{"shape": "star", "inner_radius_ratio": 0.02},
			errContains: "inner_radius_ratio",
		},
		{
			name:        "inner_radius_ratio too large",
			params:      map[string]any{"shape": "star", "inner_radius_ratio": 0.99},
			errContains: "inner_radius_ratio",
		},
		{
			name:        "arc_sweep_deg out of bounds",
			params:      map[string]any{"shape": "arc", "arc_sweep_deg": 360.0},
			errContains: "arc_sweep_deg",
		},
		{
			name:        "rounded_rect radius exceeds half dimension",
			params:      map[string]any{"shape": "rounded_rect", "width": 100.0, "height": 100.0, "radius": 60.0},
			errContains: "exceeds half",
		},
		{
			name:        "world_map zoom out of bounds",
			params:      map[string]any{"shape": "world_map", "world_map": map[string]any{"zoom": 10}},
			errContains: "zoom",
		},
		{
			name:        "world_map center lat out of bounds",
			params:      map[string]any{"shape": "world_map", "world_map": map[string]any{"center": []any{0.0, 95.0}}},
			errContains: "center",
		},
		{
			name:        "unsupported shape type",
			params:      map[string]any{"shape": "trapezoid_3d"},
			errContains: "unsupported shape type",
		},
		{
			name:        "unsupported effect type",
			params:      map[string]any{"shape": "rect", "effects": []any{map[string]any{"type": "super_warp"}}},
			errContains: "unsupported effect type",
		},
		{
			name:        "noise amount out of bounds",
			params:      map[string]any{"shape": "rect", "effects": []any{map[string]any{"type": "noise", "params": map[string]any{"amount": 1.5}}}},
			errContains: "noise amount",
		},
		{
			name:        "noise intensity rejects non numeric value",
			params:      map[string]any{"shape": "rect", "effects": []any{map[string]any{"type": "noise", "params": map[string]any{"intensity": "loud"}}}},
			errContains: "intensity must be a number",
		},
		{
			name:        "noise size rejects infinity",
			params:      map[string]any{"shape": "rect", "effects": []any{map[string]any{"type": "noise", "params": map[string]any{"size": "Infinity"}}}},
			errContains: "noise size",
		},
		{
			name:        "fractal noise octaves remain integral",
			params:      map[string]any{"shape": "rect", "effects": []any{map[string]any{"type": "fractal_noise", "params": map[string]any{"octaves": 2.5}}}},
			errContains: "octaves",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			plan := map[string]any{
				"schema_version": "renderinggen.overlay-plan.v1",
				"plan_id":        "test-plan",
				"video_id":       "test-vid",
				"width":          1920,
				"height":         1080,
				"fps_num":        30,
				"fps_den":        1,
				"duration_ms":    5000,
				"items": []any{
					map[string]any{
						"id":       "item-0",
						"kind":     "shape",
						"start_ms": 0,
						"end_ms":   5000,
						"params":   tc.params,
					},
				},
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatalf("failed to marshal test plan: %v", err)
			}
			_, err = CompileSemantic(raw)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errContains)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("expected error containing %q, got %v", tc.errContains, err)
			}
		})
	}
}
