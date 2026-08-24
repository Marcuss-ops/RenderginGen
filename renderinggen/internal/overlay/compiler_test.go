package overlay

import (
	"encoding/json"
	"testing"
)

// TestCompileIfSemanticPassthroughConcretePlan pins the execution-worker
// boundary: a concrete chronon.render-plan.v1 document is passed through
// byte-for-byte, with no assets synthesized and no layer re-derivation.
func TestCompileIfSemanticPassthroughConcretePlan(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan","version":1,"job_id":"j","canvas":{"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_frames":150},"layers":[{"id":"bg","type":"image","asset":"assets/background.jpg","start_frame":0,"duration_frames":150}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
	compiled, assets, semantic, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatalf("concrete plan must pass through: %v", err)
	}
	if semantic {
		t.Fatalf("concrete plan must not be flagged semantic")
	}
	if len(assets) != 0 {
		t.Fatalf("concrete plan must synthesize no assets, got %+v", assets)
	}
	if string(compiled) != string(raw) {
		t.Fatalf("concrete plan was mutated:\n got %s\nwant %s", compiled, raw)
	}
}

func TestCompileIfSemanticOptionalBackground(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "background":{"kind":"color","color":[0,0,0,1]},
      "items":[{"id":"phrase","template_id":"IMPORTANT_PHRASE","preset_id":"clean_slide_up","text":"hi","start_ms":0,"end_ms":1000}]
    }`)
	compiled, _, semantic, err := CompileIfSemantic(raw)
	if err != nil || !semantic {
		t.Fatalf("compile semantic background: semantic=%v err=%v", semantic, err)
	}
	var plan concretePlan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatalf("decode compiled plan: %v", err)
	}
	if len(plan.Layers) < 2 || plan.Layers[0].ID != "background" || plan.Layers[0].Type != "color" {
		t.Fatalf("background was not emitted first: %+v", plan.Layers)
	}
	if plan.Layers[0].DurationFrames != plan.Canvas.DurationFrames {
		t.Fatalf("background duration=%d, canvas duration=%d", plan.Layers[0].DurationFrames, plan.Canvas.DurationFrames)
	}
}

// TestCompileIfSemanticRejectsMissingPresetID pins ADR-029 forward-point (d):
// RenderingGen no longer re-maps a template_id to a preset (it must not know
// that IMPORTANT_PHRASE means caption_card). A preset-driven template without
// a preset_id is rejected — the semantic_role → preset decision lives only in
// PipelineGen's SemanticOverlayResolver.
func TestCompileIfSemanticRejectsMissingPresetID(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase","template_id":"IMPORTANT_PHRASE","text":"hi","start_ms":0,"end_ms":1000},
        {"id":"word","template_id":"IMPORTANT_WORD","text":"APPLE","start_ms":1000,"end_ms":2000}
      ]
    }`)
	if _, _, _, err := CompileIfSemantic(raw); err == nil {
		t.Fatal("preset-driven template without preset_id must be rejected (no template→preset mirror)")
	}
}

