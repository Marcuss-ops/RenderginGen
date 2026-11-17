package overlay

import (
	"strings"
	"testing"
)

func modernMapPlanWithPins(place string, pins []any, extra map[string]any) map[string]any {
	params := map[string]any{"map_type": MapTypeModernMap1, "map_place": place}
	for key, value := range extra {
		params[key] = value
	}
	plan := modernMapPlan(place, params)
	item := plan["items"].([]any)[0].(map[string]any)
	item["map"].(map[string]any)["pins"] = pins
	return plan
}

// Unified plate: pins lower on top of the vector territory with the same
// builders as the raster lane.
func TestModernMap1UnifiesPinsAboveVectorPlate(t *testing.T) {
	pins := []any{
		map[string]any{"id": "rome", "label": "Rome", "latitude": 41.9028, "longitude": 12.4964, "color": "#E11D48", "radius_px": 12.0},
	}
	result, err := compileMapTestPlan(t, modernMapPlanWithPins("Italy", pins, nil))
	if err != nil {
		t.Fatalf("compile ModernMap1 with pins: %v", err)
	}
	var marker, label bool
	for _, layer := range result.Plan.Layers {
		if layer.ID == "shape-map:map_pin:rome" {
			marker = true
		}
		if layer.ID == "shape-map:map_pin_label:rome" {
			label = true
		}
	}
	if !marker || !label {
		t.Fatalf("unified pins missing: marker=%v label=%v layers=%v", marker, label, len(result.Plan.Layers))
	}
}

// Static routes project through the vector fit without camera LODs.
func TestModernMap1UnifiesStaticRoutes(t *testing.T) {
	pins := []any{
		map[string]any{"id": "rome", "label": "Rome", "latitude": 41.9028, "longitude": 12.4964, "color": "#E11D48", "radius_px": 12.0},
		map[string]any{"id": "milan", "label": "Milan", "latitude": 45.4642, "longitude": 9.19, "color": "#E11D48", "radius_px": 12.0},
	}
	plan := modernMapPlanWithPins("Italy", pins, nil)
	item := plan["items"].([]any)[0].(map[string]any)
	item["map"].(map[string]any)["routes"] = []any{
		map[string]any{
			"id": "rome-milan", "color": "#38D9C5", "width_px": 5.0,
			"stops": []any{
				map[string]any{"latitude": 41.9028, "longitude": 12.4964},
				map[string]any{"latitude": 45.4642, "longitude": 9.19},
			},
		},
	}
	result, err := compileMapTestPlan(t, plan)
	if err != nil {
		t.Fatalf("compile ModernMap1 with routes: %v", err)
	}
	found := false
	for _, layer := range result.Plan.Layers {
		if layer.ID == "shape-map:modernmap1:route:rome-milan" {
			found = true
			if layer.Shape == nil || len(layer.Shape.Path) == 0 {
				t.Fatalf("route layer has no path: %+v", layer)
			}
		}
	}
	if !found {
		t.Fatalf("unified route layer missing")
	}
}

// Presentation passthrough is admitted and camera LODs stay rejected.
func TestModernMap1AdmitsPresentationButRejectsCameraLODs(t *testing.T) {
	pins := []any{
		map[string]any{"id": "rome", "label": "Rome", "latitude": 41.9028, "longitude": 12.4964, "color": "#E11D48", "radius_px": 12.0},
	}
	plan := modernMapPlanWithPins("Italy", pins, map[string]any{"map_animation": "radar_location_reveal"})
	if _, err := compileMapTestPlan(t, plan); err != nil {
		t.Fatalf("certified map_animation must be admitted on ModernMap1: %v", err)
	}
	bad := modernMapPlanWithPins("Italy", pins, nil)
	badItem := bad["items"].([]any)[0].(map[string]any)
	badItem["map"].(map[string]any)["camera_move"] = map[string]any{
		"from": map[string]any{"latitude": 41.9, "longitude": 12.5},
		"to":   map[string]any{"latitude": 45.4, "longitude": 9.19},
		"start_zoom": 4.0, "end_zoom": 6.0, "start_tilt_deg": 0.0, "end_tilt_deg": 0.0, "bearing_deg": 0.0,
	}
	if _, err := compileMapTestPlan(t, bad); err == nil || !strings.Contains(err.Error(), "camera") {
		t.Fatalf("camera_move on ModernMap1 must fail closed, got %v", err)
	}
}

// Quality: stacked halo + two-pass outline + legible label shadow.
func TestModernMap1QualityLayers(t *testing.T) {
	result, err := compileMapTestPlan(t, modernMapPlan("Italy", nil))
	if err != nil {
		t.Fatalf("compile ModernMap1 Italy: %v", err)
	}
	var halo, haloCore, outer, graticuleOpacity bool
	for _, layer := range result.Plan.Layers {
		switch layer.ID {
		case "shape-map:modernmap1:halo":
			halo = true
		case "shape-map:modernmap1:halo:core":
			haloCore = true
		case "shape-map:modernmap1:graticule":
			if layer.Opacity != nil && *layer.Opacity == 0.6 {
				graticuleOpacity = true
			}
		}
		if strings.Contains(layer.ID, ":modernmap1:outline:outer:") {
			outer = true
		}
	}
	if !halo || !haloCore || !outer || !graticuleOpacity {
		t.Fatalf("quality layers incomplete: halo=%v core=%v outer=%v graticule=%v", halo, haloCore, outer, graticuleOpacity)
	}
}

// The unified plate draws a marker, not a hole: a light core inside the
// contract colour, and a halo that keeps the place name readable on the
// saturated territory. The authored label size is preserved by the fit box.
func TestModernMap1PinDotAndLabelHalo(t *testing.T) {
	pins := []any{
		map[string]any{"id": "rome", "label": "Rome", "latitude": 41.9028, "longitude": 12.4964, "color": "#1F252A", "radius_px": 22.0},
	}
	result, err := compileMapTestPlan(t, modernMapPlanWithPins("Italy", pins, nil))
	if err != nil {
		t.Fatalf("compile ModernMap1 with pin: %v", err)
	}
	var marker, label bool
	for _, layer := range result.Plan.Layers {
		switch layer.ID {
		case "shape-map:map_pin:rome":
			marker = true
			if layer.Shape == nil || layer.Shape.Stroke == nil || layer.Shape.Stroke.Color != "#1F252A" {
				t.Fatalf("pin ring must carry the contract colour: %+v", layer.Shape)
			}
			fill, ok := layer.Shape.Fill.([]float64)
			if !ok || len(fill) != 4 || fill[0] != 1 || fill[1] != 1 || fill[2] != 1 {
				t.Fatalf("pin core must be light: %+v", layer.Shape.Fill)
			}
		case "shape-map:map_pin_label:rome":
			label = true
			if layer.Style == nil || layer.Style.Stroke == nil || layer.Style.Stroke.Color != "#FFFFFF" {
				t.Fatalf("place name must carry a light halo: %+v", layer.Style)
			}
			if layer.Style.MinFontSize != layer.Style.MaxFontSize && layer.Size[1] < float64(layer.Style.MinFontSize)*3 {
				t.Fatalf("label fit box too small for the authored size: size=%v min=%v", layer.Size, layer.Style.MinFontSize)
			}
		}
	}
	if !marker || !label {
		t.Fatalf("pin lowered without marker/label: marker=%v label=%v", marker, label)
	}
}
