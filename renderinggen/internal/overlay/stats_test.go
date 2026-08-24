package overlay

import (
	"reflect"
	"testing"
)

func TestSemanticStatsCountsSemanticPlan(t *testing.T) {
	raw := []byte(`{
      "schema_version":"renderinggen.overlay-plan.v1",
      "plan_id":"p1","video_id":"v1","width":1280,"height":720,"fps_num":30,"fps_den":1,
      "items":[
        {"id":"a","template_id":"PERSON","start_ms":0,"end_ms":1000},
        {"id":"b","template_id":"ORGANIZATION","start_ms":1000,"end_ms":2000},
        {"id":"c","template_id":"IMPORTANT_PHRASE","start_ms":0,"end_ms":1000},
        {"id":"d","template_id":"IMPORTANT_WORD","start_ms":0,"end_ms":1000},
        {"id":"e","template_id":"NUMBER","start_ms":0,"end_ms":1000},
        {"id":"f","template_id":"IMAGE_OVERLAY","start_ms":0,"end_ms":1000},
        {"id":"g","template_id":"LIGHT_LEAK","start_ms":0,"end_ms":1000,"preset_id":"impact_mix_v1"}
      ]
    }`)
	stats, err := SemanticStats(raw)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
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
	if stats.PresetID != "impact_mix_v1" {
		t.Errorf("preset_id = %q, want impact_mix_v1 (first item that carries one)", stats.PresetID)
	}
}

func TestSemanticStatsLegacyConcretePlanIsZero(t *testing.T) {
	raw := []byte(`{"schema":"chronon.render-plan","version":1,"canvas":{"width":1280,"height":720,"fps_num":30,"fps_den":1}}`)
	stats, err := SemanticStats(raw)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if !reflect.DeepEqual(stats, Stats{}) {
		t.Fatalf("legacy plan must yield zero stats, got %+v", stats)
	}
}

func TestSemanticStatsUnknownSchemaIsZero(t *testing.T) {
	raw := []byte(`{"schema_version":"future.schema.v9"}`)
	stats, err := SemanticStats(raw)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if !reflect.DeepEqual(stats, Stats{}) {
		t.Fatalf("unknown schema must yield zero stats, got %+v", stats)
	}
}

func TestSemanticStatsMalformedFails(t *testing.T) {
	if _, err := SemanticStats([]byte(`{`)); err == nil {
		t.Fatal("malformed JSON must fail closed")
	}
}
