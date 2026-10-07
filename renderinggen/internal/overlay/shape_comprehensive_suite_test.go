//go:build certification

package overlay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// 1. Contract Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestShapeKindAccepted(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"width": 1920, "height": 1080,
	})
	if err != nil {
		t.Fatalf("expected kind:'shape' to be accepted, got error: %v", err)
	}
	if layer.Type != "shape" {
		t.Errorf("expected layer.Type == 'shape', got %q", layer.Type)
	}
}

func TestUnknownShapeKindRejected(t *testing.T) {
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "test-unknown-kind",
		"video_id": "test-vid",
		"width": 1920, "height": 1080,
		"fps_num": 30, "fps_den": 1, "duration_ms": 1000,
		"items": [
			{
				"id": "item1",
				"kind": "unknown_shape_kind_xyz",
				"start_ms": 0, "end_ms": 1000,
				"params": {"shape": "rect"}
			}
		]
	}`)
	_, err := CompileSemantic(rawPlan)
	if err == nil {
		t.Fatalf("expected error for unknown item kind, got nil")
	}
}

func TestShapeRequiredFields(t *testing.T) {
	// Zero width rejected
	_, _, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"width": 0, "height": 1080,
	})
	if err == nil {
		t.Errorf("expected error for width <= 0, got nil")
	}

	// Negative height rejected
	_, _, err = compileSingleShape(t, map[string]any{
		"shape": "rect",
		"width": 1920, "height": -100,
	})
	if err == nil {
		t.Errorf("expected error for height < 0, got nil")
	}

	// Unsupported shape type rejected
	_, _, err = compileSingleShape(t, map[string]any{
		"shape": "hypercube_nonexistent",
		"width": 1920, "height": 1080,
	})
	if err == nil {
		t.Errorf("expected error for unsupported shape type, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Shape Primitives Compilation
// ─────────────────────────────────────────────────────────────────────────────

func TestRectCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect", "width": 800, "height": 600,
	})
	if err != nil {
		t.Fatalf("TestRectCompiles failed: %v", err)
	}
	if layer.Shape == nil || layer.Shape.Type != "rect" {
		t.Errorf("expected rect shape, got %+v", layer.Shape)
	}
	if layer.Size[0] != 800 || layer.Size[1] != 600 {
		t.Errorf("expected size [800, 600], got %v", layer.Size)
	}
}

func TestRoundedRectCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rounded_rect", "width": 600, "height": 400, "radius": 32.0,
	})
	if err != nil {
		t.Fatalf("TestRoundedRectCompiles failed: %v", err)
	}
	if layer.Shape.Type != "rounded_rect" || layer.Shape.Radius != 32.0 {
		t.Errorf("expected rounded_rect with radius 32, got %+v", layer.Shape)
	}
}

func TestPerCornerRadiusCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rounded_rect", "width": 600, "height": 400,
		"corner_radius": []any{10.0, 20.0, 30.0, 40.0},
	})
	if err != nil {
		t.Fatalf("TestPerCornerRadiusCompiles failed: %v", err)
	}
	expected := []float64{10, 20, 30, 40}
	if len(layer.Shape.CornerRadius) != 4 {
		t.Fatalf("expected 4 corner radii, got %v", layer.Shape.CornerRadius)
	}
	for i, v := range layer.Shape.CornerRadius {
		if v != expected[i] {
			t.Errorf("corner_radius[%d] = %v, want %v", i, v, expected[i])
		}
	}
}

func TestEllipseCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "ellipse", "width": 500, "height": 300,
	})
	if err != nil {
		t.Fatalf("TestEllipseCompiles failed: %v", err)
	}
	if layer.Shape.Type != "ellipse" {
		t.Errorf("expected ellipse, got %s", layer.Shape.Type)
	}
}

func TestStarCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "star", "points": 5, "inner_radius_ratio": 0.45,
	})
	if err != nil {
		t.Fatalf("TestStarCompiles failed: %v", err)
	}
	if layer.Shape.Type != "star" || layer.Shape.Points != 5 || layer.Shape.InnerRadius != 0.45 {
		t.Errorf("star params mismatch: %+v", layer.Shape)
	}
}

func TestPolygonCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "polygon", "points": 8, "rotation_degrees": 22.5,
	})
	if err != nil {
		t.Fatalf("TestPolygonCompiles failed: %v", err)
	}
	if layer.Shape.Type != "polygon" || layer.Shape.Points != 8 || layer.Shape.RotationDeg != 22.5 {
		t.Errorf("polygon params mismatch: %+v", layer.Shape)
	}
}

func TestArrowCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "arrow", "arrow_direction": "right",
		"arrow_head_length": 60.0, "arrow_head_width": 40.0, "arrow_shaft_width": 12.0,
	})
	if err != nil {
		t.Fatalf("TestArrowCompiles failed: %v", err)
	}
	if layer.Shape.Type != "arrow" || layer.Shape.ArrowDirection != "right" ||
		layer.Shape.ArrowHeadLength != 60.0 || layer.Shape.ArrowHeadWidth != 40.0 || layer.Shape.ArrowShaftWidth != 12.0 {
		t.Errorf("arrow params mismatch: %+v", layer.Shape)
	}
}

func TestArcCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "arc", "arc_start_deg": 45.0, "arc_sweep_deg": 270.0, "arc_thickness": 8.0,
	})
	if err != nil {
		t.Fatalf("TestArcCompiles failed: %v", err)
	}
	if layer.Shape.Type != "arc" || layer.Shape.ArcStartDeg != 45.0 ||
		layer.Shape.ArcSweepDeg != 270.0 || layer.Shape.ArcThickness != 8.0 {
		t.Errorf("arc params mismatch: %+v", layer.Shape)
	}
}

func TestPathCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "path",
		"path": []any{
			map[string]any{"type": "M", "points": []any{0.0, 0.0}},
			map[string]any{"type": "L", "points": []any{100.0, 100.0}},
			map[string]any{"type": "Z"},
		},
	})
	if err != nil {
		t.Fatalf("TestPathCompiles failed: %v", err)
	}
	if layer.Shape.Type != "path" || len(layer.Shape.Path) != 3 {
		t.Fatalf("expected 3 path commands, got %+v", layer.Shape.Path)
	}
	if layer.Shape.Path[0].Type != "M" || layer.Shape.Path[1].Type != "L" || layer.Shape.Path[2].Type != "Z" {
		t.Errorf("path command types mismatch: %+v", layer.Shape.Path)
	}
}

func TestGridCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "grid", "grid_spacing": 48.0, "grid_line_width": 1.5,
	})
	if err != nil {
		t.Fatalf("TestGridCompiles failed: %v", err)
	}
	if layer.Shape.Type != "grid" || layer.Shape.GridSpacing != 48.0 || layer.Shape.GridLineWidth != 1.5 {
		t.Errorf("grid params mismatch: %+v", layer.Shape)
	}
}

func TestDotGridCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "dot_grid", "grid_spacing": 36.0, "dot_radius": 2.5,
	})
	if err != nil {
		t.Fatalf("TestDotGridCompiles failed: %v", err)
	}
	if layer.Shape.Type != "dot_grid" || layer.Shape.GridSpacing != 36.0 || layer.Shape.DotRadius != 2.5 {
		t.Errorf("dot_grid params mismatch: %+v", layer.Shape)
	}
}

func TestDeviceFrameCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "device_frame", "device_frame_kind": "browser", "chrome": true,
	})
	if err != nil {
		t.Fatalf("TestDeviceFrameCompiles failed: %v", err)
	}
	if layer.Shape.Type != "device_frame" || layer.Shape.DeviceFrame != "browser" || layer.Shape.Chrome == nil || !*layer.Shape.Chrome {
		t.Errorf("device_frame params mismatch: %+v", layer.Shape)
	}
}

func TestWorldMapCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "world_map",
		"world_map": map[string]any{
			"projection":      "mercator",
			"center":          []any{12.5, 41.9},
			"zoom":            2.5,
			"highlight":       []any{"IT", "FR", "DE"},
			"highlight_color": "#FF0000",
			"route":           []any{[]any{12.5, 41.9}, []any{2.35, 48.85}},
			"dots":            true,
		},
	})
	if err != nil {
		t.Fatalf("TestWorldMapCompiles failed: %v", err)
	}
	wm := layer.Shape.WorldMap
	if wm == nil || wm.Projection != "mercator" || wm.Zoom != 2.5 || len(wm.Highlight) != 3 || len(wm.Route) != 2 || !wm.Dots {
		t.Errorf("world_map params mismatch: %+v", wm)
	}
}

func TestChartCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "chart",
		"chart": map[string]any{
			"chart_type": "bar",
			"data":       []any{10.0, 25.0, 42.0},
			"colors":     []any{"#FF0000", "#00FF00", "#0000FF"},
		},
	})
	if err != nil {
		t.Fatalf("TestChartCompiles failed: %v", err)
	}
	ch := layer.Shape.Chart
	if ch == nil || ch.ChartType != "bar" || len(ch.Data) != 3 || len(ch.Colors) != 3 {
		t.Fatalf("chart params mismatch: %+v", ch)
	}
	if ch.Data[0] != 10.0 || ch.Data[2] != 42.0 {
		t.Errorf("chart data values mismatch: %v", ch.Data)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. Stroke & Golden Framing
// ─────────────────────────────────────────────────────────────────────────────

func TestShapeStrokeCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape":  "rect",
		"stroke": map[string]any{"color": "#00F0FF", "width": 3.5},
	})
	if err != nil {
		t.Fatalf("TestShapeStrokeCompiles failed: %v", err)
	}
	if layer.Shape.Stroke == nil || layer.Shape.Stroke.Color != "#00F0FF" || layer.Shape.Stroke.Width != 3.5 {
		t.Errorf("stroke mismatch: %+v", layer.Shape.Stroke)
	}
}

func TestRoundedRectStrokeGolden(t *testing.T) {
	res, layer, err := compileSingleShape(t, map[string]any{
		"shape":  "rounded_rect",
		"width":  1200,
		"height": 700,
		"radius": 32.0,
		"fill":   "#0a0f1d",
		"stroke": map[string]any{"color": "#38BDF8", "width": 4.0},
	})
	if err != nil {
		t.Fatalf("TestRoundedRectStrokeGolden failed: %v", err)
	}
	if layer.Shape.Radius != 32.0 || layer.Shape.Stroke.Width != 4.0 || layer.Shape.Stroke.Color != "#38BDF8" {
		t.Errorf("rounded rect stroke properties mismatch: %+v", layer.Shape)
	}
	wire, err := res.Plan.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal plan: %v", err)
	}
	wireStr := string(wire)
	if !strings.Contains(wireStr, `"radius":32`) || !strings.Contains(wireStr, `"width":4`) || !strings.Contains(wireStr, `"color":"#38BDF8"`) {
		t.Errorf("golden wire JSON missing expected stroke/radius keys: %s", wireStr)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Gradient Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestLinearGradientCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"fill_gradient": map[string]any{
			"type":  "linear",
			"stops": []any{[]any{"#000000", 0.0}, []any{"#FFFFFF", 1.0}},
			"start": []any{0.0, 0.0},
			"end":   []any{0.0, 1.0},
		},
	})
	if err != nil {
		t.Fatalf("TestLinearGradientCompiles failed: %v", err)
	}
	grad := layer.Shape.Gradient
	if grad == nil || grad.Type != "linear" || len(grad.ColorStops) != 2 {
		t.Fatalf("linear gradient mismatch: %+v", grad)
	}
	if grad.Start[0] != 0 || grad.End[1] != 1 {
		t.Errorf("gradient start/end mismatch: start=%v end=%v", grad.Start, grad.End)
	}
}

func TestRadialGradientCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "ellipse",
		"fill_gradient": map[string]any{
			"type":  "radial",
			"stops": []any{[]any{"#FF0000", 0.0}, []any{"#0000FF", 1.0}},
			"start": []any{0.5, 0.5},
			"end":   []any{1.0, 1.0},
		},
	})
	if err != nil {
		t.Fatalf("TestRadialGradientCompiles failed: %v", err)
	}
	if layer.Shape.Gradient.Type != "radial" {
		t.Errorf("expected radial gradient, got %s", layer.Shape.Gradient.Type)
	}
}

func TestGradientStopOrderPreserved(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"fill_gradient": map[string]any{
			"type": "linear",
			"stops": []any{
				[]any{"#000000", 0.0},
				[]any{"#888888", 0.5},
				[]any{"#FFFFFF", 1.0},
			},
		},
	})
	if err != nil {
		t.Fatalf("TestGradientStopOrderPreserved failed: %v", err)
	}
	stops := layer.Shape.Gradient.ColorStops
	if len(stops) != 3 {
		t.Fatalf("expected 3 stops, got %d", len(stops))
	}
	if stops[0].Position != 0.0 || stops[1].Position != 0.5 || stops[2].Position != 1.0 {
		t.Errorf("stop order corrupted: %v, %v, %v", stops[0].Position, stops[1].Position, stops[2].Position)
	}
}

func TestGradientOpacityStops(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"fill_gradient": map[string]any{
			"type":  "linear",
			"stops": []any{[]any{"#000000", 0.0}, []any{"#FFFFFF", 1.0}},
			"opacity_stops": []any{
				map[string]any{"position": 0.0, "opacity": 0.2},
				map[string]any{"position": 1.0, "opacity": 0.95},
			},
		},
	})
	if err != nil {
		t.Fatalf("TestGradientOpacityStops failed: %v", err)
	}
	opStops := layer.Shape.Gradient.OpacityStops
	if len(opStops) != 2 || opStops[0].Opacity != 0.2 || opStops[1].Opacity != 0.95 {
		t.Errorf("opacity stops mismatch: %+v", opStops)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. Effects Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestNoiseCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"effects": []any{
			map[string]any{"type": "noise", "amount": 0.15},
		},
	})
	if err != nil {
		t.Fatalf("TestNoiseCompiles failed: %v", err)
	}
	if len(layer.Effects) != 1 || layer.Effects[0].Type != "noise" || layer.Effects[0].Params["amount"] != 0.15 {
		t.Errorf("noise effect mismatch: %+v", layer.Effects)
	}
}

func TestFractalNoiseCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"effects": []any{
			map[string]any{"type": "fractal_noise", "amplitude": 0.08, "octaves": 4},
		},
	})
	if err != nil {
		t.Fatalf("TestFractalNoiseCompiles failed: %v", err)
	}
	if len(layer.Effects) != 1 || layer.Effects[0].Type != "fractal_noise" {
		t.Errorf("fractal_noise effect mismatch: %+v", layer.Effects)
	}
}

func TestVignetteCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"effects": []any{
			map[string]any{"type": "vignette", "radius": 0.8, "softness": 0.4},
		},
	})
	if err != nil {
		t.Fatalf("TestVignetteCompiles failed: %v", err)
	}
	if len(layer.Effects) != 1 || layer.Effects[0].Type != "vignette" {
		t.Errorf("vignette effect mismatch: %+v", layer.Effects)
	}
}

func TestBloomCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"effects": []any{
			map[string]any{"type": "bloom", "radius": 24.0, "intensity": 0.5},
		},
	})
	if err != nil {
		t.Fatalf("TestBloomCompiles failed: %v", err)
	}
	if len(layer.Effects) != 1 || layer.Effects[0].Type != "bloom" {
		t.Errorf("bloom effect mismatch: %+v", layer.Effects)
	}
}

func TestGlowCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"effects": []any{
			map[string]any{"type": "glow", "radius": 32.0, "intensity": 0.6},
		},
	})
	if err != nil {
		t.Fatalf("TestGlowCompiles failed: %v", err)
	}
	if len(layer.Effects) != 1 || layer.Effects[0].Type != "glow" {
		t.Errorf("glow effect mismatch: %+v", layer.Effects)
	}
}

func TestGaussianBlurCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"effects": []any{
			map[string]any{"type": "gaussian_blur", "radius": 16.0},
		},
	})
	if err != nil {
		t.Fatalf("TestGaussianBlurCompiles failed: %v", err)
	}
	if len(layer.Effects) != 1 || layer.Effects[0].Type != "gaussian_blur" {
		t.Errorf("gaussian_blur effect mismatch: %+v", layer.Effects)
	}
}

func TestLightRaysCompiles(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "ellipse",
		"effects": []any{
			map[string]any{"type": "light_rays"},
		},
	})
	if err != nil {
		t.Fatalf("TestLightRaysCompiles failed: %v", err)
	}
	if len(layer.Effects) != 1 || layer.Effects[0].Type != "light_rays" {
		t.Errorf("light_rays effect mismatch: %+v", layer.Effects)
	}
}

func TestMultipleEffectsPreserveOrder(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"effects": []any{
			map[string]any{"type": "fractal_noise", "amplitude": 0.05},
			map[string]any{"type": "vignette"},
			map[string]any{"type": "noise", "amount": 0.02},
			map[string]any{"type": "bloom", "radius": 12.0},
		},
	})
	if err != nil {
		t.Fatalf("TestMultipleEffectsPreserveOrder failed: %v", err)
	}
	if len(layer.Effects) != 4 {
		t.Fatalf("expected 4 effects, got %d", len(layer.Effects))
	}
	expectedTypes := []string{"fractal_noise", "vignette", "noise", "bloom"}
	for i, exp := range expectedTypes {
		if layer.Effects[i].Type != exp {
			t.Errorf("effect[%d] type = %s, want %s", i, layer.Effects[i].Type, exp)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 6. Fail-Closed Bounds Checks
// ─────────────────────────────────────────────────────────────────────────────

func TestGridSpacingBelowMinRejected(t *testing.T) {
	_, _, err := compileSingleShape(t, map[string]any{"shape": "grid", "grid_spacing": 2.0})
	if err == nil {
		t.Errorf("expected rejection for grid_spacing < 4, got nil")
	}
}

func TestGridSpacingAboveMaxRejected(t *testing.T) {
	_, _, err := compileSingleShape(t, map[string]any{"shape": "grid", "grid_spacing": 600.0})
	if err == nil {
		t.Errorf("expected rejection for grid_spacing > 512, got nil")
	}
}

func TestDotRadiusBelowMinRejected(t *testing.T) {
	_, _, err := compileSingleShape(t, map[string]any{"shape": "dot_grid", "dot_radius": 0.2})
	if err == nil {
		t.Errorf("expected rejection for dot_radius < 0.5, got nil")
	}
}

func TestPointsBelowMinRejected(t *testing.T) {
	_, _, err := compileSingleShape(t, map[string]any{"shape": "star", "points": 2})
	if err == nil {
		t.Errorf("expected rejection for points < 3, got nil")
	}
}

func TestPointsAboveMaxRejected(t *testing.T) {
	_, _, err := compileSingleShape(t, map[string]any{"shape": "polygon", "points": 100})
	if err == nil {
		t.Errorf("expected rejection for points > 64, got nil")
	}
}

func TestNegativeRadiusRejected(t *testing.T) {
	_, _, err := compileSingleShape(t, map[string]any{"shape": "rounded_rect", "radius": -10.0})
	if err == nil {
		t.Errorf("expected rejection for negative radius, got nil")
	}
}

func TestZeroWidthRejected(t *testing.T) {
	_, _, err := compileSingleShape(t, map[string]any{"shape": "rect", "width": 0.0})
	if err == nil {
		t.Errorf("expected rejection for zero width, got nil")
	}
}

func TestNaNRejected(t *testing.T) {
	_, _, err := compileSingleShape(t, map[string]any{"shape": "grid", "grid_spacing": "NaN"})
	if err == nil {
		t.Errorf("expected rejection for NaN grid_spacing, got nil")
	}
}

func TestInfinityRejected(t *testing.T) {
	_, _, err := compileSingleShape(t, map[string]any{"shape": "grid", "grid_spacing": "Infinity"})
	if err == nil {
		t.Errorf("expected rejection for Inf grid_spacing, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 7. Registry Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestShapeStaticPresetRegistered(t *testing.T) {
	spec := templateSpecFor("shape_static")
	if !spec.Registered {
		t.Fatalf("expected 'shape_static' to be registered in templateRegistry")
	}
	if spec.RequiresPreset {
		t.Errorf("'shape_static' should not require preset asset directory")
	}
}

func TestUnknownShapePresetRejected(t *testing.T) {
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "test-unknown-preset",
		"video_id": "vid-1",
		"width": 1920, "height": 1080,
		"fps_num": 30, "fps_den": 1, "duration_ms": 1000,
		"items": [
			{
				"id": "item1",
				"kind": "shape",
				"template_id": "unregistered_custom_preset_404",
				"start_ms": 0, "end_ms": 1000,
				"params": {"shape": "rect"}
			}
		]
	}`)
	_, err := CompileSemantic(rawPlan)
	if err == nil {
		t.Errorf("expected rejection for unknown preset on shape, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 8. Compiler & Boundary Field Loss Guard (P0)
// ─────────────────────────────────────────────────────────────────────────────

func TestSemanticShapeToLayerGolden(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{
		"shape":  "rounded_rect",
		"width":  1400,
		"height": 800,
		"radius": 24.0,
		"fill":   "#101827",
		"stroke": map[string]any{"color": "#38BDF8", "width": 2.0},
		"effects": []any{
			map[string]any{"type": "vignette"},
			map[string]any{"type": "noise", "amount": 0.05},
		},
		"opacity": 0.9,
	})
	if err != nil {
		t.Fatalf("TestSemanticShapeToLayerGolden failed: %v", err)
	}
	if layer.ID != "test-shape-item" || layer.Type != "shape" {
		t.Errorf("layer ID/Type mismatch: %s / %s", layer.ID, layer.Type)
	}
	if layer.Shape.Type != "rounded_rect" || layer.Shape.Radius != 24.0 {
		t.Errorf("shape mismatch: %+v", layer.Shape)
	}
	if layer.Shape.Stroke == nil || layer.Shape.Stroke.Color != "#38BDF8" || layer.Shape.Stroke.Width != 2.0 {
		t.Errorf("stroke mismatch: %+v", layer.Shape.Stroke)
	}
	if len(layer.Effects) != 2 || layer.Effects[0].Type != "vignette" || layer.Effects[1].Type != "noise" {
		t.Errorf("effects mismatch: %+v", layer.Effects)
	}
	if layer.Opacity == nil || *layer.Opacity != 0.9 {
		t.Errorf("opacity mismatch: %v", layer.Opacity)
	}
}

func TestSemanticShapeToChrononGolden(t *testing.T) {
	res, _, err := compileSingleShape(t, map[string]any{
		"shape":        "grid",
		"grid_spacing": 40.0,
		"stroke":       map[string]any{"color": "#00F0FF", "width": 1.5},
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	wire, err := res.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(wire, &doc); err != nil {
		t.Fatalf("invalid json on wire: %v", err)
	}
	if doc["schema"] != "chronon.render-plan.v3" || doc["version"].(float64) != 3 {
		t.Errorf("schema header mismatch: %v v%v", doc["schema"], doc["version"])
	}
	layers := doc["layers"].([]any)
	l0 := layers[0].(map[string]any)
	shapeObj := l0["shape"].(map[string]any)
	spVal, hasSp := shapeObj["spacing"].(float64)
	if !hasSp {
		spVal, _ = shapeObj["grid_spacing"].(float64)
	}
	if shapeObj["type"] != "grid" || spVal != 40.0 {
		t.Errorf("wire shape mismatch: %+v", shapeObj)
	}
}

func TestNoShapeFieldsDropped(t *testing.T) {
	// P0: Pass a richly populated shape with all major fields and assert
	// that EVERY single authored field survives into the marshaled wire JSON.
	res, _, err := compileSingleShape(t, map[string]any{
		"shape":  "rounded_rect",
		"width":  1200,
		"height": 700,
		"radius": 28.0,
		"stroke": map[string]any{"color": "#38BDF8", "width": 3.0},
		"fill_gradient": map[string]any{
			"type":  "linear",
			"stops": []any{[]any{"#0a0e1a", 0.0}, []any{"#1a2332", 1.0}},
			"start": []any{0.0, 0.0},
			"end":   []any{0.0, 1.0},
			"opacity_stops": []any{
				map[string]any{"position": 0.0, "opacity": 0.3},
				map[string]any{"position": 1.0, "opacity": 0.9},
			},
		},
		"effects": []any{
			map[string]any{"type": "fractal_noise", "amplitude": 0.06},
			map[string]any{"type": "vignette"},
			map[string]any{"type": "noise", "amount": 0.04},
		},
		"opacity": 0.85,
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	wireBytes, err := res.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(wireBytes, &root); err != nil {
		t.Fatalf("wire json parse error: %v", err)
	}

	layers := root["layers"].([]any)
	if len(layers) == 0 {
		t.Fatalf("no layers in wire JSON")
	}
	l0 := layers[0].(map[string]any)
	shapeMap, ok := l0["shape"].(map[string]any)
	if !ok {
		t.Fatalf("shape object missing on wire: %v", l0)
	}

	// 1. Assert radius is PRESENT
	if rad, ok := shapeMap["radius"].(float64); !ok || rad != 28.0 {
		t.Errorf("radius field dropped or corrupted: %v", shapeMap["radius"])
	}

	// 2. Assert stroke is PRESENT
	strokeMap, ok := shapeMap["stroke"].(map[string]any)
	if !ok || strokeMap["width"].(float64) != 3.0 || strokeMap["color"].(string) != "#38BDF8" {
		t.Errorf("stroke field dropped or corrupted: %v", shapeMap["stroke"])
	}

	// 3. Assert gradient is PRESENT (in fill or gradient)
	gradMap, ok := shapeMap["fill"].(map[string]any)
	if !ok {
		gradMap, ok = shapeMap["gradient"].(map[string]any)
	}
	if !ok || gradMap["type"].(string) != "linear" {
		t.Errorf("gradient field dropped or corrupted: fill=%v grad=%v", shapeMap["fill"], shapeMap["gradient"])
	}
	if cStops, ok := gradMap["color_stops"].([]any); !ok || len(cStops) != 2 {
		t.Errorf("gradient color_stops dropped: %v", gradMap["color_stops"])
	}
	if opStops, ok := gradMap["opacity_stops"].([]any); !ok || len(opStops) != 2 {
		t.Errorf("gradient opacity_stops dropped: %v", gradMap["opacity_stops"])
	}

	// 4. Assert effects are PRESENT and inlined
	effList, ok := l0["effects"].([]any)
	if !ok || len(effList) != 3 {
		t.Fatalf("effects array dropped or corrupted: %v", l0["effects"])
	}
	e0 := effList[0].(map[string]any)
	if e0["type"] != "fractal_noise" || e0["amplitude"].(float64) != 0.06 {
		t.Errorf("fractal_noise params dropped: %v", e0)
	}
	e1 := effList[1].(map[string]any)
	if e1["type"] != "vignette" {
		t.Errorf("vignette dropped: %v", e1)
	}
	e2 := effList[2].(map[string]any)
	if e2["type"] != "noise" || e2["amount"].(float64) != 0.04 {
		t.Errorf("noise params dropped: %v", e2)
	}

	// 5. Assert opacity is PRESENT
	if op, ok := l0["opacity"].(float64); !ok || op != 0.85 {
		t.Errorf("opacity dropped or corrupted: %v", l0["opacity"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 9. Layering & Lifetime
// ─────────────────────────────────────────────────────────────────────────────

func TestBackgroundShapeZBelowImage(t *testing.T) {
	// Shape at start, image following
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "test-zorder",
		"video_id": "vid-z",
		"width": 1920, "height": 1080,
		"fps_num": 30, "fps_den": 1, "duration_ms": 5000,
		"items": [
			{
				"id": "bg-shape",
				"kind": "shape",
				"template_id": "shape_static",
				"start_ms": 0, "end_ms": 5000,
				"params": {"shape": "rect", "fill": "#000000"}
			},
			{
				"id": "content-image",
				"kind": "image",
				"template_id": "image_clean",
				"start_ms": 0, "end_ms": 5000,
				"asset_refs": [{"asset_id": "img1", "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "url": "file://dummy.png"}],
				"params": {"source_width": 800, "source_height": 600}
			}
		]
	}`)
	res, err := CompileSemantic(rawPlan)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(res.Plan.Layers) < 2 {
		t.Fatalf("expected at least 2 layers, got %d", len(res.Plan.Layers))
	}
	// Background shape must be rendered before content image (layer index 0 vs subsequent)
	if res.Plan.Layers[0].ID != "bg-shape" {
		t.Errorf("expected bg-shape at index 0, got %s", res.Plan.Layers[0].ID)
	}
}

func TestDecorativeGridZOrdering(t *testing.T) {
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "test-decorative-grid",
		"video_id": "vid-grid",
		"width": 1920, "height": 1080,
		"fps_num": 30, "fps_den": 1, "duration_ms": 5000,
		"items": [
			{
				"id": "bg-base",
				"kind": "shape",
				"template_id": "shape_static",
				"start_ms": 0, "end_ms": 5000,
				"params": {"shape": "rect", "fill": "#050811"}
			},
			{
				"id": "bg-grid",
				"kind": "shape",
				"template_id": "shape_static",
				"start_ms": 0, "end_ms": 5000,
				"params": {"shape": "grid", "grid_spacing": 64}
			}
		]
	}`)
	res, err := CompileSemantic(rawPlan)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(res.Plan.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(res.Plan.Layers))
	}
	if res.Plan.Layers[0].ID != "bg-base" || res.Plan.Layers[1].ID != "bg-grid" {
		t.Errorf("grid ordering mismatch: [0]=%s [1]=%s", res.Plan.Layers[0].ID, res.Plan.Layers[1].ID)
	}
}

func TestBackgroundFullCompositionLifetime(t *testing.T) {
	_, layer, err := compileSingleShape(t, map[string]any{"shape": "rect"})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	// 5000ms at 30fps = 150 frames
	if layer.StartFrame != 0 {
		t.Errorf("StartFrame = %d, want 0", layer.StartFrame)
	}
	if layer.DurationFrames != 150 {
		t.Errorf("DurationFrames = %d, want 150", layer.DurationFrames)
	}
}

func TestShapeStartEndFrameConversion(t *testing.T) {
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "test-frames",
		"video_id": "vid-f",
		"width": 1920, "height": 1080,
		"fps_num": 60, "fps_den": 1, "duration_ms": 10000,
		"items": [
			{
				"id": "mid-shape",
				"kind": "shape",
				"start_ms": 2000, "end_ms": 5000,
				"params": {"shape": "rect"}
			}
		]
	}`)
	res, err := CompileSemantic(rawPlan)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	layer := res.Plan.Layers[0]
	// 2000ms at 60fps = 120 frames start; (5000-2000)ms at 60fps = 180 frames duration
	if layer.StartFrame != 120 {
		t.Errorf("StartFrame = %d, want 120", layer.StartFrame)
	}
	if layer.DurationFrames != 180 {
		t.Errorf("DurationFrames = %d, want 180", layer.DurationFrames)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 10. Determinism
// ─────────────────────────────────────────────────────────────────────────────

func TestShapePlanDeterministic(t *testing.T) {
	planJSON := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "test-determinism",
		"video_id": "vid-det",
		"width": 1920, "height": 1080,
		"fps_num": 30, "fps_den": 1, "duration_ms": 5000,
		"items": [
			{
				"id": "det-shape",
				"kind": "shape",
				"start_ms": 0, "end_ms": 5000,
				"params": {
					"shape": "rounded_rect",
					"width": 800, "height": 500, "radius": 20.0,
					"fill_gradient": {
						"type": "linear",
						"stops": [["#111111", 0.0], ["#333333", 1.0]]
					},
					"effects": [{"type": "vignette"}, {"type": "noise", "amount": 0.05}]
				}
			}
		]
	}`)

	res1, err := CompileSemantic(planJSON)
	if err != nil {
		t.Fatalf("run 1 failed: %v", err)
	}
	m1, err := res1.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal 1 failed: %v", err)
	}

	res2, err := CompileSemantic(planJSON)
	if err != nil {
		t.Fatalf("run 2 failed: %v", err)
	}
	m2, err := res2.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal 2 failed: %v", err)
	}

	if !bytes.Equal(m1, m2) {
		t.Fatalf("non-deterministic compilation output:\nRUN 1: %s\nRUN 2: %s", string(m1), string(m2))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 11. Canaries & E2E Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestDocumentaryBackgroundE2E(t *testing.T) {
	// Canary A: Rect + Linear Gradient + Fractal Noise + Noise + Vignette + Grid
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "canary-a-doc",
		"video_id": "canary-a",
		"width": 1920, "height": 1080,
		"fps_num": 30, "fps_den": 1, "duration_ms": 2000,
		"items": [
			{
				"id": "doc-bg",
				"kind": "shape",
				"start_ms": 0, "end_ms": 2000,
				"params": {
					"shape": "rect",
					"fill_gradient": {
						"type": "linear",
						"stops": [["#090D16", 0.0], ["#131B2A", 1.0]],
						"start": [0.0, 0.0], "end": [0.0, 1.0]
					},
					"effects": [
						{"type": "fractal_noise", "amplitude": 0.06},
						{"type": "vignette"},
						{"type": "noise", "amount": 0.03}
					]
				}
			},
			{
				"id": "doc-grid",
				"kind": "shape",
				"start_ms": 0, "end_ms": 2000,
				"params": {
					"shape": "grid",
					"grid_spacing": 64.0,
					"grid_line_width": 1.0,
					"stroke": {"color": "#1C2738", "width": 1.0},
					"opacity": 0.4
				}
			}
		]
	}`)

	res, err := CompileSemantic(rawPlan)
	if err != nil {
		t.Fatalf("Canary A compilation failed: %v", err)
	}
	if len(res.Plan.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(res.Plan.Layers))
	}
	l0 := res.Plan.Layers[0]
	if l0.Shape.Gradient == nil || len(l0.Effects) != 3 {
		t.Errorf("layer 0 missing gradient or effects: %+v", l0)
	}
	l1 := res.Plan.Layers[1]
	if l1.Shape.Type != "grid" || l1.Shape.GridSpacing != 64.0 {
		t.Errorf("layer 1 grid mismatch: %+v", l1.Shape)
	}
}

func TestMediaCardBackgroundE2E(t *testing.T) {
	// Canary B: Media Card (Rounded Rect + stroke + bounds padding)
	imageW := 800.0
	imageH := 500.0
	cardW := imageW + 16.0
	cardH := imageH + 16.0
	radius := 32.0

	res, layer, err := compileSingleShape(t, map[string]any{
		"shape":  "rounded_rect",
		"width":  cardW,
		"height": cardH,
		"radius": radius,
		"fill":   "#0A0F1D",
		"stroke": map[string]any{"color": "#38BDF8", "width": 4.0},
	})
	if err != nil {
		t.Fatalf("Canary B compilation failed: %v", err)
	}
	if layer.Size[0] != cardW || layer.Size[1] != cardH {
		t.Errorf("card bounds mismatch: size=%v want [%v, %v]", layer.Size, cardW, cardH)
	}
	if layer.Shape.Radius != radius {
		t.Errorf("card radius mismatch: %v want %v", layer.Shape.Radius, radius)
	}
	if layer.Shape.Stroke.Width != 4.0 {
		t.Errorf("card stroke mismatch: %v want 4.0", layer.Shape.Stroke.Width)
	}
	_ = res
}

func TestGeneratedBackgroundE2E(t *testing.T) {
	// Canary C: World Map + Route + Dot Grid + Glow + Vignette
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "canary-c-gen",
		"video_id": "canary-c",
		"width": 1920, "height": 1080,
		"fps_num": 30, "fps_den": 1, "duration_ms": 2000,
		"items": [
			{
				"id": "c-map",
				"kind": "shape",
				"start_ms": 0, "end_ms": 2000,
				"params": {
					"shape": "world_map",
					"world_map": {
						"projection": "mercator",
						"center": [15.0, 48.0],
						"zoom": 2.5,
						"highlight": ["IT", "US", "DE"],
						"highlight_color": "#FF4444",
						"route": [[12.5, 41.9], [-74.0, 40.7]],
						"dots": true
					},
					"effects": [
						{"type": "vignette"},
						{"type": "glow", "radius": 16.0}
					]
				}
			},
			{
				"id": "c-dots",
				"kind": "shape",
				"start_ms": 0, "end_ms": 2000,
				"params": {
					"shape": "dot_grid",
					"grid_spacing": 48.0,
					"dot_radius": 2.0,
					"fill": "#00F0FF",
					"opacity": 0.5
				}
			}
		]
	}`)
	res, err := CompileSemantic(rawPlan)
	if err != nil {
		t.Fatalf("Canary C compilation failed: %v", err)
	}
	if len(res.Plan.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(res.Plan.Layers))
	}
	mapLayer := res.Plan.Layers[0]
	if mapLayer.Shape.WorldMap == nil || len(mapLayer.Effects) != 2 {
		t.Errorf("world map or effects missing on Canary C: %+v", mapLayer)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 12. Chronon Roundtrip Validation
// ─────────────────────────────────────────────────────────────────────────────

func TestShapeChrononRoundTrip(t *testing.T) {
	// 1. Compile a semantic plan with shape to Chronon JSON
	res, layer, err := compileSingleShape(t, map[string]any{
		"shape":  "rounded_rect",
		"width":  1200,
		"height": 700,
		"radius": 24.0,
		"fill":   "#101827",
		"stroke": map[string]any{"color": "#38BDF8", "width": 2.0},
		"effects": []any{
			map[string]any{"type": "vignette"},
		},
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if layer.Shape.Type != "rounded_rect" {
		t.Fatalf("layer shape type mismatch: %s", layer.Shape.Type)
	}

	wireBytes, err := res.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// 2. Discover chronon3d_cli binary
	chrononBin := os.Getenv("CHRONON_BIN")
	assetsRoot := ""
	if wsRoot, ok := findWorkspaceRoot("."); ok {
		assetsRoot = filepath.Join(wsRoot, "Chronon3d")
		if chrononBin == "" {
			candidate := filepath.Join(wsRoot, "Chronon3d", "build", "chronon", "linux-video-release", "apps", "chronon3d_cli", "chronon3d_cli")
			if _, err := os.Stat(candidate); err == nil {
				chrononBin = candidate
			}
		}
	}
	if chrononBin == "" {
		cwd, _ := os.Getwd()
		candidateDirs := []string{
			"../../Chronon3d",
			"../../../Chronon3d",
			"../Chronon3d",
		}
		dir := cwd
		for i := 0; i < 5; i++ {
			c := filepath.Join(dir, "Chronon3d")
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				candidateDirs = append(candidateDirs, c)
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		for _, candidateDir := range candidateDirs {
			candidate := filepath.Join(candidateDir, "build", "chronon", "linux-video-release", "apps", "chronon3d_cli", "chronon3d_cli")
			if _, err := os.Stat(candidate); err == nil {
				chrononBin = candidate
				assetsRoot = candidateDir
				break
			}
		}
	}
	if chrononBin == "" {
		t.Skip("chronon3d_cli binary not found; skipping binary validator execution")
	}

	// 3. Write Chronon plan to temp file and run `chronon3d_cli validate --plan`
	tmpDir := t.TempDir()
	planPath := filepath.Join(tmpDir, "roundtrip.plan.json")
	if err := os.WriteFile(planPath, wireBytes, 0o644); err != nil {
		t.Fatalf("failed to write plan file: %v", err)
	}

	cmdArgs := []string{"validate", "--plan", planPath}
	if assetsRoot != "" {
		cmdArgs = append(cmdArgs, "--assets-root", assetsRoot)
	}
	cmd := exec.Command(chrononBin, cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chronon3d_cli validate failed on roundtrip plan:\nOutput:\n%s\nError: %v", string(out), err)
	}

	// Assert Chronon reported valid JSON
	var valDoc struct {
		Valid  bool   `json:"valid"`
		Status string `json:"status"`
	}
	// The CLI output contains JSON at the end
	lines := strings.Split(string(out), "\n")
	var jsonLines []string
	capture := false
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "{") {
			capture = true
		}
		if capture {
			jsonLines = append(jsonLines, l)
		}
	}
	if len(jsonLines) > 0 {
		if err := json.Unmarshal([]byte(strings.Join(jsonLines, "\n")), &valDoc); err == nil {
			if !valDoc.Valid || valDoc.Status != "PASS" {
				t.Errorf("chronon3d_cli validator did not return PASS: %+v", valDoc)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 13. Regression
// ─────────────────────────────────────────────────────────────────────────────

func TestExistingImageBackgroundUnchanged(t *testing.T) {
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "test-reg-image",
		"video_id": "vid-reg",
		"width": 1920, "height": 1080,
		"fps_num": 30, "fps_den": 1, "duration_ms": 2000,
		"items": [
			{
				"id": "existing-img",
				"kind": "image",
				"template_id": "image_clean",
				"start_ms": 0, "end_ms": 2000,
				"asset_refs": [{"asset_id": "a1", "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "url": "file://dummy.png"}],
				"params": {"source_width": 1920, "source_height": 1080}
			}
		]
	}`)
	res, err := CompileSemantic(rawPlan)
	if err != nil {
		t.Fatalf("existing image overlay plan failed to compile: %v", err)
	}
	if len(res.Plan.Layers) == 0 {
		t.Fatalf("expected compiled layers for image item")
	}
	// Verify image layer was lowered without regression
	imgLayer := res.Plan.Layers[0]
	if !strings.HasPrefix(imgLayer.ID, "existing-img") {
		t.Errorf("image layer ID mismatch: %s", imgLayer.ID)
	}
}

func TestExistingOverlayPlansStillCompile(t *testing.T) {
	// Text subtitle overlay
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "test-reg-text",
		"video_id": "vid-txt",
		"width": 1920, "height": 1080,
		"fps_num": 30, "fps_den": 1, "duration_ms": 1000,
		"items": [
			{
				"id": "t1",
				"kind": "entity_image",
				"template_id": "image_clean",
				"start_ms": 0, "end_ms": 1000,
				"asset_refs": [{"asset_id": "img1", "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "url": "file://entity.png"}],
				"params": {"source_width": 400, "source_height": 300}
			}
		]
	}`)
	res, err := CompileSemantic(rawPlan)
	if err != nil {
		t.Fatalf("existing entity_image plan failed: %v", err)
	}
	if len(res.Plan.Layers) == 0 {
		t.Fatalf("expected compiled layers for entity_image")
	}
}

func TestBrowserBackgroundE2E(t *testing.T) {
	res, layer, err := compileSingleShape(t, map[string]any{
		"shape":  "device_frame",
		"device": "browser",
		"width":  1400.0,
		"height": 900.0,
		"stroke": map[string]any{"color": "#334155", "width": 2.0},
	})
	if err != nil {
		t.Fatalf("browser device frame failed: %v", err)
	}
	if layer.Shape.Device != "browser" {
		t.Errorf("expected device=browser, got %s", layer.Shape.Device)
	}
	_ = res
}

func TestWorldMapBackgroundE2E(t *testing.T) {
	res, layer, err := compileSingleShape(t, map[string]any{
		"shape": "world_map",
		"world_map": map[string]any{
			"projection":      "mercator",
			"center":          []any{12.5, 41.9},
			"zoom":            3.0,
			"highlight":       []any{"IT", "FR", "ES"},
			"highlight_color": "#FFCC00",
			"route":           []any{[]any{12.5, 41.9}, []any{2.35, 48.85}},
		},
	})
	if err != nil {
		t.Fatalf("world map background failed: %v", err)
	}
	if layer.Shape.WorldMap == nil {
		t.Fatalf("expected non-nil WorldMap payload")
	}
	if len(layer.Shape.WorldMap.Highlight) != 3 {
		t.Errorf("expected 3 highlights, got %d", len(layer.Shape.WorldMap.Highlight))
	}
	_ = res
}

func TestNoiseVignetteGpuSafe(t *testing.T) {
	chrononBin, assetsRoot := findChrononBin(t)
	if chrononBin == "" {
		t.Skip("chronon3d_cli binary not found; skipping Vulkan execution")
	}

	res, err := CompileSemantic([]byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "gpu-noise-vignette",
		"video_id": "gpu-canary",
		"width": 1280, "height": 720,
		"fps_num": 30, "fps_den": 1, "duration_ms": 1000,
		"items": [
			{
				"id": "bg",
				"kind": "shape",
				"start_ms": 0, "end_ms": 1000,
				"params": {
					"shape": "rect",
					"fill_gradient": {
						"type": "linear",
						"stops": [["#090D16", 0.0], ["#131B2A", 1.0]],
						"start": [0.0, 0.0], "end": [0.0, 1.0]
					},
					"effects": [
						{"type": "fractal_noise", "amplitude": 0.06},
						{"type": "vignette"},
						{"type": "noise", "amount": 0.03}
					]
				}
			},
			{
				"id": "grid",
				"kind": "shape",
				"start_ms": 0, "end_ms": 1000,
				"params": {
					"shape": "grid",
					"grid_spacing": 64.0,
					"grid_line_width": 1.0,
					"stroke": {"color": "#1C2738", "width": 1.0},
					"opacity": 0.4
				}
			}
		]
	}`))
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	wireBytes, err := res.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	tmpDir := t.TempDir()
	planPath := filepath.Join(tmpDir, "plan.json")
	outPath := filepath.Join(tmpDir, "vulkan_frame0.png")
	if err := os.WriteFile(planPath, wireBytes, 0o644); err != nil {
		t.Fatalf("failed to write plan: %v", err)
	}

	args := []string{"render", "plan", "--plan", planPath, "--backend", "vulkan", "--frame", "0", "-o", outPath}
	if assetsRoot != "" {
		args = append(args, "--assets-root", assetsRoot)
	}
	cmd := exec.Command(chrononBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chronon3d_cli render plan --backend vulkan failed:\nOutput:\n%s\nErr: %v", string(out), err)
	}

	actualOut := resolveOutputFrame(outPath)
	info, err := os.Stat(actualOut)
	if err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty output frame at %s (resolved %s), err: %v", outPath, actualOut, err)
	}
}

// TestShapeSoftwareGpuParity compares a rounded backing plate with an inset
// rounded image surface through decoded MP4 frames. The still/PNG lane is not
// pixel truth and deliberately is not used as the backend oracle here.
func TestShapeSoftwareGpuParity(t *testing.T) {
	chrononBin, assetsRoot := findChrononBin(t)
	if chrononBin == "" {
		t.Skip("chronon3d_cli binary not found; skipping GPU parity test")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg is required to compare decoded renderer output: %v", err)
	}

	res, err := CompileSemantic([]byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "parity-test",
		"video_id": "parity",
		"width": 640, "height": 360,
		"fps_num": 30, "fps_den": 1, "duration_ms": 1000,
		"items": [
			{
				"id": "frame",
				"kind": "shape",
				"start_ms": 0, "end_ms": 1000,
				"params": {"shape": "rounded_rect", "width": 600, "height": 320, "radius": 28, "fill": "#38BDF8"}
			},
			{
				"id": "image_surface",
				"kind": "shape",
				"start_ms": 0, "end_ms": 1000,
				"params": {"shape": "rounded_rect", "width": 592, "height": 312, "radius": 24, "fill": "#D02020"}
			}
		]
	}`))
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	wireBytes, err := res.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	tmpDir := t.TempDir()
	planPath := filepath.Join(tmpDir, "plan.json")
	swOut := filepath.Join(tmpDir, "sw.mp4")
	vkOut := filepath.Join(tmpDir, "vk.mp4")
	if err := os.WriteFile(planPath, wireBytes, 0o644); err != nil {
		t.Fatalf("failed to write plan: %v", err)
	}

	render := func(backend, output string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		args := []string{"render", "--plan", planPath,
			"--backend", backend, "--encoder-backend", "pipe", "--hardware", "none",
			"--gpu-hot-path-mode", "auto", "--encode-preset", "ultrafast", "-o", output}
		if assetsRoot != "" {
			args = append(args, "--assets-root", assetsRoot)
		}
		cmd := exec.CommandContext(ctx, chrononBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s render failed:\n%s\nErr: %v", backend, string(out), err)
		}
		info, err := os.Stat(output)
		if err != nil || info.Size() == 0 {
			t.Fatalf("%s render did not produce a non-empty video: %v", backend, err)
		}
	}
	render("software", swOut)
	render("vulkan", vkOut)
	swFramePath := filepath.Join(tmpDir, "sw_frame.png")
	vkFramePath := filepath.Join(tmpDir, "vk_frame.png")
	swFrame := extractVideoFramePNG(t, swOut, 0)
	vkFrame := extractVideoFramePNG(t, vkOut, 0)
	if err := os.WriteFile(swFramePath, swFrame, 0o600); err != nil {
		t.Fatalf("write software frame: %v", err)
	}
	if err := os.WriteFile(vkFramePath, vkFrame, 0o600); err != nil {
		t.Fatalf("write Vulkan frame: %v", err)
	}
	assertRenderFramesClose(t, swFramePath, vkFramePath)
}

func TestUnsupportedGpuEffectFailsClosed(t *testing.T) {
	chrononBin, assetsRoot := findChrononBin(t)
	if chrononBin == "" {
		t.Skip("chronon3d_cli binary not found; skipping strict native check")
	}

	res, err := CompileSemantic([]byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "strict-gpu-test",
		"video_id": "strict-gpu",
		"width": 640, "height": 360,
		"fps_num": 30, "fps_den": 1, "duration_ms": 1000,
		"items": [
			{
				"id": "rect",
				"kind": "shape",
				"start_ms": 0, "end_ms": 1000,
				"params": {
					"shape": "rect",
					"fill": "#0A0F1D",
					"effects": [
						{"type": "fractal_noise"}
					]
				}
			}
		]
	}`))
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	wireBytes, err := res.Plan.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	tmpDir := t.TempDir()
	planPath := filepath.Join(tmpDir, "plan.json")
	outPath := filepath.Join(tmpDir, "out.png")
	if err := os.WriteFile(planPath, wireBytes, 0o644); err != nil {
		t.Fatalf("failed to write plan: %v", err)
	}

	args := []string{"render", "plan", "--plan", planPath, "--backend", "vulkan", "--gpu-hot-path-mode", "require_gpu_native", "--frame", "0", "-o", outPath}
	if assetsRoot != "" {
		args = append(args, "--assets-root", assetsRoot)
	}
	cmd := exec.Command(chrononBin, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected command to fail closed in strict native mode with unsupported effect, but succeeded:\n%s", string(out))
	}
	t.Logf("command failed closed as expected: %s", string(out))
}

func TestBackgroundLongDuration(t *testing.T) {
	rawPlan := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "test-long-duration",
		"video_id": "long-bg",
		"width": 1920, "height": 1080,
		"fps_num": 30, "fps_den": 1, "duration_ms": 1800000,
		"items": [
			{
				"id": "bg-long",
				"kind": "shape",
				"start_ms": 0, "end_ms": 1800000,
				"params": {
					"shape": "rect",
					"fill": "#0A0F1D"
				}
			}
		]
	}`)
	res, err := CompileSemantic(rawPlan)
	if err != nil {
		t.Fatalf("compile 30min plan failed: %v", err)
	}
	if len(res.Plan.Layers) == 0 {
		t.Fatalf("expected compiled layers")
	}
	layer := res.Plan.Layers[0]
	if layer.StartFrame != 0 || layer.DurationFrames != 54000 {
		t.Errorf("expected 0..54000 frames lifetime, got start=%d dur=%d", layer.StartFrame, layer.DurationFrames)
	}
}

func TestManyShapeLayers(t *testing.T) {
	items := make([]map[string]any, 500)
	for i := 0; i < 500; i++ {
		items[i] = map[string]any{
			"id":       fmt.Sprintf("shape-%d", i),
			"kind":     "shape",
			"start_ms": 0,
			"end_ms":   1000,
			"params": map[string]any{
				"shape":  "rect",
				"width":  100 + i%50,
				"height": 100 + i%50,
				"fill":   "#102030",
			},
		}
	}
	payload := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "many-shapes",
		"video_id":       "stress-500",
		"width":          1920,
		"height":         1080,
		"fps_num":        30,
		"fps_den":        1,
		"duration_ms":    1000,
		"items":          items,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	res, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compilation of 500 shapes failed: %v", err)
	}
	if len(res.Plan.Layers) != 500 {
		t.Fatalf("expected 500 layers, got %d", len(res.Plan.Layers))
	}
}

func TestDenseDotGrid(t *testing.T) {
	res, layer, err := compileSingleShape(t, map[string]any{
		"shape":        "dot_grid",
		"grid_spacing": 8.0,
		"dot_radius":   0.5,
		"fill":         "#00F0FF",
	})
	if err != nil {
		t.Fatalf("dense dot grid failed: %v", err)
	}
	if layer.Shape.GridSpacing != 8.0 || layer.Shape.DotRadius != 0.5 {
		t.Errorf("dense dot grid parameters mismatch: %+v", layer.Shape)
	}
	_ = res
}

func TestGradientManyStops(t *testing.T) {
	stops := make([][2]any, 32)
	for i := 0; i < 32; i++ {
		stops[i] = [2]any{"#102030", float64(i) / 31.0}
	}
	res, layer, err := compileSingleShape(t, map[string]any{
		"shape": "rect",
		"fill_gradient": map[string]any{
			"type":  "linear",
			"stops": stops,
		},
	})
	if err != nil {
		t.Fatalf("gradient many stops failed: %v", err)
	}
	if layer.Shape.Gradient == nil || len(layer.Shape.Gradient.Stops) != 32 {
		t.Fatalf("expected 32 gradient stops, got %v", layer.Shape.Gradient)
	}
	_ = res
}
