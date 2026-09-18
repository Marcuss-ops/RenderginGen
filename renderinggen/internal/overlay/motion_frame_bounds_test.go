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
				animation, err := lowerMotion(id, declarative.Definition.Enter, declarative.Definition.Exit, nil, "CATALOG TEST", duration)
				if err != nil {
					t.Fatalf("lower motion: %v", err)
				}
				if animation == nil {
					t.Fatal("lowering returned no animation")
				}
				assertTracksInsideDuration(t, id+"/layer", animation.Tracks, duration)
				for _, animator := range animation.TextAnimators {
					assertTracksInsideDuration(t, id+"/text/"+animator.ID+"/properties", animator.Properties, duration)
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
