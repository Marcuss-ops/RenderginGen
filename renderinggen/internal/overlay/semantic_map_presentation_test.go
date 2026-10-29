package overlay

import "testing"

// presentationMapItem builds one contract-valid static map item with the
// resolved runtime presentation attached in params.
func presentationMapItem(params map[string]any) semanticItem {
	return semanticItem{
		ID:   "map-1",
		Kind: string(KindMap),
		Map: &SemanticMap{
			Provider:      "local",
			SourceID:      "plate-1",
			SourceLicense: "operator-supplied",
			Attribution:   "© OpenStreetMap contributors",
			Center:        SemanticMapPoint{Latitude: 0, Longitude: 0},
			Zoom:          5,
			Width:         1280,
			Height:        720,
			Pins: []SemanticMapPin{
				{ID: "city:rome", Label: "Rome", Latitude: 0.1, Longitude: 0.1, Color: "#FF3B30", RadiusPX: 10},
				{ID: "city:paris", Label: "Paris", Latitude: -0.1, Longitude: -0.1, Color: "#FF3B30", RadiusPX: 10},
			},
		},
		Assets: []SemanticAssetRef{{ID: "plate-1-asset", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MediaType: "image/png"}},
		Params: params,
	}
}

// TestMapPresentationParamsAreAdmitted proves the worker ACCEPTS the new
// certified map presentation contract on map items.
func TestMapPresentationParamsAreAdmitted(t *testing.T) {
	item := presentationMapItem(map[string]any{
		// ChrononPlugin's native documentary vocabulary is the single source
		// of truth for this field (maps-renderer-catalog.v1.json).
		"map_animation":        "radar_location_reveal",
		"map_camera_animation": "exponential_dive",
		"map_label_animation":  "none",
		"map_backend":          "dynamic",
		"map_group_strategy":   "multi_pin",
		"map_group":            map[string]any{"index": float64(1), "count": float64(2), "size": float64(2)},
		"map_pin_timing": map[string]any{
			"city:rome":  map[string]any{"start_us": float64(0), "duration_us": float64(1_200_000)},
			"city:paris": map[string]any{"start_us": float64(1_500_000), "duration_us": float64(900_000)},
		},
	})
	if err := validateMapContract(item, KindMap, 1280, 720); err != nil {
		t.Fatalf("certified map presentation must be admitted: %v", err)
	}
}

// TestMapPinTimingEscapingWindowFailsClosed proves the shared invariant
// pin.start+duration<=map.duration is enforced at the compile boundary with
// no silent repair (PipelineGen prevents, RenderingGen rejects).
func TestMapPinTimingEscapingWindowFailsClosed(t *testing.T) {
	dur := int64(2_000_000)
	start := int64(0)
	item := presentationMapItem(map[string]any{
		"map_pin_timing": map[string]any{
			"city:rome": map[string]any{"start_us": float64(1_500_000), "duration_us": float64(1_000_000)},
		},
	})
	item.StartUS = &start
	item.DurationUS = &dur
	item.StartMS = 0
	item.EndMS = 2000
	if err := validateMapContract(item, KindMap, 1280, 720); err == nil {
		t.Fatal("pin escaping map window must fail closed")
	}
}

// TestMapPresentationParamsFailClosed proves the worker rejects a map
// presentation it cannot render — an unknown composition, a schedule for an
// undeclared pin, or an incoherent group identity — instead of ignoring it.
func TestMapPresentationParamsFailClosed(t *testing.T) {
	cases := map[string]map[string]any{
		"unknown composition": {"map_animation": "radar_lock"},
		// The retired Python renderer's composition families are not native
		// ChrononPlugin compositions; a stale plan must fail closed by name.
		"retired python composition":    {"map_animation": "region_zoom"},
		"retired editorial composition": {"map_animation": "ed_saudi_base"},
		"unknown camera":                {"map_camera_animation": "hyper_spin"},
		"unknown backend":               {"map_backend": "wgpu"},
		"unknown strategy":              {"map_group_strategy": "yolo"},
		// The Python renderer is retired (TICKET-MAPS-NATIVE-PORT fase 7):
		// a stale plan may not claim it in new work, or it would compile
		// against a renderer that no longer exists.
		"retired python backend": {"map_backend": "python"},
		"timing for undeclared pin": {
			"map_pin_timing": map[string]any{"city:berlin": map[string]any{"start_us": float64(0), "duration_us": float64(1)}},
		},
		"non-positive timing": {
			"map_pin_timing": map[string]any{"city:rome": map[string]any{"start_us": float64(0), "duration_us": float64(0)}},
		},
		"malformed timing": {
			"map_pin_timing": map[string]any{"city:rome": "start now"},
		},
		"incoherent group": {
			"map_group": map[string]any{"index": float64(3), "count": float64(2), "size": float64(2)},
		},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateMapContract(presentationMapItem(params), KindMap, 1280, 720); err == nil {
				t.Fatal("invalid map presentation must fail closed")
			}
		})
	}
}
