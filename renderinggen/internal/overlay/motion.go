package overlay

import (
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// The motion registry is the only authoring-motion → renderer-track lowering
// point. Chronon receives properties and keyframes, never motion names/units.
// Fail-closed: a registry miss or a compile error is returned to the caller
// and rejects the job — the historical swallow-and-return-nil turned a broken
// motion into a silently static overlay that passed the pipeline as healthy.
//
// ONE motion lowers to TWO renderer contracts, and both are always produced:
//
//   - composition-level tracks (Layer.Animation) — what the whole layer does;
//   - per-word/glyph animators (Layer.TextAnimators) — what each unit does.
//
// The two used to be mutually exclusive on the wire: the producer-selected
// motion path transported only the animators, the official preset path only the
// layer tracks. Half of every official motion was therefore dropped — a
// text-only motion (word_reveal, char_wave, ...) rendered with no composition
// motion at all, and an phrase_default phrase lost its glyph choreography. The
// certified phrase corpus (phrase_animations_v1) carries BOTH on the same
// layer, and the phrase_default phrase library authors both, so both is the contract.
//
// The lowering also OWNS the exit. MotionDefinition.Exit — and the preset's own
// exit window — has been carried through the catalog since the beginning and
// was never read by any code path, so every rendered overlay hard-cut at its
// last frame. A layer with room for the window now replays its entrance
// backwards over the last Exit frames (see appendExitTracks).
func lowerMotion(id string, enter, exit int, params motion.MotionParams, text string, duration int64) (*LayerAnimation, error) {
	if id == "" {
		return nil, nil
	}
	plugin, err := motion.Registry.Resolve(id)
	if err != nil {
		return nil, fmt.Errorf("overlay: resolve motion %q: %w", id, err)
	}
	if plugin == nil {
		return nil, fmt.Errorf("overlay: motion %q resolved to no plugin", id)
	}
	// The caller's windows win (an official preset owns its own entrance and
	// exit); a producer-selected motion_id supplies only the exit fallback, so
	// the motion's registered windows fill the gaps.
	if declarative, ok := plugin.(motion.DeclarativePlugin); ok {
		if enter <= 0 {
			enter = declarative.Definition.Enter
		}
		if exit <= 0 {
			exit = declarative.Definition.Exit
		}
	}
	entrance := entranceFrames(enter, exit, duration)
	ctx := motion.MotionContext{Text: text, DurationFrames: entrance}
	tracks, err := plugin.Compile(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("overlay: compile motion %q: %w", id, err)
	}
	animation := &LayerAnimation{Tracks: retimeMotionTracks(fromMotionTracks(tracks), entrance)}
	if textPlugin, ok := plugin.(motion.TextMotionPlugin); ok {
		definitions, err := textPlugin.CompileText(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("overlay: compile text motion %q: %w", id, err)
		}
		animation.TextAnimators = retimeTextAnimators(fromTextMotionDefinitions(definitions, entrance), entrance)
	}
	animation.Tracks = appendExitTracks(animation.Tracks, exit, duration)
	return animation, nil
}

// entranceFrames is how many frames of a layer the entrance may occupy once the
// exit window is reserved. The authored window is the ceiling — a preset's
// 72-frame entrance does not stretch on a longer layer — while layer-minus-exit
// is the cap, so a short overlay compresses its entrance instead of letting
// keyframes run past the layer boundary (which Chronon rejects, and which used
// to leave a 0.7 s phrase carrying keyframes authored for 3 s).
func entranceFrames(authored, exitFrames int, duration int64) int64 {
	if duration <= 0 {
		if authored > 0 {
			return int64(authored)
		}
		return 0
	}
	target := duration
	if exitFrames > 0 && int64(exitFrames) < duration {
		target = duration - int64(exitFrames)
	}
	if authored > 0 && int64(authored) < target {
		target = int64(authored)
	}
	if target < 1 {
		target = 1
	}
	return target
}

// appendExitTracks gives a layer a real OUT. The out replays the entrance
// backwards inside the last exitFrames frames of the layer: the geometry each
// track settled on at the end of the entrance returns to the value it started
// from, so a phrase that slides up in slides back down out instead of cutting.
//
// Opacity is the one track that is not mirrored: a layer that leaves must end
// transparent, so its out is always 1 -> 0 (synthesized when the entrance had no
// opacity track). Replaying an authored opacity curve backwards could otherwise
// end a layer half visible — the exact failure an editor reads as "no exit
// animation". A layer with no room for the window is left untouched rather than
// given overlapping keyframes.
func appendExitTracks(tracks []AnimationTrack, exitFrames int, duration int64) []AnimationTrack {
	if exitFrames <= 0 || duration <= 1 || int64(exitFrames) >= duration {
		return tracks
	}
	start, last := duration-int64(exitFrames), lastValidFrame(duration)
	out := make([]AnimationTrack, 0, len(tracks)+1)
	hasOpacity := false
	for _, track := range tracks {
		if len(track.Keyframes) == 0 {
			continue
		}
		if track.Property == "opacity" {
			hasOpacity = true
			track.Keyframes = append(track.Keyframes,
				AnimationKeyframe{Frame: start, Value: 1.0},
				AnimationKeyframe{Frame: last, Value: 0.0})
			out = append(out, track)
			continue
		}
		track.Keyframes = append(track.Keyframes,
			AnimationKeyframe{Frame: start, Value: track.Keyframes[len(track.Keyframes)-1].Value},
			AnimationKeyframe{Frame: last, Value: track.Keyframes[0].Value})
		out = append(out, track)
	}
	if !hasOpacity {
		// The motion only animated position/scale, so the exit needs its own
		// opacity track — and that track must be LAYER-RELATIVE like every
		// other one: keyframes are measured from the layer's first visible
		// frame, so a track whose first keyframe sits at `start` animates a
		// range that does not include the beginning of the layer. That is both
		// an invalid plan (TestFinal_AnimationFirstMiddleLastFrame: "first
		// keyframe at 24, want 0 (layer-relative)") and ambiguous for a
		// renderer that holds the value before the first keyframe: the card
		// would be born already opaque instead of fading out of nothing.
		//
		// Anchoring the corridor at frame 0 preserves the intended reading —
		// fully visible from the first frame until the exit begins — while
		// making it explicit instead of implied.
		out = append(out, AnimationTrack{Property: "opacity", Easing: "in_out_sine", Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: 1.0}, {Frame: start, Value: 1.0}, {Frame: last, Value: 0.0},
		}})
	}
	return out
}

