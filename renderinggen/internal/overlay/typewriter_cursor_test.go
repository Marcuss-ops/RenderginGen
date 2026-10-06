package overlay

import (
	"math"
	"testing"
)

func TestCompileTypewriterCursorLeavesFinalSpace(t *testing.T) {
	const fontSize = 100.0
	text := Layer{
		ID: "phrase", Type: "text", Text: "OK", Position: []float64{960, 540},
		Size: []float64{500, 200}, Style: &LayerStyle{FontSize: fontSize},
		TextAnimators: []TextAnimator{{Selectors: []TextSelector{{Start: &AnimationTrack{
			Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 10, Value: 100.0}},
		}}}}},
	}
	ri := resolvedItem{Item: semanticItem{MotionID: "typewriter_modern_01_monospace_block_cursor"}}
	cursor, ok := compileTypewriterCursor(ri, &semanticPlan{}, text)
	if !ok {
		t.Fatal("typewriter cursor was not compiled")
	}
	track := cursor.Animation.Tracks[0]
	if len(track.Keyframes) != 2 {
		t.Fatalf("cursor x keyframes = %d, want 2", len(track.Keyframes))
	}
	lastIndex := len(track.Keyframes) - 1
	if got := track.Keyframes[lastIndex].Value.(float64); math.Abs(got-95) > 1e-6 {
		t.Fatalf("final cursor x = %.3f, want centered glyph end plus 32px", got)
	}
	withoutGap := 0.0
	for _, char := range text.Text {
		withoutGap += typewriterGlyphAdvance(char, fontSize)
	}
	withoutGap -= withoutGap * .5 // centered line origin
	if got := track.Keyframes[lastIndex].Value.(float64) - withoutGap; math.Abs(got-fontSize*.32) > 1e-6 {
		t.Fatalf("final cursor offset from final glyph edge = %.3f, want %.3f", got, fontSize*.32)
	}
}

func TestModernTypewriterSelectsBricolageAndHonorsExplicitFontOverride(t *testing.T) {
	for _, tc := range []struct {
		name, params, want string
	}{
		{"modern default", `{}`, "assets/fonts/Bricolage-Grotesque.ttf"},
		{"explicit Inter override", `{"font_family":"inter"}`, "assets/fonts/Inter.ttf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := `{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"modern-typewriter-font","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"typewriter_modern_01_monospace_block_cursor","text":"30 SEP 2026","params":` + tc.params + `,"start_ms":0,"end_ms":3000}]}`
			result, err := CompileSemantic([]byte(plan))
			if err != nil {
				t.Fatal(err)
			}
			if got := result.Plan.Layers[0].Style.Font; got != tc.want {
				t.Fatalf("runtime font = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAllModernTypewriterMotionsHaveRuntimeCursors(t *testing.T) {
	ids := []string{
		"typewriter_modern_01_monospace_block_cursor", "typewriter_modern_02_kinetic_scramble",
		"typewriter_modern_03_soft_opacity_ramp", "typewriter_modern_04_character_bounce",
		"typewriter_modern_05_backspace_correction", "typewriter_modern_06_glow_beam_sweep",
		"typewriter_modern_07_word_snap", "typewriter_modern_08_mechanical_y_shift",
		"typewriter_modern_09_highlighter_expansion", "typewriter_modern_10_weight_ramp",
		"typewriter_modern_11_dynamic_auto_wrap", "typewriter_modern_12_glitch_pop",
		"typewriter_modern_13_elastic_leading_cursor", "typewriter_modern_14_focal_blur_dissolve",
		"typewriter_modern_15_paper_punch_stencil",
	}
	for _, id := range ids {
		if _, ok := typewriterCursorForMotion(id, 100); !ok {
			t.Errorf("%s has no runtime cursor style", id)
		}
	}
}
