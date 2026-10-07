package overlay

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

func TestMotionWindowsRejectsInvalidRuntimeTimingOverrides(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "string", value: "fast"},
		{name: "fraction", value: 2.5},
		{name: "negative", value: -1},
		{name: "too large", value: 241},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "NaN", value: math.NaN()},
		{name: "integer overflow", value: int64(math.MaxInt64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := motionWindows(map[string]any{"enter_frames": tc.value}, 12); err == nil || !strings.Contains(err.Error(), "motion_params.enter_frames") {
				t.Fatalf("motionWindows accepted %v or returned an unclear error: %v", tc.value, err)
			}
		})
	}
}

func TestMotionWindowsExposeRuntimeFrameControlsWithoutClosingPluginParams(t *testing.T) {
	enter, exit, err := motionWindows(map[string]any{
		"enter_frames":           1,
		"exit_frames":            240,
		"active_layer_id":        "front",
		"plugin_specific_option": map[string]any{"mode": "focus"},
	}, 12)
	if err != nil {
		t.Fatalf("valid runtime timing: %v", err)
	}
	if enter != 1 || exit != 240 {
		t.Fatalf("runtime windows = %d/%d, want 1/240", enter, exit)
	}
	enter, exit, err = motionWindows(map[string]any{"enter_frames": 0, "exit_frames": 0}, 12)
	if err != nil || enter != 0 || exit != 12 {
		t.Fatalf("zero should retain catalog/preset windows, got %d/%d, err=%v", enter, exit, err)
	}
}

