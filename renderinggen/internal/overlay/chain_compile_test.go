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
	if first.TransitionIn != nil {
		t.Fatalf("first segment must enter with its own motion, no transition: %+v", first.TransitionIn)
	}
	if second.TransitionIn == nil {
		t.Fatal("non-first chain segment must carry the fly-under dip + fade transition")
	}
	if second.TransitionIn.Kind != "fly_under_fade" {
		t.Fatalf("chain handoff transition = %q, want fly_under_fade", second.TransitionIn.Kind)
	}
	if dip, ok := second.TransitionIn.Params["dip_px"]; !ok || dip != 60.0 {
		t.Fatalf("chain handoff dip_px = %v, want 60.0", second.TransitionIn.Params["dip_px"])
	}
	if second.TransitionIn.DurationFrames < 1 || second.TransitionIn.DurationFrames > second.DurationFrames {
		t.Fatalf("chain handoff duration %d escapes the segment (%d frames)", second.TransitionIn.DurationFrames, second.DurationFrames)
	}
	if second.StartFrame <= first.StartFrame {
		t.Fatalf("segments must be ordered in time: %d vs %d", first.StartFrame, second.StartFrame)
	}
}

func TestSemanticItemFramesUseCanonicalMicrosecondsWhenPresent(t *testing.T) {
	startUS, durationUS := int64(100_500), int64(105_500)
	item := semanticItem{StartMS: 101, EndMS: 207, StartUS: &startUS, DurationUS: &durationUS}
	start, end := itemFrames(item, 24, 1)
	wantStart, wantEnd := usFrames(startUS, startUS+durationUS, 24, 1)
	if start != wantStart || end != wantEnd {
		t.Fatalf("item frames=[%d,%d), want direct microsecond frames [%d,%d)", start, end, wantStart, wantEnd)
	}
	legacy := semanticItem{StartMS: 101, EndMS: 207}
	legacyStart, legacyEnd := itemFrames(legacy, 24, 1)
	wantLegacyStart, wantLegacyEnd := msFrames(101, 207, 24, 1)
	if legacyStart != wantLegacyStart || legacyEnd != wantLegacyEnd {
		t.Fatalf("legacy frames=[%d,%d), want millisecond compatibility [%d,%d)", legacyStart, legacyEnd, wantLegacyStart, wantLegacyEnd)
	}
}

// A mixed_chain carrying one image segment + one text segment must lower to
// both an image layer and a text layer inside the same pass, with the
// fly-under fade on the second segment.
func TestCompileMixedChainExpandsImageAndText(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-mixed","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{
		"id":"chain-mixed-1","kind":"mixed_chain","template_id":"MIXED_CHAIN","preset_id":"phrase_default",
		"motion_id":"phrase_apple_clean_07_slide_up_soft","text":"LA FRASE",
		"start_ms":10000,"end_ms":15000,
		"asset_refs":[{"asset_id":"img-1","url":"https://example/img.jpg","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","media_type":"image/jpeg"}],
		"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_segments":[
			{"text":"","motion_id":"image_focus_reveal","preset_id":"image_scale_in","template_id":"IMAGE_OVERLAY","kind":"image","start_us":10000000,"duration_us":5000000,"asset_id":"img-1"},
			{"text":"LA FRASE","motion_id":"phrase_apple_clean_07_slide_up_soft","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":12000000,"duration_us":3000000}
		]}
	}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic mixed chain: %v", err)
	}
	hasImage, hasText := false, false
	for i := range result.Plan.Layers {
		l := result.Plan.Layers[i]
		if l.Type == "image" {
			hasImage = true
		}
		if l.Text == "LA FRASE" {
			hasText = true
		}

	}
	if !hasImage || !hasText {
		rawPlan, _ := json.Marshal(result.Plan)
		t.Fatalf("mixed chain needs image+text, got %s", rawPlan)
	}
}

// A mixed_chain with a number segment lowers it through the text path with
// its own motion and the fly-under fade.
func TestCompileMixedChainExpandsNumberSegment(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-number","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{
		"id":"chain-num-1","kind":"mixed_chain","template_id":"MIXED_CHAIN","preset_id":"phrase_default",
		"motion_id":"phrase_apple_clean_07_slide_up_soft","text":"FIRST",
		"start_ms":0,"end_ms":4000,
		"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_segments":[
			{"text":"FIRST","motion_id":"phrase_apple_clean_07_slide_up_soft","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":0,"duration_us":2000000},
			{"text":"42","motion_id":"phrase_apple_clean_07_slide_up_soft","preset_id":"phrase_default","template_id":"NUMBER","kind":"number","start_us":2000000,"duration_us":2000000}
		]}
	}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic mixed number chain: %v", err)
	}
	found := false
	for i := range result.Plan.Layers {
		if result.Plan.Layers[i].Text == "42" {
			found = true
			if result.Plan.Layers[i].TransitionIn == nil || result.Plan.Layers[i].TransitionIn.Kind != "fly_under_fade" {
				t.Fatalf("number segment must carry the fly-under handoff transition: %+v", result.Plan.Layers[i].TransitionIn)
			}
		}
	}
	if !found {
		t.Fatalf("number segment layer not found: %+v", result.Plan.Layers)
	}
}

