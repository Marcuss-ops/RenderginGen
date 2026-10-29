package overlay

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestChrononPluginMapsCatalogOwnsTheMapVocabulary pins the consequence of the
// ChrononPlugin-owned catalog: the native documentary compositions are admitted
// and the retired Python composition families are not.
func TestChrononPluginMapsCatalogOwnsTheMapVocabulary(t *testing.T) {
	for _, id := range []string{
		"route_line",
		"multi_leg_flight",
		"territory_glow_reveal",
		"area_glow_pulse",
	} {
		if !certifiedMapCompositions[id] {
			t.Errorf("ChrononPlugin map composition %q is not admitted", id)
		}
	}
	for _, retired := range []string{
		"region_zoom", "pulse_marker", "spotlight",
		"ed_saudi_base", "ed_iran_glow", "ed_yemen_fill",
		"satellite_dive_lock", "crime_scene_radar", "territory_polygon_wipe",
		"cell_tower_triangulation", "thermal_heatmap_overlay",
		"night_vision_infrared", "missile_arc_ballistic",
		"great_circle_polar_route", "cinematic_anchor_pitch_tilt",
	} {
		if certifiedMapCompositions[retired] {
			t.Errorf("retired Python map composition %q is still admitted", retired)
		}
	}
	if !certifiedMapCameraAnimations["slow_approach"] || !certifiedMapCameraAnimations["exponential_dive"] {
		t.Fatalf("camera animations are not the ChrononPlugin set: %v", certifiedMapCameraAnimations)
	}
	if len(certifiedMapLabelAnimations) != 1 || !certifiedMapLabelAnimations["none"] {
		t.Fatalf("label animations are not the ChrononPlugin set: %v", certifiedMapLabelAnimations)
	}
	if chrononPluginMaps.BaseFlyover != "" {
		t.Fatalf("base flyover must be the empty composition id, got %q", chrononPluginMaps.BaseFlyover)
	}
}

// TestChrononPluginMapsCatalogMatchesSource fails when the embedded projection
// drifts from the catalog published by ChrononPlugin. The sibling checkout is
// absent in RenderingGen-only CI, so that case skips instead of failing.
func TestChrononPluginMapsCatalogMatchesSource(t *testing.T) {
	source, ok := chrononPluginMapsCatalogSourcePath()
	if !ok {
		t.Skip("ChrononPlugin checkout is not available; embedded catalog drift cannot be checked here")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read ChrononPlugin map catalog: %v", err)
	}
	fromSource, err := loadChrononPluginMapsCatalog(raw)
	if err != nil {
		t.Fatalf("parse ChrononPlugin map catalog %s: %v", source, err)
	}
	if !reflect.DeepEqual(chrononPluginMaps, fromSource) {
		t.Fatalf("embedded map catalog is stale: copy %s into renderinggen/internal/overlay/catalog/%s",
			source, chrononPluginMapsCatalogFileName)
	}
}

func chrononPluginMapsCatalogSourcePath() (string, bool) {
	for _, candidate := range []string{
		filepath.Join("..", "..", "..", "..", "ChrononPlugin", "plugins", "maps", "maps-renderer-catalog.v1.json"),
		filepath.Join("..", "..", "..", "ChrononPlugin", "plugins", "maps", "maps-renderer-catalog.v1.json"),
		filepath.Join("ChrononPlugin", "plugins", "maps", "maps-renderer-catalog.v1.json"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}