// retimeTextAnimators keeps per-unit property keyframes inside the concrete
// entrance window exactly like retimeMotionTracks does for layer tracks. A
// selector sweep is already authored against that window by
// fromTextMotionDefinitions and is left alone.
func retimeTextAnimators(animators []TextAnimator, duration int64) []TextAnimator {
	if duration <= 0 || len(animators) == 0 {
		return animators
	}
	var source int64
	for _, animator := range animators {
		for _, track := range animator.Properties {
			for _, keyframe := range track.Keyframes {
				if keyframe.Frame > source {
					source = keyframe.Frame
				}
			}
		}
	}
	targetLast := lastValidFrame(duration)
	for i := range animators {
		for j := range animators[i].Properties {
			for k := range animators[i].Properties[j].Keyframes {
				keyframe := &animators[i].Properties[j].Keyframes[k]
				if source > 0 {
					keyframe.Frame = keyframe.Frame * targetLast / source
				}
			}
			clampAnimationTrack(&animators[i].Properties[j], duration)
		}
		for j := range animators[i].Selectors {
			clampTextSelectorTracks(&animators[i].Selectors[j], duration)
		}
	}
	return animators
}

// resolveMotion is the layer-track-only view of the shared lowering. It keeps
// the authored motion windows of the definition it is handed and, without a
// concrete layer duration, emits no exit window.
func resolveMotion(m MotionDefinition) ([]AnimationTrack, error) {
	animation, err := lowerMotion(m.ID, m.Enter, m.Exit, nil, "", 0)
	if err != nil {
		return nil, err
	}
	if animation == nil {
		return nil, nil
	}
	return animation.Tracks, nil
}

func retimeMotionTracks(tracks []AnimationTrack, duration int64) []AnimationTrack {
	if duration <= 0 {
		return tracks
	}
	var sourceDuration int64
	for _, track := range tracks {
		for _, keyframe := range track.Keyframes {
			if keyframe.Frame > sourceDuration {
				sourceDuration = keyframe.Frame
			}
		}
	}
	if sourceDuration <= 0 || sourceDuration == duration {
		return clampMotionTracks(tracks, duration)
	}
	targetLast := lastValidFrame(duration)
	for i := range tracks {
		for j := range tracks[i].Keyframes {
			tracks[i].Keyframes[j].Frame = tracks[i].Keyframes[j].Frame * targetLast / sourceDuration
		}
	}
	return clampMotionTracks(tracks, duration)
}

// clampMotionTracks keeps preset-authored keyframes inside the concrete
// layer duration. A semantic overlay can be shorter than the catalog's
// default enter duration (for example a brief spoken entity); Chronon rejects
// any keyframe beyond that layer/composition boundary.
func clampMotionTracks(tracks []AnimationTrack, duration int64) []AnimationTrack {
	if duration <= 0 {
		return tracks
	}
	for i := range tracks {
		clampAnimationTrack(&tracks[i], duration)
	}
	return tracks
}

