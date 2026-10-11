package overlay

import (
	"encoding/json"
	"strings"
	"testing"
)

// One phrase_chain item must lower to one layer per segment inside the same
// pass, each keeping its own motion, with the fly-under + fade transition on
// every non-first segment.
func TestCompilePhraseChainExpandsSegments(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-test","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{
		"id":"chain-1","kind":"phrase_chain","template_id":"PHRASE_CHAIN","preset_id":"phrase_default",
		"motion_id":"phrase_apple_clean_07_slide_up_soft","text":"FIRST",
		"start_ms":0,"end_ms":4000,
		"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_segments":[
			{"text":"FIRST","motion_id":"phrase_apple_clean_07_slide_up_soft","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":0,"duration_us":2000000},
			{"text":"SECOND","motion_id":"soft_edge_spotlight_dissolve","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":2000000,"duration_us":2000000}
		]}
	}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic chain: %v", err)
	}
	layers := result.Plan.Layers
	if len(layers) < 2 {
		rawPlan, _ := json.Marshal(result.Plan)
		t.Fatalf("want >= 2 layers for 2 segments, got %d: %s", len(layers), rawPlan)
	}
	var first, second *Layer
	for i := range layers {
		if strings.Contains(layers[i].ID, "seg0") && first == nil {
			first = &layers[i]
		}
		if strings.Contains(layers[i].ID, "seg1") && second == nil {
			second = &layers[i]
		}
	}
	if first == nil || second == nil {
		t.Fatalf("segment layers not found: %+v", layers)
	}
	if first.Text != "FIRST" || second.Text != "SECOND" {
		t.Fatalf("segment texts = %q/%q", first.Text, second.Text)
	}
	if second.TransitionIn == nil || second.TransitionIn.Kind != "fly_under_fade" {
		t.Fatalf("second segment needs the fly-under fade transition: %+v", second.TransitionIn)
	}
	if first.TransitionIn != nil {
		t.Fatalf("first segment must enter with its own motion, no transition: %+v", first.TransitionIn)
	}
	if second.StartFrame <= first.StartFrame {
		t.Fatalf("segments must be ordered in time: %d vs %d", first.StartFrame, second.StartFrame)
	}
}

// A chain whose segments escape the item window fails closed instead of
// rendering a mistimed video.
func TestCompilePhraseChainRejectsEscapingSegment(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-bad","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{
		"id":"chain-1","kind":"phrase_chain","template_id":"PHRASE_CHAIN","preset_id":"phrase_default",
		"motion_id":"phrase_apple_clean_07_slide_up_soft","text":"FIRST",
		"start_ms":0,"end_ms":2000,
		"params":{"chain_segments":[
			{"text":"FIRST","motion_id":"phrase_apple_clean_07_slide_up_soft","preset_id":"phrase_default","start_us":0,"duration_us":2000000},
			{"text":"LATE","motion_id":"soft_edge_spotlight_dissolve","preset_id":"phrase_default","start_us":5000000,"duration_us":2000000}
		]}
	}]}`)
	if _, err := CompileSemantic(raw); err == nil {
		t.Fatal("escaping chain segment must fail closed")
	}
}
