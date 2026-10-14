package overlay

import (
	"strings"
	"testing"
)

// TestChainShortPhraseSegmentsKeepVisibleEntrance pins the short-hold retiming
// inside fused chains: two short-phrase segments at the minimum chain segment
// duration (2.5 s, below some recipes' authored Enter) must each lower with a
// non-empty entrance animation instead of collapsing to a static hold, and the
// non-first segment must still carry the fly-under handoff.
func TestChainShortPhraseSegmentsKeepVisibleEntrance(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-short-hold","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,` +
		`"items":[{"id":"s1:chain-text:0","scene_id":"s1","kind":"phrase_chain","template_id":"PHRASE_CHAIN","preset_id":"phrase_default",` +
		`"text":"alpha","start_ms":0,"end_ms":5400,"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_gap_us":400000,` +
		`"chain_motions":["short_phrase_character_tracking_reveal","short_phrase_editorial_quiet_zoom"],` +
		`"chain_segments":[` +
		`{"text":"alpha","motion_id":"short_phrase_character_tracking_reveal","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":0,"duration_us":2500000},` +
		`{"text":"beta gamma","motion_id":"short_phrase_editorial_quiet_zoom","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":2900000,"duration_us":2500000}` +
		`]}}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("chain with short-phrase segments must compile: %v", err)
	}
	if len(result.Plan.Layers) != 2 {
		t.Fatalf("chain layers = %d, want 2 segments: %v", len(result.Plan.Layers), layerIDs(result.Plan.Layers))
	}
	for i, layer := range result.Plan.Layers {
		// Layer tracks and glyph animators travel on separate fields:
		// applyMotionRouting keeps text-only entrances (e.g. tracking
		// reveals, which author no layer tracks) on layer.TextAnimators
		// while layer.Animation stays nil. Either one proves the
		// short-hold retiming did not collapse the entrance.
		layerTracks, layerAnimators := 0, len(layer.TextAnimators)
		if layer.Animation != nil {
			layerTracks = len(layer.Animation.Tracks)
			layerAnimators += len(layer.Animation.TextAnimators)
		}
		if layerTracks == 0 && layerAnimators == 0 {
			t.Errorf("segment layer %q has an empty entrance animation (short-hold collapse)", layer.ID)
		}
		if i == 1 && (layer.TransitionIn == nil || layer.TransitionIn.Kind != "fly_under_fade") {
			t.Errorf("second segment must carry the fly-under handoff, got %+v", layer.TransitionIn)
		}
	}
}

// TestChainMixedShapeSegmentKeepsFill pins the shape-fill carve-out in chain
// lowering: a shape bar carrying its native array fill inside a mixed chain
// must keep its rect geometry instead of losing the color to the text-style
// filter (the standalone chart failure, fused form).
func TestChainMixedShapeSegmentKeepsFill(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"chain-mixed-fill","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,` +
		`"items":[{"id":"s1:chain-mixed:0","scene_id":"s1","kind":"mixed_chain","template_id":"MIXED_CHAIN",` +
		`"text":"headline","start_ms":0,"end_ms":5000,"params":{"chain_camera":"fly_under_fade","chain_count":2,"chain_gap_us":400000,` +
		`"chain_motions":["phrase_apple_clean_07_slide_up_soft",""],` +
		`"chain_segments":[` +
		`{"text":"headline","motion_id":"phrase_apple_clean_07_slide_up_soft","preset_id":"phrase_default","template_id":"IMPORTANT_PHRASE","kind":"text_phrase","start_us":0,"duration_us":2500000},` +
		`{"template_id":"SHAPE","kind":"shape","start_us":2900000,"duration_us":2100000,"params":{"shape":"rect","width":400,"height":78,"position":[861.184,284.4],"fill":[0.2,0.78,0.94,1]}}` +
		`]}}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("mixed chain with a shape segment must compile: %v", err)
	}
	found := false
	for _, layer := range result.Plan.Layers {
		if layer.Shape == nil || layer.Shape.Type != "rect" {
			continue
		}
		found = true
		fill, ok := layer.Shape.Fill.([]float64)
		if !ok || len(fill) != 4 || fill[1] < 0.7 {
			t.Errorf("shape segment fill = %#v, want the [0.2,0.78,0.94,1] bar color", layer.Shape.Fill)
		}
	}
	if !found {
		t.Fatalf("no rect shape segment lowered: %v", layerIDs(result.Plan.Layers))
	}
	if wire, err := result.Plan.Marshal(); err != nil {
		t.Fatalf("marshal chain plan: %v", err)
	} else if strings.Contains(string(wire), `"chart"`) {
		t.Fatalf("compiled chain must not carry a shape.chart payload")
	}
}
