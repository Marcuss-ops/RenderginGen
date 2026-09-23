package motion

import "testing"

// TestEveryPresetPublishesMaterialContract checks the preset catalog without
// duplicating glow-quality policy outside Chronon3D.
func TestEveryPresetPublishesMaterialContract(t *testing.T) {
	catalog, err := Canonical()
	if err != nil {
		t.Fatalf("canonical catalog: %v", err)
	}
	if len(catalog.Final3DPresets) == 0 {
		t.Fatal("canonical catalog lists no composition presets")
	}
	for _, preset := range catalog.Final3DPresets {
		if err := validatePresetMaterial(preset); err != nil {
			t.Error(err)
		}
	}
}


