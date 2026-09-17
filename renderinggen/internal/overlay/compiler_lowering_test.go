package overlay

import (
	"encoding/json"
	"fmt"
	"testing"
)

// TestCompileSemanticUntypedConcretePlanIsRejected keeps the Plan struct in
// lockstep with the concrete render-plan shape, so a real golden document
// always decodes without error. The untyped document itself is rejected by
// CompileSemantic (fail-closed); decoding is exercised directly here.
func TestCompileSemanticUntypedConcretePlanIsRejected(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan.v2","version":2,"job_id":"j","canvas":{"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_frames":150},"layers":[{"id":"p","type":"text","text":"X","preset":"caption_card","start_frame":20,"duration_frames":41,"animation":{"preset":"fade_in"}}],"output":{"path":"result.mp4","format":"mp4","codec":"h264"}}`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("untyped concrete plan must be rejected by CompileSemantic")
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
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"p","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"style_profile":"crime","items":[{"id":"n","entity_id":"entity:ada","kind":"entity_card","template_id":"PERSON","preset_id":"apple_v2","text":"Ada","start_ms":0,"end_ms":1000,"duration_ms":1000}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatal(err)
	}
	compiled := result.Plan
	outBytes, err := compiled.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(outBytes, &out); err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{"preset_id": true, "style_profile": true, "safe_area": true, "lower_third": true, "animation_preset": true, "enter_duration_frames": true, "exit_duration_frames": true}
	// `unit` is NOT in that blanket set, and the reason is a name collision that
	// made this check report a false positive the moment text animators started
	// surviving the compile (they used to be dropped, so the rule was never
	// exercised):
	//
	//   - AUTHORING unit is the preset/motion authoring concept ("layer" for a
	//     whole-layer image motion, "glyph" for a text motion). Chronon has no
	//     such concept and must never receive it.
	//   - RENDERER unit is `selectors[].unit` in the render-plan v2 contract
	//     (TextSelectorUnit: glyph/character/grapheme/word/line). Chronon's
	//     decoder reads it (render_plan_decoder_layer.cpp: `value.value("unit",
	//     "glyph")`) and glyph_selector_compile.cpp branches on it to decide the
	//     selection granularity — dropping it would silently change how a
	//     word-level reveal animates.
	//
	// So the authoring concept is banned BY POSITION: `unit` may appear only
	// inside a selector, and never with the authoring value.
	const authoringLayerUnit = "layer"
	var walk func(path string, v any, insideSelector bool)
	walk = func(path string, v any, insideSelector bool) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if forbidden[k] {
					t.Errorf("authoring key %q leaked into Chronon plan at %s", k, path)
				}
				if k == "unit" {
					if !insideSelector {
						t.Errorf("authoring key %q leaked into Chronon plan outside a text selector at %s", k, path)
					}
					if value, ok := child.(string); ok && value == authoringLayerUnit {
						t.Errorf("authoring unit %q leaked into a Chronon selector unit at %s", authoringLayerUnit, path)
					}
				}
				walk(path+"."+k, child, insideSelector || k == "selectors")
			}
		case []any:
			for i, child := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), child, insideSelector)
			}
		}
	}
	walk("$", out, false)
	layer := out["layers"].([]any)[0].(map[string]any)
	if _, ok := layer["position"]; !ok {
		t.Fatal("resolved absolute geometry missing")
	}
	if _, ok := layer["style"]; !ok {
		t.Fatal("concrete style missing")
	}
}
