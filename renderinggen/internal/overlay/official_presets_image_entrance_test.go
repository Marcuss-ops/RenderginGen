package overlay

import "testing"

// The image corpus renders a 5-second card whose reveal must be visible for at
// least 1.5 seconds. The first delivery shipped an 8-frame entrance (0.33 s at
// 24 fps): the clip looked static, and nothing in the code said how long the
// reveal was supposed to last. These tests pin the window in BOTH places that
// can change it — the catalog's Enter field and the animation the compiler
// actually lowers into the render plan — so the regression cannot come back
// through either door.
const (
	// imageEntranceFrameIntervals is the revealed duration in frame intervals:
	// 36 intervals = 1.5 s at 24 fps. A window of 37 frames starts at frame 0
	// and is still moving at frame 36.
	imageEntranceFrameIntervals = 36
	imageEntranceCatalogFrames  = imageEntranceFrameIntervals + 1
)

// fullImagePresets are the reveal presets the corpus renders with a long
// entrance. image_fast_fade is deliberately excluded: it keeps the short fade
// (and is pinned below), because a corpus needs one fast variant too.
var fullImagePresets = []string{
	"image_focus_in",
	"image_fade_in",
	"image_scale_in",
	"image_slide_left",
	"image_slide_right",
}

// TestImagePresetCatalogEntranceIsAtLeastOneAndAHalfSeconds pins the authoring
// window of every long-reveal image preset.
func TestImagePresetCatalogEntranceIsAtLeastOneAndAHalfSeconds(t *testing.T) {
	for _, id := range fullImagePresets {
		def, err := ResolveOfficialPreset(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		if def.Family != PresetImage {
			t.Errorf("preset %s is %s, want the image family", id, def.Family)
		}
		if def.Motion.Enter < imageEntranceCatalogFrames {
			t.Errorf("preset %s entrance = %d frame(s), want >= %d (1.5 s at 24 fps): the reveal is not visible long enough",
				id, def.Motion.Enter, imageEntranceCatalogFrames)
		}
		if def.Motion.ID == "" {
			t.Errorf("preset %s has no motion id: the entrance cannot be lowered", id)
		}
	}
}

// TestImagePresetEntranceReachesTheRenderPlan is the half that matters: a longer
// catalog value is worthless if the lowering clamps or drops it. It compiles a
// real image plan per preset through CompileSemantic and measures the keyframe
// span of the emitted animation.
func TestImagePresetEntranceReachesTheRenderPlan(t *testing.T) {
	for _, id := range fullImagePresets {
		def, err := ResolveOfficialPreset(id)
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		plan := certificationPlan(t, id)
		layer := certificationEntityLayer(plan, def)
		if layer == nil {
			t.Fatalf("preset %s: compiled plan carries no image layer", id)
		}
		if layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
			t.Fatalf("preset %s lowered to a static layer: no animation track reached the render plan", id)
		}
		span := int64(-1)
		properties := map[string]bool{}
		for _, track := range layer.Animation.Tracks {
			properties[track.Property] = true
			for _, keyframe := range track.Keyframes {
				if keyframe.Frame > span {
					span = keyframe.Frame
				}
			}
		}
		if span < imageEntranceFrameIntervals {
			t.Errorf("preset %s: entrance motion spans %d frame interval(s), want >= %d (1.5 s at 24 fps) — properties=%v",
				id, span+1, imageEntranceFrameIntervals, properties)
		}
		// A reveal must move something visible: a fade drives opacity, the
		// focus/scale presets drive scale, the slide presets drive position.
		if len(properties) == 0 {
			t.Errorf("preset %s: entrance animation declares no property", id)
		}
	}
}

// TestImageFastFadeStaysShort pins the documented exception, so "make every
// image preset 1.5 s" cannot silently turn the fast variant into a slow one.
func TestImageFastFadeStaysShort(t *testing.T) {
	def, err := ResolveOfficialPreset("image_fast_fade")
	if err != nil {
		t.Fatalf("resolve image_fast_fade: %v", err)
	}
	if def.Motion.Enter >= imageEntranceCatalogFrames {
		t.Errorf("image_fast_fade entrance = %d frame(s), want < %d: this preset is the fast variant on purpose",
			def.Motion.Enter, imageEntranceCatalogFrames)
	}
}