// A map_chain with two map segments lowers grounded map layers for both.
func TestCompileMapChainExpandsMapSegments(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-map","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[{
		"id":"chain-map-1","kind":"map_chain","template_id":"MAP_CHAIN",
		"start_ms":0,"end_ms":6000,
		"asset_refs":[{"asset_id":"basemap","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"assets/maps/plate.png","media_type":"image/png"}],
		"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_segments":[
			{"motion_id":"image_fade_reveal","template_id":"MAP","kind":"map","start_us":0,"duration_us":3000000,
			 "assets":[{"asset_id":"basemap","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"assets/maps/plate.png","media_type":"image/png"}],
			 "map":{"provider":"local","source_id":"certified-plate","source_license":"operator-supplied","center":{"latitude":0.0,"longitude":0.0},"zoom":8,"width":1280,"height":720,"attribution":"Map data supplied by operator","motion_id":"image_fade_reveal","pins":[{"id":"target","label":"Target","latitude":0.0,"longitude":0.0,"color":"#E11D48","radius_px":12.0}]}},
			{"motion_id":"image_fade_reveal","template_id":"MAP","kind":"map","start_us":3000000,"duration_us":3000000,
			 "assets":[{"asset_id":"basemap","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","url":"assets/maps/plate.png","media_type":"image/png"}],
			 "map":{"provider":"local","source_id":"certified-plate","source_license":"operator-supplied","center":{"latitude":0.0,"longitude":0.05},"zoom":8,"width":1280,"height":720,"attribution":"Map data supplied by operator","motion_id":"image_fade_reveal","pins":[{"id":"target2","label":"Target 2","latitude":0.0,"longitude":0.05,"color":"#E11D48","radius_px":12.0}]}}
		]}
	}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic map chain: %v", err)
	}
	if len(result.Plan.Layers) < 2 {
		rawPlan, _ := json.Marshal(result.Plan)
		t.Fatalf("want map layers for both segments, got %d: %s", len(result.Plan.Layers), rawPlan)
	}
	foundSecond := false
	for _, layer := range result.Plan.Layers {
		if strings.Contains(layer.ID, "seg1") {
			foundSecond = true
			if layer.TransitionIn != nil {
				t.Fatalf("map segment must use its camera motion without fly-under transition: %+v", layer.TransitionIn)
			}
		}
	}
	if !foundSecond {
		t.Fatalf("second map segment layers not found: %+v", result.Plan.Layers)
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

// A mixed_chain whose FIRST segment is text carries the text runtime
// overrides at chain level (the producer mirrors the first segment). The
// non-text siblings must not inherit them: live failure was
// `cannot apply text runtime override "font_size_px" to a non-text layer`
// on a text-first mixed chain.
func TestCompileMixedChainTextFirstDropsOverridesOnImage(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-mixed-text-first","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{
		"id":"chain-mixed-tf-1","kind":"mixed_chain","template_id":"MIXED_CHAIN","preset_id":"phrase_default",
		"motion_id":"phrase_apple_clean_07_slide_up_soft","text":"presunzione di innocenza",
		"start_ms":10000,"end_ms":16000,
		"asset_refs":[{"asset_id":"img-1","url":"https://example/img.jpg","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","media_type":"image/jpeg"}],
		"params":{"chain_camera":"fly_under_fade","chain_count":2,"font_size_px":82,"glow_size":10,"stroke_size":2,"chain_segments":[
			{"text":"presunzione di innocenza","motion_id":"phrase_apple_clean_07_slide_up_soft","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":10000000,"duration_us":3000000},
			{"text":"","motion_id":"image_focus_reveal","preset_id":"image_scale_in","template_id":"IMAGE_OVERLAY","kind":"image","start_us":13000000,"duration_us":3000000,"asset_id":"img-1"}
		]}
	}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic text-first mixed chain: %v", err)
	}
	hasImage, hasText := false, false
	for i := range result.Plan.Layers {
		l := result.Plan.Layers[i]
		if l.Type == "image" {
			hasImage = true
		}
		if l.Text == "presunzione di innocenza" {
			hasText = true
		}
	}
	if !hasImage || !hasText {
		rawPlan, _ := json.Marshal(result.Plan)
		t.Fatalf("text-first mixed chain needs image+text, got %s", rawPlan)
	}
}