// TestCompileIfSemanticPresetLessPrimitiveCompilesBare pins that preset-less
// primitives (PRODUCT / LOGO) do NOT require a preset_id: they compile to a
// bare layer whose appearance is the renderer's default. This is the other
// half of ADR-029 (d) — RenderingGen only enforces the preset for preset-driven
// templates, never invents one for a primitive.
func TestCompileIfSemanticPresetLessPrimitiveCompilesBare(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"product","template_id":"PRODUCT","start_ms":0,"end_ms":1000,
         "asset_refs":[{"asset_id":"prod","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://store.example/objects/prod.png","media_type":"image/png"}]}
      ]
    }`)
	compiled, _, semantic, err := CompileIfSemantic(raw)
	if err != nil || !semantic {
		t.Fatalf("preset-less primitive must compile: semantic=%v err=%v", semantic, err)
	}
	var plan Plan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Layers) != 1 || plan.Layers[0].Preset != "" {
		t.Fatalf("preset-less primitive must compile to a bare layer: %+v", plan.Layers)
	}
}

// TestCompileIfSemanticRejectsUnknownSchema pins the fail-closed path for a
// plan that is neither concrete nor the known semantic contract.
func TestCompileIfSemanticRejectsUnknownSchema(t *testing.T) {
	raw := []byte(`{"schema_version":"something.else.v9","plan_id":"p"}`)
	_, _, _, err := CompileIfSemantic(raw)
	if err == nil {
		t.Fatal("unknown plan schema must be rejected")
	}
}

// TestCompileIfSemanticMalformedJSON pins graceful decoding failure.
func TestCompileIfSemanticMalformedJSON(t *testing.T) {
	if _, _, _, err := CompileIfSemantic([]byte(`{not-json`)); err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
}

// TestCompileIfSemanticNewContractPresetIDAndEntityRef pins the extended
// contract: the plan's preset_id slot is compiled through the SAME single
// compileSemantic path (no new renderer), and the entity_ref block supplies
// the display text (surface_text → name) when the item carries no explicit
// text.
func TestCompileIfSemanticNewContractPresetIDAndEntityRef(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase","template_id":"IMPORTANT_PHRASE","preset_id":"caption_card","text":"QUESTO CAMBIA TUTTO","start_ms":0,"end_ms":1000},
        {"id":"person","template_id":"PERSON","preset_id":"lower_third_safe","entity_ref":{"entity_id":"ent_tim_cook","type":"PERSON","name":"Tim Cook","surface_text":"Cook"},"start_ms":1000,"end_ms":2000}
      ]
    }`)
	compiled, _, semantic, err := CompileIfSemantic(raw)
	if err != nil || !semantic {
		t.Fatalf("semantic plan must compile: semantic=%v err=%v", semantic, err)
	}
	var plan Plan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Layers) != 2 {
		t.Fatalf("compiled layers = %+v", plan.Layers)
	}
	// preset_id contract slot is used verbatim (no template re-mapping).
	if plan.Layers[0].Preset != "caption_card" {
		t.Fatalf("preset_id = %q, want caption_card", plan.Layers[0].Preset)
	}
	// The PERSON item with no explicit text falls back to entity_ref:
	// surface_text first, then name.
	if plan.Layers[1].Text != "Cook" {
		t.Fatalf("entity_ref surface_text = %q, want Cook", plan.Layers[1].Text)
	}
	if plan.Layers[1].Preset != "lower_third_safe" {
		t.Fatalf("PERSON template preset = %q, want lower_third_safe", plan.Layers[1].Preset)
	}
}

// TestCompileIfSemanticEntityRefNameFallback pins the entity_ref `name`
// fallback (new contract) when surface_text is absent.
func TestCompileIfSemanticEntityRefNameFallback(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"person","template_id":"PERSON","preset_id":"lower_third_safe","entity_ref":{"entity_id":"ent_tim_cook","type":"PERSON","name":"Tim Cook"},"start_ms":0,"end_ms":1000}
      ]
    }`)
	compiled, _, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	var plan Plan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Layers) != 1 || plan.Layers[0].Text != "Tim Cook" {
		t.Fatalf("entity_ref name fallback failed: %+v", plan.Layers)
	}
}

// TestCompileIfSemanticLegacyCanonicalNameFallback pins backward
// compatibility: legacy plans whose entity_ref spells canonical_name keep
// compiling through the same path.
func TestCompileIfSemanticLegacyCanonicalNameFallback(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"person","template_id":"PERSON","preset_id":"lower_third_safe","entity_ref":{"entity_id":"ent_tim_cook","type":"PERSON","canonical_name":"Tim Cook"},"start_ms":0,"end_ms":1000}
      ]
    }`)
	compiled, _, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	var plan Plan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Layers) != 1 || plan.Layers[0].Text != "Tim Cook" {
		t.Fatalf("legacy canonical_name fallback failed: %+v", plan.Layers)
	}
}

// TestCompileIfSemanticTransportsChrononPresetID ensures RenderingGen does
// not mirror Chronon's preset registry. Chronon remains responsible for the
// authoritative lookup during plan compilation.
func TestCompileIfSemanticTransportsChrononPresetID(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_focus_v1","text":"x","start_ms":0,"end_ms":1000}
      ]
    }`)
	if _, _, _, err := CompileIfSemantic(raw); err != nil {
		t.Fatalf("non-empty Chronon preset id must be transported: %v", err)
	}
}

// TestCompileIfSemanticConcretePlanDecodes keeps the Plan struct in lockstep
// with the concrete render-plan shape the worker passes through, so a real
// golden document always decodes without error.
func TestCompileIfSemanticConcretePlanDecodes(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan","version":1,"job_id":"j","canvas":{"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_frames":150},"layers":[{"id":"p","type":"text","text":"X","preset":"caption_card","start_frame":20,"duration_frames":41,"animation":{"preset":"fade_in"}}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
	compiled, _, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	var plan Plan
	if err := json.Unmarshal(compiled, &plan); err != nil {
		t.Fatalf("concrete plan must decode: %v", err)
	}
	if plan.Schema != "chronon.render-plan" || len(plan.Layers) != 1 || plan.Layers[0].Preset != "caption_card" {
		t.Fatalf("decoded plan = %+v", plan)
	}
}
