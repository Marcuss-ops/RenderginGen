package overlay

import (
	"encoding/json"
	"testing"
)

// TestCompileIfSemanticPassthroughConcretePlan pins the execution-worker
// boundary: a concrete chronon.render-plan.v1 document is passed through
// byte-for-byte, with no assets synthesized and no layer re-derivation.
func TestCompileIfSemanticPassthroughConcretePlan(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan","version":1,"job_id":"j","canvas":{"width":1280,"height":720,"fps":30,"duration_frames":150},"layers":[{"id":"bg","type":"image","asset":"assets/background.jpg","start_frame":0,"duration_frames":150}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
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

// TestCompileIfSemanticRejectsSemanticPlan pins the dumb-worker contract:
// PipelineGen owns the semantic_role → preset compile (SemanticOverlayResolver),
// so a semantic overlay-plan.v1 reaching the worker is rejected, never silently
// re-compiled into a second Chronon. This also guarantees no global contrast
// veil (or any layer) can be injected here, because the worker never builds
// layers.
func TestCompileIfSemanticRejectsSemanticPlan(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p","video_id":"v","width":1280,"height":720,"fps":30,
      "items":[{"id":"phrase","template_id":"IMPORTANT_PHRASE","text":"hi","start_ms":0,"end_ms":1000}]
    }`)
	_, _, _, err := CompileIfSemantic(raw)
	if err == nil {
		t.Fatal("semantic plan must be rejected (compile is PipelineGen's job)")
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

// TestCompileIfSemanticConcretePlanDecodes keeps the Plan struct in lockstep
// with the concrete render-plan shape the worker passes through, so a real
// golden document always decodes without error.
func TestCompileIfSemanticConcretePlanDecodes(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan","version":1,"job_id":"j","canvas":{"width":1280,"height":720,"fps":30,"duration_frames":150},"layers":[{"id":"p","type":"text","text":"X","preset":"caption_card","start_frame":20,"duration_frames":41,"animation":{"preset":"fade_in"}}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
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