// lastValidFrame is the single frame-boundary authority for all animation
// tracks. A duration is a count of frames, so the valid interval is
// [0, duration), never [0, duration].
func lastValidFrame(duration int64) int64 {
	if duration <= 1 {
		return 0
	}
	return duration - 1
}

func clampAnimationTrack(track *AnimationTrack, duration int64) {
	if track == nil || duration <= 0 {
		return
	}
	last := lastValidFrame(duration)
	for i := range track.Keyframes {
		if track.Keyframes[i].Frame < 0 {
			track.Keyframes[i].Frame = 0
		} else if track.Keyframes[i].Frame > last {
			track.Keyframes[i].Frame = last
		}
	}
}

func clampTextSelectorTracks(selector *TextSelector, duration int64) {
	if selector == nil || duration <= 0 {
		return
	}
	clampAnimationTrack(selector.Start, duration)
	clampAnimationTrack(selector.End, duration)
	clampAnimationTrack(selector.Offset, duration)
	clampAnimationTrack(selector.Amount, duration)
}

// animationForPreset is the shared lowering path for the official catalog. The
// preset owns both of its windows (the entrance it declares and the exit), and
// the same pass produces the layer tracks AND the text animators, so a preset's
// two halves can never drift apart on the wire again.
func animationForPreset(d PresetDefinition, text string, duration int64) (*LayerAnimation, error) {
	return lowerMotion(d.Motion.ID, d.Motion.Enter, d.Motion.Exit, nil, text, duration)
}

// withPhraseEntryExit adds a visible layer fade around the phrase's selected
// text motion. The catalog MotionID owns the phrase-specific entrance; this
// shared envelope makes entry and exit explicit for every important phrase,
// including text-only selector motions that have no layer tracks of their
// own. Existing opacity tracks are replaced so two curves cannot fight over
// the same layer property.
func withPhraseEntryExit(animation *LayerAnimation, duration int64) *LayerAnimation {
	if duration < 3 {
		return animation
	}
	if animation == nil {
		animation = &LayerAnimation{}
	}
	transitionFrames := duration / 4
	if transitionFrames > 8 {
		transitionFrames = 8
	}
	if transitionFrames < 1 {
		transitionFrames = 1
	}
	enterEnd := transitionFrames
	exitStart := duration - transitionFrames - 1
	if exitStart < enterEnd {
		exitStart = enterEnd
	}
	tracks := animation.Tracks[:0]
	for _, track := range animation.Tracks {
		if track.Property != "opacity" {
			tracks = append(tracks, track)
		}
	}
	keyframes := []AnimationKeyframe{
		{Frame: 0, Value: 0.0},
		{Frame: enterEnd, Value: 1.0},
	}
	if exitStart > enterEnd {
		keyframes = append(keyframes, AnimationKeyframe{Frame: exitStart, Value: 1.0})
	}
	keyframes = append(keyframes, AnimationKeyframe{Frame: lastValidFrame(duration), Value: 0.0})
	tracks = append(tracks, AnimationTrack{Property: "opacity", Easing: "linear", Keyframes: keyframes})
	animation.Tracks = tracks
	return animation
}

func fromMotionTracks(src []motion.AnimationTrack) []AnimationTrack {
	tracks := make([]AnimationTrack, len(src))
	for i, t := range src {
		tracks[i] = AnimationTrack{Property: t.Property, Easing: t.Easing, Keyframes: make([]AnimationKeyframe, len(t.Keyframes))}
		for j, k := range t.Keyframes {
			tracks[i].Keyframes[j] = AnimationKeyframe{Frame: k.Frame, Value: k.Value}
		}
	}
	return tracks
}

func animationUses3D(animation *LayerAnimation) bool {
	if animation == nil {
		return false
	}
	for _, track := range animation.Tracks {
		switch track.Property {
		// Keep in sync with Chronon3d/src/render_plan/render_plan_decoder.cpp:is_3d_property.
		// Only position_z / rotation_x / rotation_y require enable_3d; rotation_z is
		// in-plane (2D) and must NOT force the 3D/projected path which clears
		// the canvas to black when no camera is present.
		case "position_z", "rotation_x", "rotation_y":
			return true
		}
	}
	return false
}

func fromTrackDefinitions(src []motion.TrackDefinition) []AnimationTrack {
	result := make([]AnimationTrack, len(src))
	for i, t := range src {
		result[i] = AnimationTrack{Property: t.Property, Easing: t.Easing, Keyframes: make([]AnimationKeyframe, len(t.Keyframes))}
		for j, k := range t.Keyframes {
			result[i].Keyframes[j] = AnimationKeyframe{Frame: k.Frame, Value: k.Value}
		}
	}
	return result
}

