package overlay

// The Classic Apple long-phrase motions are authored in ChrononTemplate for
// wrapped copy and land in PipelineGen's long-phrase pool through the animation
// registry. This test is the renderer half of that contract: every pool entry
// must compile under the shared phrase preset (the run never asks for the
// classic_phrase style) and must keep a real selector sweep inside the
// one-third entrance, so the reveal cannot collapse to a layer fade.

import (
	"fmt"
	"testing"
)

func TestClassicAppleLongPhrasePoolCompilesUnderSharedPreset(t *testing.T) {
	cases := []struct {
		motion string
		unit   string
	}{
		{"classic_playfair_baseline_rise", "word"},
		{"classic_playfair_breath_in", "glyph"},
		{"classic_playfair_depth_push", "line"},
		{"classic_playfair_focus_resolve", "glyph"},
		{"classic_playfair_glyph_curtain", "line"},
		{"classic_playfair_glyph_glide", "glyph"},
		{"classic_playfair_hero_settle", "line"},
		{"classic_playfair_letter_tick", "glyph"},
		{"classic_playfair_line_float", "line"},
		{"classic_playfair_quiet_dissolve", "word"},
		{"classic_playfair_quiet_zoom", "word"},
		{"classic_playfair_soft_push_in", "word"},
		{"classic_playfair_tracking_close", "line"},
		{"classic_playfair_tracking_open", "word"},
		{"classic_playfair_word_lift", "word"},
	}
	for _, tc := range cases {
		t.Run(tc.motion, func(t *testing.T) {
			raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"classic-pool","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":%q,"text":"LE PAROLE GIUSTE ARRIVANO AL MOMENTO GIUSTO","start_ms":0,"end_ms":8000}]}`, tc.motion))
			result, err := CompileSemantic(raw)
			if err != nil {
				t.Fatalf("compile %q under phrase_default: %v", tc.motion, err)
			}
			if len(result.Plan.Layers) != 1 {
				t.Fatalf("%q compiled %d layers, want one phrase layer", tc.motion, len(result.Plan.Layers))
			}
			layer := result.Plan.Layers[0]
			if layer.Animation == nil || len(layer.Animation.TextAnimators) != 1 {
				t.Fatalf("%q compiled no single text animator", tc.motion)
			}
			animator := layer.Animation.TextAnimators[0]
			if len(animator.Selectors) != 1 {
				t.Fatalf("%q compiled %d selectors, want one", tc.motion, len(animator.Selectors))
			}
			selector := animator.Selectors[0]
			if selector.Unit != tc.unit {
				t.Fatalf("%q selector unit = %q, want %q", tc.motion, selector.Unit, tc.unit)
			}
			if selector.Start == nil || len(selector.Start.Keyframes) < 3 {
				t.Fatalf("%q lost its selector sweep: %+v", tc.motion, selector.Start)
			}
			last := selector.Start.Keyframes[len(selector.Start.Keyframes)-1]
			if opened, _ := last.Value.(float64); opened < 99.9 {
				t.Fatalf("%q selector sweep never opens: last keyframe %+v", tc.motion, last)
			}
			// The phrase entrance is one third of the 192-frame window.
			if last.Frame > 63 {
				t.Fatalf("%q sweep ends at frame %d, past the 64-frame entrance", tc.motion, last.Frame)
			}
			if len(animator.Properties) == 0 {
				t.Fatalf("%q compiled no animator properties", tc.motion)
			}
		})
	}
}
