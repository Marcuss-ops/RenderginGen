package overlay

import (
	"encoding/json"
	"os"
	"testing"
)

// TestZZExportChrononMapsPlan writes native chronon render plans for the
// ModernMap1 preview set. Skipped unless MAPS_PLAN_OUT_DIR is set.
func TestZZExportChrononMapsPlan(t *testing.T) {
	outDir := os.Getenv("MAPS_PLAN_OUT_DIR")
	if outDir == "" {
		t.Skip("MAPS_PLAN_OUT_DIR not set")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	build := func(place string, pins []any, routes []any) []byte {
		item := map[string]any{
			"id": "map-" + place, "kind": "map", "template_id": "MAP",
			"start_ms": 0, "end_ms": 8000,
			"params": map[string]any{
				"map_type": MapTypeModernMap1, "map_place": place,
			},
			"map": map[string]any{
				"provider": "local", "source_id": "natural-earth", "source_license": "public-domain",
				"center":      map[string]any{"latitude": 0.0, "longitude": 0.0},
				"zoom":        0, "width": 1920, "height": 1080,
				"attribution": "Natural Earth", "pins": pins, "routes": routes,
			},
		}
		plan := map[string]any{
			"schema_version": "renderinggen.overlay-plan.v1",
			"plan_id":        "modernmap1-" + place, "video_id": "modernmap1-" + place,
			"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
			"background": map[string]any{"kind": "color", "color": []float64{0.03, 0.05, 0.08, 1}},
			"items":      []any{item},
		}
		raw, err := json.Marshal(plan)
		if err != nil {
			t.Fatalf("marshal plan: %v", err)
		}
		return raw
	}
	rome := map[string]any{"id": "rome", "label": "Rome", "latitude": 41.9028, "longitude": 12.4964, "color": "#1F252A", "radius_px": 22.0}
	milan := map[string]any{"id": "milan", "label": "Milan", "latitude": 45.4642, "longitude": 9.19, "color": "#1F252A", "radius_px": 22.0}
	cases := map[string][]byte{
		"italy_pin": build("Italy", []any{rome}, nil),
		"italy_route": build("Italy", []any{rome, milan}, []any{
			map[string]any{
				"id": "rome-milan", "color": "#F8F5EA", "width_px": 7.0,
				"stops": []any{
					map[string]any{"latitude": 41.9028, "longitude": 12.4964},
					map[string]any{"latitude": 45.4642, "longitude": 9.19},
				},
			},
		}),
		"japan_pin": build("Japan", []any{
			map[string]any{"id": "tokyo", "label": "Tokyo", "latitude": 35.6895, "longitude": 139.6917, "color": "#1F252A", "radius_px": 22.0},
		}, nil),
	}
	for name, raw := range cases {
		result, err := CompileSemantic(raw)
		if err != nil {
			t.Fatalf("%s: compile: %v", name, err)
		}
		wire, err := result.Plan.Marshal()
		if err != nil {
			t.Fatalf("%s: marshal chronon plan: %v", name, err)
		}
		if err := os.WriteFile(outDir+"/"+name+".chronon.json", wire, 0o644); err != nil {
			t.Fatalf("%s: write: %v", name, err)
		}
		t.Logf("%s: %d layers", name, len(result.Plan.Layers))
	}
}
