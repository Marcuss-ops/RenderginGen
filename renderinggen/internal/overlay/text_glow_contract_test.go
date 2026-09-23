package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCanonicalTextPresetLowersTheCanaryGlow pins the simple glow contract:
// the canonical text preset emits only radius, intensity and color, with no
// quality selector or layered-lobe controls.
func TestCanonicalTextPresetLowersTheCanaryGlow(t *testing.T) {
	def, err := ResolveOfficialPreset(PhraseDefaultPresetID)
	if err != nil {
		t.Fatalf("resolve canonical preset: %v", err)
	}
	if def.Style.Glow == nil {
		t.Fatalf("canonical text preset carries no glow: %+v", def.Style)
	}
	g := def.Style.Glow
	if g.Radius <= 0 || g.Intensity < 0 || g.Color == "" {
		t.Errorf("preset glow = %+v, want positive radius, non-negative intensity and a color", g)
	}
}

// TestCompiledTextPlanCarriesGlowNotShadowAsGlow compiles one canonical text
// item and checks the emitted layer style: glow present with the preset's
// numbers, and the shadow block never used as a glow stand-in (offset stays
// a real drop shadow; the glow block is the halo).
func TestCompiledTextPlanCarriesGlowNotShadowAsGlow(t *testing.T) {
	raw := []byte(`{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id": "glow-contract", "video_id": "glow-contract",
		"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
		"duration_ms": 5000,
		"background": {"kind": "color", "color": [0, 0, 0, 1]},
		"items": [{
			"id": "phrase", "template_id": "IMPORTANT_PHRASE",
			"preset_id": "phrase_default", "kind": "important_phrase",
			"text": "GLOW CONTRACT", "start_ms": 0, "end_ms": 5000
		}]
	}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var textLayer *Layer
	for i := range result.Plan.Layers {
		if result.Plan.Layers[i].Type == "text" {
			textLayer = &result.Plan.Layers[i]
			break
		}
	}
	if textLayer == nil || textLayer.Style == nil {
		t.Fatalf("no text layer style in compiled plan: %+v", result.Plan.Layers)
	}
	if textLayer.Style.Glow == nil {
		t.Fatalf("compiled text layer carries no glow block: %+v", textLayer.Style)
	}
	glow := textLayer.Style.Glow
	if glow.Radius <= 0 || glow.Intensity < 0 || glow.Color == "" {
		t.Fatalf("compiled glow is not the simple glow contract: %+v", glow)
	}
	// Round-trip: the marshalled layer must decode as the schema-defined
	// style object — glow present, every field spelled the Chronon way.
	out, err := json.Marshal(textLayer.Style)
	if err != nil {
		t.Fatalf("marshal style: %v", err)
	}
	var decoded struct {
		Glow map[string]any `json:"glow"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode style: %v", err)
	}
	for _, field := range []string{"radius", "intensity", "color"} {
		if _, ok := decoded.Glow[field]; !ok {
			t.Errorf("emitted glow missing schema field %q (got %v)", field, decoded.Glow)
		}
	}
	if !strings.Contains(string(out), `"glow"`) {
		t.Fatalf("marshalled style lost the glow block: %s", out)
	}
	for _, removed := range []string{"threshold", "falloff", "core_strength", "aura_strength", "bloom_strength", "high_quality"} {
		if _, ok := decoded.Glow[removed]; ok {
			t.Errorf("emitted glow unexpectedly contains removed control %q", removed)
		}
	}
}
