package overlay

import "testing"

// TestStatsComeFromTheSingleCompilePass pins that the ledger counters are a
// product of CompileSemantic — the one compile pass — and never a second
// reading of the raw document. Every item is valid so the pass succeeds.
func TestStatsComeFromTheSingleCompilePass(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p1","video_id":"v1","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"a","entity_id":"person:ada","kind":"entity_card","template_id":"PERSON","preset_id":"lower_third_safe","text":"Ada","start_ms":0,"end_ms":1000,"duration_ms":1000},
        {"id":"b","entity_id":"org:acme","kind":"organization","template_id":"ORGANIZATION","preset_id":"lower_third_safe","text":"ACME","start_ms":1000,"end_ms":2000,"duration_ms":1000},
        {"id":"c","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"caption_card","text":"hi","start_ms":0,"end_ms":1000},
        {"id":"d","kind":"important_word","template_id":"IMPORTANT_WORD","preset_id":"active_word_pop","text":"WOW","start_ms":0,"end_ms":1000},
        {"id":"e","kind":"number","template_id":"NUMBER","preset_id":"active_word_pop","text":"42","start_ms":0,"end_ms":1000},
        {"id":"f","kind":"entity_image","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in","start_ms":0,"end_ms":1000,
         "asset_refs":[{"asset_id":"a","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"https://store.example/a.png","media_type":"image/png"}]},
        {"id":"g","kind":"light_leak","template_id":"LIGHT_LEAK","start_ms":0,"end_ms":1000,
         "asset_refs":[{"asset_id":"l","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","url":"https://store.example/l.mp4","media_type":"video/mp4"}]}
      ]
    }`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	stats := result.Stats
	if stats.EntityCount != 2 {
		t.Errorf("entity_count = %d, want 2", stats.EntityCount)
	}
	if stats.ImportantPhraseCnt != 1 {
		t.Errorf("important_phrase_count = %d, want 1", stats.ImportantPhraseCnt)
	}
	if stats.ImportantWordCnt != 2 {
		t.Errorf("important_word_count = %d, want 2 (IMPORTANT_WORD + NUMBER)", stats.ImportantWordCnt)
	}
	if stats.ImageCount != 1 {
		t.Errorf("image_count = %d, want 1", stats.ImageCount)
	}
	if stats.LightLeakCount != 1 {
		t.Errorf("light_leak_count = %d, want 1", stats.LightLeakCount)
	}
	if stats.PresetID != "lower_third_safe" {
		t.Errorf("preset_id = %q, want lower_third_safe (first item that carries one)", stats.PresetID)
	}
	if len(result.Plan.Layers) == 0 {
		t.Fatal("compile pass emitted no layers")
	}
}

// TestCompileSemanticRejectsConcretePlan pins the fail-closed boundary: a
// concrete chronon.render-plan.v2 document is not a semantic input and must
// never be counted or executed as one.
func TestCompileSemanticRejectsConcretePlan(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan.v2","version":2,"canvas":{"width":1280,"height":720,"fps_num":30,"fps_den":1,"duration_frames":1},"layers":[],"output":{"path":"out.mp4"}}`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("concrete plan must be rejected by the semantic compiler")
	}
}

func TestCompileSemanticMalformedFails(t *testing.T) {
	if _, err := CompileSemantic([]byte(`{`)); err == nil {
		t.Fatal("malformed JSON must fail closed")
	}
}
