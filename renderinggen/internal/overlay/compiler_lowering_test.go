package overlay

import (
	"encoding/json"
	"testing"
)

// TestCompileIfSemanticUntypedConcretePlanIsRejected keeps the Plan struct in
// lockstep with the concrete render-plan shape, so a real golden document
// always decodes without error. The untyped document itself is rejected by
// CompileIfSemantic (fail-closed); decoding is exercised directly here.
func TestCompileIfSemanticUntypedConcretePlanIsRejected(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan.v2","version":2,"job_id":"j","canvas":{"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_frames":150},"layers":[{"id":"p","type":"text","text":"X","preset":"caption_card","start_frame":20,"duration_frames":41,"animation":{"preset":"fade_in"}}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
	if _, _, _, err := CompileIfSemantic(raw); err == nil {
		t.Fatal("untyped concrete plan must be rejected by CompileIfSemantic")
	}
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatalf("concrete plan must decode: %v", err)
	}
	if plan.Schema != "chronon.render-plan.v2" || len(plan.Layers) != 1 {
		t.Fatalf("decoded plan = %+v", plan)
	}
}

func TestCompileSemanticLowersAuthoringConcepts(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"style_profile":"crime","items":[{"id":"n","kind":"entity_card","template_id":"PERSON","preset_id":"name_glow_slide","text":"Ada","start_ms":0,"end_ms":1000}]}`)
	compiled, _, _, err := CompileIfSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	outBytes, err := compiled.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(outBytes, &out); err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{"preset_id": true, "style_profile": true, "safe_area": true, "lower_third": true, "animation_preset": true, "unit": true, "enter_duration_frames": true, "exit_duration_frames": true}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if forbidden[k] {
					t.Errorf("authoring key %q leaked into Chronon plan", k)
				}
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(out)
	layer := out["layers"].([]any)[0].(map[string]any)
	if _, ok := layer["position"]; !ok {
		t.Fatal("resolved absolute geometry missing")
	}
	if _, ok := layer["style"]; !ok {
		t.Fatal("concrete style missing")
	}
}
