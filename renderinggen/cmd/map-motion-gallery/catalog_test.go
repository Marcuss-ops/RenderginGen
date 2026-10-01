package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMapMotionCatalogContainsAll45UniqueIDs(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "ChrononTemplate", "catalog", "map_motion_v1.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var catalog gallery
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Schema != "chronontemplate.map-motion-family.v1" || catalog.Version != 1 || catalog.CatalogID != "map_motion_v1" {
		t.Fatalf("unexpected catalog identity: %+v", catalog)
	}
	want := map[string]int{
		"map_basic_v1":          10,
		"map_routes_v1":         5,
		"map_multi_location_v1": 10,
		"map_data_v1":           5,
		"map_satellite_v1":      5,
		"map_terrain_v1":        5,
		"map_historical_v1":     5,
	}
	if len(catalog.Families) != len(want) {
		t.Fatalf("family count=%d, want %d", len(catalog.Families), len(want))
	}
	seenFamilies := make(map[string]bool)
	seenIDs := make(map[string]bool)
	total := 0
	for _, family := range catalog.Families {
		count, ok := want[family.ID]
		if !ok || seenFamilies[family.ID] {
			t.Fatalf("unexpected or duplicate family %q", family.ID)
		}
		seenFamilies[family.ID] = true
		if len(family.Items) != count {
			t.Fatalf("family %s has %d items, want %d", family.ID, len(family.Items), count)
		}
		for _, item := range family.Items {
			if item.ID == "" || seenIDs[item.ID] {
				t.Fatalf("empty or duplicate motion ID %q", item.ID)
			}
			if item.Title == "" || item.Source != "natural_earth" && item.Source != "nasa" {
				t.Fatalf("motion %s has incomplete title/source: %+v", item.ID, item)
			}
			seenIDs[item.ID] = true
			total++
		}
	}
	if total != 45 {
		t.Fatalf("catalog has %d motions, want 45", total)
	}
}

func TestEveryCatalogMotionBuildsAVisibleMapPlan(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	previousRoot := repoRoot
	repoRoot = root
	t.Cleanup(func() { repoRoot = previousRoot })

	catalogBytes, err := os.ReadFile(filepath.Join(root, "ChrononTemplate", "catalog", "map_motion_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog gallery
	if err := json.Unmarshal(catalogBytes, &catalog); err != nil {
		t.Fatal(err)
	}
	assetDir := filepath.Join(t.TempDir(), "assets", "maps")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outputRoot := filepath.Dir(filepath.Dir(assetDir))
	compiled := 0
	for _, family := range catalog.Families {
		for _, item := range family.Items {
			t.Run(item.ID, func(t *testing.T) {
				semantic, plan, assets, err := buildPlan(catalog, item, outputRoot, assetDir)
				if err != nil {
					t.Fatalf("buildPlan failed: %v", err)
				}
				if len(plan.Layers) < 5 {
					t.Fatalf("only %d render layers; expected basemap, boundaries and editorial overlays", len(plan.Layers))
				}
				if plan.Canvas.Width != canvasWidth || plan.Canvas.Height != canvasHeight || plan.Canvas.DurationFrames != durationFrames {
					t.Fatalf("render canvas/duration = %+v, want %dx%d for %d frames", plan.Canvas, canvasWidth, canvasHeight, durationFrames)
				}
				if len(assets) != 3 {
					t.Fatalf("staged asset count = %d, want basemap, boundaries and bundled font", len(assets))
				}
				if len(semantic) == 0 || !json.Valid(semantic) {
					t.Fatal("semantic JSON plan is empty or invalid")
				}
				var document map[string]any
				if err := json.Unmarshal(semantic, &document); err != nil {
					t.Fatal(err)
				}
				items, ok := document["items"].([]any)
				if !ok || len(items) < 5 {
					t.Fatalf("semantic scene has %d items, want at least 5", len(items))
				}
				wire, err := plan.Marshal()
				if err != nil {
					t.Fatalf("Chronon plan serialization failed: %v", err)
				}
				if !json.Valid(wire) {
					t.Fatal("Chronon render plan is invalid JSON")
				}
				compiled++
			})
		}
	}
	if compiled != 45 {
		t.Fatalf("compiled %d gallery motions, want 45", compiled)
	}
}
