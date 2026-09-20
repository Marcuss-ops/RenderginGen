package motion

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEveryPresetPublishesTheNonBoostingGlowPolicy pins what the emitted catalog
// carries for the composition presets. ChrononTemplate is the authority for the
// halo policy; this side only has to notice if it stops shipping it, because a
// boosted halo is a visual regression no id can reveal.
func TestEveryPresetPublishesTheNonBoostingGlowPolicy(t *testing.T) {
	catalog, err := Canonical()
	if err != nil {
		t.Fatalf("canonical catalog: %v", err)
	}
	if len(catalog.Final3DPresets) == 0 {
		t.Fatal("canonical catalog lists no composition presets")
	}
	for _, preset := range catalog.Final3DPresets {
		if !(preset.Glow.Falloff >= 1) {
			t.Errorf("%s: glow falloff = %v, want >= 1 (non-boosting)", preset.ID, preset.Glow.Falloff)
		}
		if !preset.Glow.HighQuality {
			t.Errorf("%s: glow declares the preview path, want the final-render path", preset.ID)
		}
		switch preset.Material.Kind {
		case "unlit", "lambert":
			// No halo to activate.
		case "emissive":
			if !(preset.Material.EmissiveStrength > 0) {
				t.Errorf("%s: emissive material with strength %v has no active halo",
					preset.ID, preset.Material.EmissiveStrength)
			}
		default:
			t.Errorf("%s: material kind = %q, want unlit, lambert or emissive",
				preset.ID, preset.Material.Kind)
		}
	}

	glow, ok := PresetGlow("typewriter_3d_glow")
	if !ok {
		t.Fatal("typewriter_3d_glow is missing from the canonical catalog")
	}
	if !(glow.Falloff >= 1) || !glow.HighQuality {
		t.Errorf("typewriter_3d_glow glow = %+v, want the corrected non-boosting high-quality policy", glow)
	}
	if _, ok := PresetGlow("no_such_preset"); ok {
		t.Error("an unknown preset must not report a glow policy")
	}
}

// mutatedCatalog rewrites the embedded artifact in memory. It is how the gate
// below gets a broken input to reject: a validation that only ever sees the
// healthy document is not evidence that it rejects anything.
func mutatedCatalog(t *testing.T, mutate func(preset map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(canonicalCatalogJSON, &document); err != nil {
		t.Fatalf("decode embedded catalog: %v", err)
	}
	presets, ok := document["final3d_presets"].([]any)
	if !ok || len(presets) == 0 {
		t.Fatal("embedded catalog has no final3d_presets")
	}
	for _, entry := range presets {
		preset, ok := entry.(map[string]any)
		if !ok {
			t.Fatal("embedded catalog has a preset row that is not an object")
		}
		mutate(preset)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode mutated catalog: %v", err)
	}
	return raw
}

// TestGlowContractGateRejectsADegradedPolicy proves the gate is non-vacuous: the
// untouched document parses, and every single-field degradation of the glow
// contract is rejected. A boosting falloff is the exact regression the policy
// exists to prevent, so it must never reach a render through this path.
func TestGlowContractGateRejectsADegradedPolicy(t *testing.T) {
	if _, err := parseCanonical(canonicalCatalogJSON); err != nil {
		t.Fatalf("the embedded catalog must parse: %v", err)
	}

	// Control: the mutation machinery itself produces a document that parses,
	// so a failure below is the gate and not a broken fixture.
	if _, err := parseCanonical(mutatedCatalog(t, func(map[string]any) {})); err != nil {
		t.Fatalf("an unmutated rewrite must still parse: %v", err)
	}

	cases := []struct {
		name   string
		want   string
		mutate func(preset map[string]any)
	}{
		{
			name: "boosting falloff",
			want: "boosting or non-finite glow falloff",
			mutate: func(preset map[string]any) {
				preset["glow"].(map[string]any)["falloff"] = 0.5
			},
		},
		{
			// A row emitted without the policy decodes to a falloff of zero,
			// which is the boosting case: absence cannot pass as agreement.
			name: "absent glow policy",
			want: "boosting or non-finite glow falloff",
			mutate: func(preset map[string]any) {
				delete(preset, "glow")
			},
		},
		{
			name: "preview render path",
			want: "interactive preview glow path",
			mutate: func(preset map[string]any) {
				preset["glow"].(map[string]any)["high_quality"] = false
			},
		},
		{
			name: "unknown material kind",
			want: "unknown material kind",
			mutate: func(preset map[string]any) {
				preset["material"].(map[string]any)["kind"] = "phong"
			},
		},
		{
			name: "emissive material with an inactive halo",
			want: "emissive_strength",
			mutate: func(preset map[string]any) {
				preset["material"].(map[string]any)["kind"] = "emissive"
				preset["material"].(map[string]any)["emissive_strength"] = 0.0
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCanonical(mutatedCatalog(t, tc.mutate))
			if err == nil {
				t.Fatalf("a catalog with a %s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}
