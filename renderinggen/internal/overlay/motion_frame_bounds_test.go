package overlay

import (
	"fmt"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

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

func TestPhraseEntranceUsesOneThirdDurationFloorWhenExitAllowsIt(t *testing.T) {
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
		{name: "authored window already longer", authored: 110, exit: 12, duration: 288, wantEnter: 110, phrase: true},
		{name: "exit allows exactly floor", authored: 12, exit: 80, duration: 120, wantEnter: 40, phrase: true},
		{name: "exit constrains below floor", authored: 12, exit: 100, duration: 120, wantEnter: 20, phrase: true},
		{name: "one frame available before exit", authored: 12, exit: 119, duration: 120, wantEnter: 1, phrase: true},
		// Short phrase previews (3-6s) stretch to the full available window
		// so the stagger stays visibly animated for ~4s instead of <1s.
		{name: "phrase preview stretches to 4s window 108 frames", authored: 42, exit: 12, duration: 108, wantEnter: 96, phrase: true},
		{name: "phrase preview 72 frames stretches", authored: 42, exit: 12, duration: 72, wantEnter: 60, phrase: true},
		{name: "phrase preview 144 frames stretches", authored: 42, exit: 12, duration: 144, wantEnter: 132, phrase: true},
		{name: "non-phrase preview does not stretch", authored: 42, exit: 12, duration: 108, wantEnter: 42, phrase: false},
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

func TestPhrasePreviewAnimationStaysVisibleForFourSeconds(t *testing.T) {
	// The 4.5s preview (108 frames): the fix must keep the stagger alive
	// for 96 frames (4s at 24fps) minus the exit, not the authored 42.
	animation, err := lowerMotion("typewriter_clean", 42, 12, nil, "OLHA ISSO!", 108, true)
	if err != nil {
		t.Fatalf("lower typewriter_clean preview: %v", err)
	}
	if len(animation.TextAnimators) == 0 || animation.TextAnimators[0].Selectors[0].Start == nil {
		t.Fatalf("typewriter_clean has no selector sweep: %+v", animation.TextAnimators)
	}
	sweep := animation.TextAnimators[0].Selectors[0].Start
	// The sweep is baked per-frame (selector tracks accept linear keyframes
	// only), so the contract is: it starts at 0, reaches the last valid frame
	// 95 of the 96-frame window, and settles at the full 100.
	if len(sweep.Keyframes) < 2 {
		t.Fatalf("preview sweep must span the 96-frame window, got %+v", sweep.Keyframes)
	}
	lastSweep := sweep.Keyframes[len(sweep.Keyframes)-1]
	if lastSweep.Frame != 95 || lastSweep.Value != 100.0 || sweep.Keyframes[0].Frame != 0 || sweep.Keyframes[0].Value != 0.0 {
		t.Fatalf("preview sweep must run 0..95 and settle at 100, got first=%+v last=%+v", sweep.Keyframes[0], lastSweep)
	}
	// Non-phrase must not stretch: a 42-frame motion on 108 frames stays 42.
	nonPhrase, err := lowerMotion("typewriter_clean", 42, 12, nil, "OLHA ISSO!", 108, false)
	if err != nil {
		t.Fatalf("lower non-phrase: %v", err)
	}
	if nonPhrase.TextAnimators[0].Selectors[0].Start.Keyframes[1].Frame == 95 {
		t.Fatalf("non-phrase preview must not stretch to 96 frames")
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
