package overlay

// layer_timeline.go owns the one translation between the layer-local clock the
// text lowerers use and the composition clock Chronon samples for text
// animators.
//
// A text layer's composition-level Animation stays layer-local: the first
// phrase of a chain reveals, and so does a standalone overlay placed later.
// Text animators are different: their selector/property keyframes are sampled
// on the composition clock. Every lowerer authors them from frame 0 because it
// knows only the layer it is building, so a chain's second card at frame 41
// used to reach the renderer with its 0→7 selector already elapsed: the first
// card typed, every later card rendered fully revealed and static. This pass
// shifts both halves of every text animator once, after the plate lift, so the
// reveal stays attached to its layer.
//
// Selector start/end/offset/amount and the per-unit properties share the same
// clock, so they move together; an exit authored on the same curve moves with
// the entrance.

// shiftTextAnimatorsToCompositionClock translates text-animator keyframes to
// absolute composition frames. It is idempotent for layers already at frame 0.
func shiftTextAnimatorsToCompositionClock(plan *Plan) {
	if plan == nil {
		return
	}
	for index := range plan.Layers {
		layer := &plan.Layers[index]
		shift := layer.StartFrame
		if shift <= 0 || len(layer.TextAnimators) == 0 {
			continue
		}
		for animatorIndex := range layer.TextAnimators {
			animator := &layer.TextAnimators[animatorIndex]
			for selectorIndex := range animator.Selectors {
				selector := &animator.Selectors[selectorIndex]
				shiftAnimationTrack(selector.Start, shift)
				shiftAnimationTrack(selector.End, shift)
				shiftAnimationTrack(selector.Offset, shift)
				shiftAnimationTrack(selector.Amount, shift)
			}
			shiftAnimationTracks(animator.Properties, shift)
		}
	}
}

func shiftAnimationTracks(tracks []AnimationTrack, shift int64) {
	for index := range tracks {
		shiftAnimationTrack(&tracks[index], shift)
	}
}

func shiftAnimationTrack(track *AnimationTrack, shift int64) {
	if track == nil || shift == 0 {
		return
	}
	for index := range track.Keyframes {
		track.Keyframes[index].Frame += shift
	}
}