func TestCompileImportantPhraseRejectsInvalidRuntimeTiming(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"bad-phrase-timing","video_id":"v","width":1280,"height":720,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"typewriter_clean","motion_params":{"enter_frames":"fast"},"text":"Runtime timing stays explicit","start_ms":0,"end_ms":3000}]}`)
	if _, err := CompileSemantic(raw); err == nil || !strings.Contains(err.Error(), "motion_params.enter_frames") {
		t.Fatalf("invalid phrase motion timing was not rejected clearly: %v", err)
	}
}

func TestImageLayerRuntimeTimingRejectsInvalidStandaloneAndChildOverrides(t *testing.T) {
	preset, err := resolveOfficialPreset("image_focus_in", string(PresetImage))
	if err != nil {
		t.Fatalf("resolve image preset: %v", err)
	}
	for _, scope := range []string{"standalone image", "composite child"} {
		t.Run(scope, func(t *testing.T) {
			_, err := compileSingleImageLayer(resolvedItem{Kind: KindEntityImage}, nil, "asset.png", preset,
				"image_focus_reveal", map[string]any{"exit_frames": -2}, 0, 120, "image")
			if err == nil || !strings.Contains(err.Error(), "motion_params.exit_frames") {
				t.Fatalf("invalid %s timing was not rejected: %v", scope, err)
			}
		})
	}
	if _, err := imageMotionAnimation("image_glow_depth_in", map[string]any{"enter_frames": "slow"}, 120, 12); err == nil {
		t.Fatal("premium image recipe accepted malformed runtime timing")
	}
}

func TestEveryCatalogMotionKeepsAllKeyframesInsideLayerDuration(t *testing.T) {
	durations := []int64{1, 2, 8, 17, 24, 72, 120}
	ids := motion.Registry.List()
	if len(ids) == 0 {
		t.Fatal("motion catalog is empty")
	}

	for _, id := range ids {
		plugin, err := motion.Registry.Resolve(id)
		if err != nil {
			t.Fatalf("resolve %q: %v", id, err)
		}
		declarative, ok := plugin.(motion.DeclarativePlugin)
		if !ok {
			t.Fatalf("motion %q is not declarative; catalog-wide bounds test cannot inspect its authored windows", id)
		}

		for _, duration := range durations {
			t.Run(fmt.Sprintf("%s/duration_%d", id, duration), func(t *testing.T) {
				animation, err := lowerMotion(id, declarative.Definition.Enter, declarative.Definition.Exit, nil, "CATALOG TEST", duration, false)
				if err != nil {
					t.Fatalf("lower motion: %v", err)
				}
				if animation == nil {
					t.Fatal("lowering returned no animation")
				}
				assertTracksInsideDuration(t, id+"/layer", animation.Tracks, duration)
				assertUniqueTrackFrames(t, id+"/layer", animation.Tracks)
				for _, animator := range animation.TextAnimators {
					assertTracksInsideDuration(t, id+"/text/"+animator.ID+"/properties", animator.Properties, duration)
					assertUniqueTrackFrames(t, id+"/text/"+animator.ID+"/properties", animator.Properties)
					for _, selector := range animator.Selectors {
						assertSelectorTrackInsideDuration(t, id+"/text/"+animator.ID+"/selector/"+selector.ID+"/start", selector.Start, duration)
						assertSelectorTrackInsideDuration(t, id+"/text/"+animator.ID+"/selector/"+selector.ID+"/end", selector.End, duration)
						assertSelectorTrackInsideDuration(t, id+"/text/"+animator.ID+"/selector/"+selector.ID+"/offset", selector.Offset, duration)
						assertSelectorTrackInsideDuration(t, id+"/text/"+animator.ID+"/selector/"+selector.ID+"/amount", selector.Amount, duration)
					}
				}
			})
		}
	}
}

func assertUniqueTrackFrames(t *testing.T, path string, tracks []AnimationTrack) {
	t.Helper()
	for _, track := range tracks {
		for i := 1; i < len(track.Keyframes); i++ {
			if track.Keyframes[i-1].Frame == track.Keyframes[i].Frame {
				t.Errorf("%s %s has duplicate keyframe at frame %d: %+v", path, track.Property, track.Keyframes[i].Frame, track.Keyframes)
			}
		}
	}
}

func TestPhraseEntranceUsesExactlyOneThirdDurationAndFitsExit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		authored  int
		exit      int
		duration  int64
		wantEnter int64
		phrase    bool
	}{
		{name: "twelve seconds at 24fps", authored: 72, exit: 12, duration: 288, wantEnter: 96, phrase: true},
		{name: "floor rounds up to whole frame", authored: 1, exit: 0, duration: 10, wantEnter: 4, phrase: true},
		{name: "non-phrase motions retain authored ceiling", authored: 72, exit: 12, duration: 288, wantEnter: 72, phrase: false},
		{name: "one third overrides a longer authored window", authored: 110, exit: 12, duration: 288, wantEnter: 96, phrase: true},
		{name: "exit allows exactly one third", authored: 12, exit: 80, duration: 120, wantEnter: 40, phrase: true},
		{name: "exit would overlap entrance and is shortened", authored: 12, exit: 100, duration: 120, wantEnter: 40, phrase: true},
		{name: "exit leaves one frame but phrase keeps one third", authored: 12, exit: 119, duration: 120, wantEnter: 40, phrase: true},
		{name: "4.5s phrase entrance is one third", authored: 42, exit: 12, duration: 108, wantEnter: 36, phrase: true},
		{name: "3s phrase entrance is one third", authored: 42, exit: 12, duration: 72, wantEnter: 24, phrase: true},
		{name: "6s phrase entrance is one third", authored: 42, exit: 12, duration: 144, wantEnter: 48, phrase: true},
		{name: "non-phrase preview retains authored entrance", authored: 42, exit: 12, duration: 108, wantEnter: 42, phrase: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := entranceFrames(tc.authored, tc.exit, tc.duration, tc.phrase); got != tc.wantEnter {
				t.Fatalf("entranceFrames(%d, %d, %d) = %d, want %d", tc.authored, tc.exit, tc.duration, got, tc.wantEnter)
			}
		})
	}
}

func TestPhraseMotionTracksReachTheProportionalEntranceFloor(t *testing.T) {
	animation, err := lowerMotion("phrase_apple_clean_07_slide_up_soft", 60, 12, nil, "SIX WORD PHRASE FIT CHECK", 288, true)
	if err != nil {
		t.Fatalf("lower phrase motion: %v", err)
	}
	for _, track := range animation.Tracks {
		if track.Property == "opacity" {
			continue // phrase fade envelope is intentionally independent
		}
		foundEntranceEnd := false
		for _, keyframe := range track.Keyframes {
			foundEntranceEnd = foundEntranceEnd || keyframe.Frame == 95
		}
		if !foundEntranceEnd {
			t.Fatalf("phrase %s entrance did not stretch through frame 95 (96 frames): %+v", track.Property, track.Keyframes)
		}
	}
}

func TestPhrasePreviewAnimationRunsForOneThirdOfTheWindow(t *testing.T) {
	// The 4.5s preview (108 frames) animates for exactly 36 frames.
	animation, err := lowerMotion("typewriter_clean", 42, 100, nil, "OLHA ISSO!", 108, true)
	if err != nil {
		t.Fatalf("lower typewriter_clean preview: %v", err)
	}
	if len(animation.TextAnimators) == 0 || animation.TextAnimators[0].Selectors[0].Start == nil {
		t.Fatalf("typewriter_clean has no selector sweep: %+v", animation.TextAnimators)
	}
	sweep := animation.TextAnimators[0].Selectors[0].Start
	// Selector animation is baked per-frame; 36 frames span frame 0..35.
	if len(sweep.Keyframes) < 2 {
		t.Fatalf("preview sweep must span the 36-frame window, got %+v", sweep.Keyframes)
	}
	lastSweep := sweep.Keyframes[len(sweep.Keyframes)-1]
	if lastSweep.Frame != 35 || lastSweep.Value != 100.0 || sweep.Keyframes[0].Frame != 0 || sweep.Keyframes[0].Value != 0.0 {
		t.Fatalf("preview sweep must run 0..35 and settle at 100, got first=%+v last=%+v", sweep.Keyframes[0], lastSweep)
	}
	// The authored 100-frame exit is shortened to the 72 frames remaining
	// after the entrance, so entrance and exit fit the layer without overlap.
	if animation.Tracks == nil {
		t.Fatal("phrase entrance/exit layer tracks were not compiled")
	}
	for _, track := range animation.Tracks {
		for i := 1; i < len(track.Keyframes); i++ {
			if track.Keyframes[i].Frame <= track.Keyframes[i-1].Frame {
				t.Fatalf("phrase track %q is not strictly increasing: %+v", track.Property, track.Keyframes)
			}
		}
	}
	// Non-phrase motions retain their authored entrance window.
	nonPhrase, err := lowerMotion("typewriter_clean", 42, 12, nil, "OLHA ISSO!", 108, false)
	if err != nil {
		t.Fatalf("lower non-phrase: %v", err)
	}
	if nonPhrase.TextAnimators[0].Selectors[0].Start.Keyframes[len(nonPhrase.TextAnimators[0].Selectors[0].Start.Keyframes)-1].Frame != 41 {
		t.Fatalf("non-phrase authored entrance should remain 42 frames")
	}
}

func TestCompileSemanticKeepsLongPhraseWindowAndOneThirdEntrance(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"long-phrase-one-third","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"phrase_apple_clean_07_slide_up_soft","motion_params":{"enter_frames":64},"text":"A LONG IMPORTANT PHRASE STAYS FOR ITS FULL SPOKEN WINDOW","start_ms":500,"end_ms":8500}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile long phrase: %v", err)
	}
	if len(result.Plan.Layers) != 1 {
		t.Fatalf("compiled layers = %d, want one phrase layer", len(result.Plan.Layers))
	}
	layer := result.Plan.Layers[0]
	if layer.StartFrame != 12 || layer.DurationFrames != 192 {
		t.Fatalf("phrase layer window = %d+%d frames, want [12,204) (8 seconds at 24fps)", layer.StartFrame, layer.DurationFrames)
	}
	if layer.Animation == nil {
		t.Fatal("phrase has no layer animation")
	}
	wantEntranceEnd := int64(63) // 64 entrance frames: [0,64)
	for _, track := range layer.Animation.Tracks {
		if track.Property == "opacity" {
			continue // shared readability envelope is not the motion entrance
		}
		foundEntranceEnd := false
		for _, keyframe := range track.Keyframes {
			if keyframe.Frame == wantEntranceEnd {
				foundEntranceEnd = true
			}
		}
		if !foundEntranceEnd {
			t.Fatalf("motion track %q does not end its entrance at frame %d: %+v", track.Property, wantEntranceEnd, track.Keyframes)
		}
	}
}

func TestPhrasePresetWithoutMotionOverrideUsesOneThirdEntrance(t *testing.T) {
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"phrase-preset-one-third","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","text":"A LONG IMPORTANT PHRASE USES THE PRESET MOTION WINDOW","start_ms":0,"end_ms":8000}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile preset-only phrase: %v", err)
	}
	if len(result.Plan.Layers) != 1 || result.Plan.Layers[0].DurationFrames != 192 {
		t.Fatalf("preset-only phrase layers = %+v, want one 192-frame phrase", result.Plan.Layers)
	}
	layer := result.Plan.Layers[0]
	if layer.Animation == nil {
		t.Fatal("preset-only phrase has no layer animation")
	}
	foundEntranceEnd := false
	for _, track := range layer.Animation.Tracks {
		if track.Property == "opacity" {
			continue
		}
		for _, keyframe := range track.Keyframes {
			foundEntranceEnd = foundEntranceEnd || keyframe.Frame == 63
		}
	}
	if !foundEntranceEnd {
		t.Fatalf("preset-only phrase entrance did not last exactly 64 frames (one third): %+v", layer.Animation.Tracks)
	}
}

func TestImage25DHoldsDoNotCompressTheEntrance(t *testing.T) {
	animation, err := lowerMotion("image_25d_yaw_flip_in", 16, 12, nil, "", 72, false)
	if err != nil {
		t.Fatalf("lower image motion: %v", err)
	}
	var rotation *AnimationTrack
	for i := range animation.Tracks {
		if animation.Tracks[i].Property == "rotation_y" {
			rotation = &animation.Tracks[i]
			break
		}
	}
	if rotation == nil {
		t.Fatal("image motion has no rotation_y track")
	}
	if len(rotation.Keyframes) < 3 {
		t.Fatalf("image rotation has only %d keyframes: %+v", len(rotation.Keyframes), rotation.Keyframes)
	}
	firstValue, firstOK := rotation.Keyframes[0].Value.(float64)
	secondValue, secondOK := rotation.Keyframes[1].Value.(float64)
	if !firstOK || !secondOK || rotation.Keyframes[0].Frame != 0 || firstValue > -17.9 || rotation.Keyframes[1].Frame != 15 || secondValue < -0.1 || secondValue > 0.1 {
		t.Fatalf("image rotation entrance was compressed by post-entrance holds: %+v", rotation.Keyframes)
	}
}

func assertTracksInsideDuration(t *testing.T, path string, tracks []AnimationTrack, duration int64) {
	t.Helper()
	for _, track := range tracks {
		for _, keyframe := range track.Keyframes {
			if keyframe.Frame < 0 || keyframe.Frame >= duration {
				t.Errorf("%s %s keyframe frame=%d outside [0,%d)", path, track.Property, keyframe.Frame, duration)
			}
		}
	}
}

func assertSelectorTrackInsideDuration(t *testing.T, path string, track *AnimationTrack, duration int64) {
	t.Helper()
	if track != nil {
		assertTracksInsideDuration(t, path, []AnimationTrack{*track}, duration)
	}
}
