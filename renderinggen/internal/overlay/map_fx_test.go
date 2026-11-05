package overlay

import (
	"strings"
	"testing"
)

func mapFxPlan(fx string, extraPins int) map[string]any {
	plan := georeferencedMapPlan(0, 0)
	item := plan["items"].([]any)[0].(map[string]any)
	item["params"] = map[string]any{"map_fx": fx}
	if extraPins == 0 {
		return plan
	}
	mapBlock := item["map"].(map[string]any)
	pins := mapBlock["pins"].([]any)
	offsets := []float64{0.05, -0.05, 0.10, -0.10, 0.15, -0.15, 0.20}
	for index := 0; index < extraPins && index < len(offsets); index++ {
		pins = append(pins, map[string]any{
			"id": "target_" + string(rune('b'+index)), "label": "Target",
			"latitude": 0.0, "longitude": offsets[index],
			"color": "#E11D48", "radius_px": 12.0,
		})
	}
	mapBlock["pins"] = pins
	return plan
}

func TestMapFxLowersOnlyPrimitiveLayers(t *testing.T) {
	cases := []struct {
		id              string
		extraPins       int
		wantLayer       string
		wantShape       string
		wantTrimmedPath bool
	}{
		{id: "area_glow_pulse", wantLayer: ":map_fx:area_ring_0", wantShape: "ellipse"},
		{id: "radar_location_reveal", wantLayer: ":map_fx:radar_crosshair_x", wantShape: "rectangle"},
		{id: "light_beam_drop", wantLayer: ":map_fx:energy_beam", wantShape: "rectangle"},
		{id: "city_network_expansion", extraPins: 2, wantLayer: ":map_fx:network_link_0",
			wantShape: "path", wantTrimmedPath: true},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			result, err := compileMapTestPlan(t, mapFxPlan(tc.id, tc.extraPins))
			if err != nil {
				t.Fatalf("compile %s: %v", tc.id, err)
			}
			found := false
			for _, layer := range result.Plan.Layers {
				if !strings.Contains(layer.ID, ":map_fx:") {
					continue
				}
				if layer.Type != "shape" || layer.Shape == nil {
					t.Fatalf("%s layer %q is not a primitive shape: %+v", tc.id, layer.ID, layer)
				}
				if layer.DurationFrames <= 0 || layer.DurationFrames > 90 {
					t.Fatalf("%s layer %q duration %d outside the 90-frame map", tc.id, layer.ID, layer.DurationFrames)
				}
				if strings.Contains(layer.ID, tc.wantLayer) {
					found = true
					if layer.Shape.Type != tc.wantShape {
						t.Fatalf("%s layer %q shape = %q, want %q", tc.id, layer.ID, layer.Shape.Type, tc.wantShape)
					}
					if tc.wantTrimmedPath && len(layer.Shape.Operators) == 0 {
						t.Fatalf("%s link has no trim operator", tc.id)
					}
				}
			}
			if !found {
				t.Fatalf("%s did not emit the expected layer containing %q", tc.id, tc.wantLayer)
			}
		})
	}
}

func TestMapFxRejectsUnknownAndUnsupportedPinCounts(t *testing.T) {
	if _, err := compileMapTestPlan(t, mapFxPlan("not_a_map_fx", 0)); err == nil ||
		!strings.Contains(err.Error(), "unsupported map_fx") {
		t.Fatalf("unknown map_fx error = %v, want unsupported map_fx", err)
	}
	if _, err := compileMapTestPlan(t, mapFxPlan("city_network_expansion", 0)); err == nil ||
		!strings.Contains(err.Error(), "requires 2..8 pins") {
		t.Fatalf("single-pin network error = %v, want a 2..8 pin error", err)
	}
	if _, err := compileMapTestPlan(t, mapFxPlan("area_glow_pulse", 1)); err == nil ||
		!strings.Contains(err.Error(), "exactly one pin") {
		t.Fatalf("two-pin area error = %v, want an exactly-one-pin error", err)
	}
}