func fromTextMotionDefinitions(src []motion.TextAnimatorDefinition, duration int64) []TextAnimator {
	result := make([]TextAnimator, 0, len(src))
	for _, definition := range src {
		selector := TextSelector{
			ID: definition.ID + "_selector", Unit: definition.Selector.Kind,
			Shape: definition.Selector.Shape, Order: definition.Selector.Order, Combine: "replace",
			ExcludeSpaces: true,
		}
		if definition.Selector.Kind == "" {
			selector.Unit = "glyph"
		}
		if selector.Shape == "" {
			selector.Shape = "smooth"
		}
		if selector.Order == "" {
			selector.Order = "forward"
		}
		// A selector sweep is the renderer-neutral form of stagger: Chronon
		// evaluates the animated range per glyph/word every frame.
		// For reveals (word_reveal, character_cascade), animating start from 0 -> 100
		// with property values (opacity: 0, position_y: offset) makes glyphs start hidden/offset
		// and progressively drop to their baseline as start sweeps past them.
		if definition.Selector.Stagger > 0 {
			sweepDuration := lastValidFrame(duration)
			if sweepDuration > MaxStaggerSweepFrames {
				sweepDuration = MaxStaggerSweepFrames
			}
			selector.Start = &AnimationTrack{Property: "start", Easing: "out_cubic", Keyframes: []AnimationKeyframe{
				{Frame: 0, Value: 0.0}, {Frame: sweepDuration, Value: 100.0},
			}}
		}
		animator := TextAnimator{ID: definition.ID, Selectors: []TextSelector{selector}, Properties: fromTrackDefinitions(definition.Properties)}
		result = append(result, animator)
	}
	return result
}

func resolveLayout(l PresetLayout, boxWidth, boxHeight, canvasWidth, canvasHeight int) []float64 {
	if boxWidth <= 0 {
		boxWidth = DefaultTextBoxWidth
	}
	if boxHeight <= 0 {
		boxHeight = DefaultTextBoxHeight
	}
	x, y := float64(canvasWidth-boxWidth)/2, float64(canvasHeight-boxHeight)/2
	switch l.Anchor {
	case "lower_third":
		x, y = AnchorSafeAreaFraction*float64(canvasWidth), AnchorLowerThirdYFraction*float64(canvasHeight)
	case "safe_area":
		x, y = AnchorSafeAreaFraction*float64(canvasWidth), AnchorSafeAreaFraction*float64(canvasHeight)
	case "image_left":
		x = 0
	case "image_right":
		x = float64(canvasWidth - boxWidth)
	case "bottom_right":
		x, y = float64(canvasWidth-boxWidth), float64(canvasHeight-boxHeight)
	}
	// Image anchors describe the placement of the whole image card. Do not
	// let generic text alignment override an image anchor.
	if l.Alignment == "center" && l.Anchor != "image_left" && l.Anchor != "image_right" && l.Anchor != "bottom_right" {
		x = (float64(canvasWidth) - float64(boxWidth)) / 2
	}
	return []float64{x, y}
}

// Chronon positions image layers around the composition center, while the
// preset/layout contract uses top-left canvas coordinates. Keep text on the
// existing top-left contract and translate only image cards here.
func resolveImageLayout(l PresetLayout, boxWidth, boxHeight, canvasWidth, canvasHeight int) []float64 {
	pos := resolveLayout(l, boxWidth, boxHeight, canvasWidth, canvasHeight)
	return []float64{
		pos[0] + float64(boxWidth)/2 - float64(canvasWidth)/2,
		pos[1] + float64(boxHeight)/2 - float64(canvasHeight)/2,
	}
}

// resolveTextLayout is the canonical text layer position: the canvas centre, in
// ABSOLUTE canvas coordinates.
//
// The engine (render_plan_compiler_animation.cpp:apply_layer_primitives) treats
// a text layer's position as the absolute canvas coordinate of the layer centre
// and subtracts canvas/2 itself, so the canonical centred text is [w/2, h/2],
// not [0,0]. [0,0] under that contract is the canvas corner — which is where
// every plan that declared a style but no authored placement used to land.
//
// The canvas is an argument because the value depends on it. An earlier
// signature accepted five arguments and discarded all of them, promising a
// resolution the function did not perform.
func resolveTextLayout(canvasW, canvasH int) []float64 {
	return []float64{float64(canvasW) / 2, float64(canvasH) / 2}
}
