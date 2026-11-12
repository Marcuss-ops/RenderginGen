package overlay

import (
	"strings"
	"testing"
)

// Maps are standalone by contract: a hand-built mixed chain cannot smuggle a
// georeferenced map segment through the consumer compiler.
func TestCompileRejectsMapInMixedChain(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"map-in-mixed","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[{
		"id":"chain-mixed-1","kind":"mixed_chain","template_id":"MIXED_CHAIN",
		"start_ms":0,"end_ms":6000,
		"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_segments":[
			{"motion_id":"image_fade_reveal","template_id":"MAP","kind":"map","start_us":0,"duration_us":3000000,
			 "map":{"provider":"local","source_id":"plate","source_license":"operator-supplied","center":{"latitude":0.0,"longitude":0.0},"zoom":8,"width":1280,"height":720,"attribution":"operator","pins":[]}},
			{"text":"frase","motion_id":"phrase_default","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":3000000,"duration_us":3000000}
		]}
	}]}`)
	_, err := CompileSemantic(raw)
	if err == nil || !strings.Contains(err.Error(), "maps must stay standalone") {
		t.Fatalf("mixed chain carrying a map must fail closed, got %v", err)
	}
}

// A map_chain is only valid when every segment actually carries a map.
func TestCompileRejectsMapChainSegmentWithoutMap(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"map-chain-missing","video_id":"v","width":1280,"height":720,"fps_num":30,"fps_den":1,"items":[{
		"id":"chain-map-1","kind":"map_chain","template_id":"MAP_CHAIN",
		"start_ms":0,"end_ms":6000,
		"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_segments":[
			{"motion_id":"image_fade_reveal","template_id":"MAP","kind":"map","start_us":0,"duration_us":3000000,
			 "map":{"provider":"local","source_id":"plate","source_license":"operator-supplied","center":{"latitude":0.0,"longitude":0.0},"zoom":8,"width":1280,"height":720,"attribution":"operator","pins":[]}},
			{"text":"frase","motion_id":"phrase_default","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":3000000,"duration_us":3000000}
		]}
	}]}`)
	_, err := CompileSemantic(raw)
	if err == nil || !strings.Contains(err.Error(), "has no map payload") {
		t.Fatalf("map_chain segment without a map must fail closed, got %v", err)
	}
}
