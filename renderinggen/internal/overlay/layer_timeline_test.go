package overlay

import "testing"

func TestShiftTextAnimatorsToCompositionClock(t *testing.T) {
	track := func() AnimationTrack {
		return AnimationTrack{
			Property: "opacity",
			Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: 0.0},
				{Frame: 7, Value: 1.0},
			},
		}
	}
	start := track()
	plan := &Plan{Layers: []Layer{
		{
			ID: "chain:seg1", Type: "text", StartFrame: 41, DurationFrames: 29,
			// Composition-level layer animation stays layer-local.
			Animation: &LayerAnimation{Tracks: []AnimationTrack{track()}},
			TextAnimators: []TextAnimator{{
				ID:         "typing",
				Selectors:  []TextSelector{{ID: "typing_selector", Unit: "glyph", Start: &start}},
				Properties: []AnimationTrack{track()},
			}},
		},
		{
			ID: "chain:seg0", Type: "text", StartFrame: 0, DurationFrames: 30,
			TextAnimators: []TextAnimator{{
				ID:         "typing",
				Selectors:  []TextSelector{{ID: "typing_selector", Unit: "glyph", Start: trackPtr(track())}},
				Properties: []AnimationTrack{track()},
			}},
		},
	}}

	shiftTextAnimatorsToCompositionClock(plan)

	shifted := plan.Layers[0]
	if got := shifted.Animation.Tracks[0].Keyframes[1].Frame; got != 7 {
		t.Fatalf("layer animation frame = %d, want layer-local 7", got)
	}
	animator := shifted.TextAnimators[0]
	if got := animator.Selectors[0].Start.Keyframes[0].Frame; got != 41 {
		t.Fatalf("selector start frame = %d, want 41", got)
	}
	if got := animator.Selectors[0].Start.Keyframes[1].Frame; got != 48 {
		t.Fatalf("selector end frame = %d, want 48", got)
	}
	if got := animator.Properties[0].Keyframes[0].Frame; got != 41 {
		t.Fatalf("property start frame = %d, want 41", got)
	}

	zero := plan.Layers[1]
	if got := zero.TextAnimators[0].Selectors[0].Start.Keyframes[1].Frame; got != 7 {
		t.Fatalf("zero-start animator frame = %d, want 7", got)
	}
}

func trackPtr(track AnimationTrack) *AnimationTrack { return &track }
