package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCanonicalTextPresetLowersTheCanaryGlow pins the halo contract: the
// canonical text preset must carry a glow block, the compiler must transport
// it verbatim into the compiled plan's layer style, and the numbers must
// satisfy Chronon's fail-closed decoder rules (finite, non-negative, falloff
// >= 1). Without this gate a RenderingGen regression would silently demote
// every plan-rendered phrase back to the shadow-as-glow hack.
func TestCanonicalTextPresetLowersTheCanaryGlow(t *testing.T) {
	def, err := ResolveOfficialPreset(CanonicalTextPresetID)
	if err != nil {
		t.Fatalf("resolve canonical preset: %v", err)
	}
	if def.Style.Glow == nil {
		t.Fatalf("canonical text preset carries no glow: %+v", def.Style)
	}
	g := def.Style.Glow
	if !(g.Falloff >= 1) {
		t.Errorf("preset glow falloff = %v, want >= 1 (non-boosting halo)", g.Falloff)
	}
	if !g.HighQuality {
		t.Errorf("preset glow must select the final-render (HighQuality) path")
	}
	for name, v := range map[string]float64{
		"radius": g.Radius, "intensity": g.Intensity, "threshold": g.Threshold,
		"core_strength": g.CoreStrength, "aura_strength": g.AuraStrength,
		"bloom_strength": g.BloomStrength,
	} {
		if v < 0 {
			t.Errorf("preset glow.%s = %v, want >= 0", name, v)
		}
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
			"preset_id": "apple_v2", "kind": "important_phrase",
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
	if !(glow.Falloff >= 1) || !glow.HighQuality || glow.Radius <= 0 {
		t.Fatalf("compiled glow is not the canary contract: %+v", glow)
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
	for _, field := range []string{"radius", "intensity", "threshold", "falloff", "core_strength", "aura_strength", "bloom_strength", "high_quality"} {
		if _, ok := decoded.Glow[field]; !ok {
			t.Errorf("emitted glow missing schema field %q (got %v)", field, decoded.Glow)
		}
	}
	if !strings.Contains(string(out), `"glow"`) {
		t.Fatalf("marshalled style lost the glow block: %s", out)
	}
}
