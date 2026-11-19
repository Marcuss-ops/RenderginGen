package overlay

import (
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motionmath"
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
func lowerMotion(id string, enter, exit int, params motion.MotionParams, text string, duration int64, phraseEntranceFloor bool) (*LayerAnimation, error) {
	return lowerMotionForTarget(id, enter, exit, params, text, duration, phraseEntranceFloor, "", "")
}

func lowerMotionForTarget(id string, enter, exit int, params motion.MotionParams, text string, duration int64, phraseEntranceFloor bool, itemID, target string) (*LayerAnimation, error) {
	if id == "" {
		return nil, nil
	}
	resolved, err := resolveRegisteredMotion(id)
	if err != nil {
		if target != "" {
			return nil, fmt.Errorf("overlay: item %q motion %q: %w", itemID, id, err)
		}
		return nil, fmt.Errorf("overlay: resolve motion %q: %w", id, err)
	}
	return lowerResolvedMotion(id, resolved, enter, exit, params, text, duration, phraseEntranceFloor, itemID, target)
}

func lowerResolvedMotion(id string, resolved *resolvedMotion, enter, exit int, params motion.MotionParams, text string, duration int64, phraseEntranceFloor bool, itemID, target string) (*LayerAnimation, error) {
	if resolved == nil || resolved.plugin == nil {
		return nil, fmt.Errorf("overlay: motion %q resolved to no plugin", id)
	}
	if err := motionDeprecationError(id); err != nil {
		return nil, err
	}
	if target != "" && resolved.definition != nil && resolved.definition.Targets != nil &&
		!motionDefinitionAdmitsTarget(resolved.definition, target) {
		return nil, fmt.Errorf("overlay: item %q motion %q is not supported for target %q", itemID, id, target)
	}
	plugin := resolved.plugin
	disableExit := exit < 0
	if disableExit {
		exit = 0
	}
	// The caller's windows win (an official preset owns its own entrance and
	// exit); a producer-selected motion_id supplies only the exit fallback, so
	// the motion's registered windows fill the gaps.
	authoredEnter := 0
	if definition := resolved.definition; definition != nil {
		authoredEnter = definition.Enter
		if enter <= 0 {
			enter = definition.Enter
		}
		if exit <= 0 && !disableExit {
			exit = definition.Exit
		}
	}
	// ChrononTemplate product short-phrase recipes author an enter/hold/exit
	// timeline, but overlay clips are often shorter than the recipe's full
	// timeline. Keep the authored entrance and hold the resolved state for the
	// rest of the clip. Compressing the exit into the remaining frames made the
	// phrase slide or fade out while the overlay was still visible.
	fullPhraseTimeline := resolved.definition != nil && resolved.definition.Category == "short_phrase_style"
	entrance := entranceFrames(enter, exit, duration, phraseEntranceFloor)
	if fullPhraseTimeline && duration > 0 {
		entrance = duration
	}
	if phraseEntranceFloor && duration > 0 && exit > 0 {
		// The phrase entrance duration has already been bounded to its authored
		// window and the short transition cap. When a caller's exit would overlap
		// it, shorten the exit rather than stretching or clipping the entrance.
		maxExit := duration - entrance
		if int64(exit) > maxExit {
			exit = int(maxExit)
		}
	}
	ctx := motion.MotionContext{Text: text, DurationFrames: entrance}
	tracks, err := plugin.Compile(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("overlay: compile motion %q: %w", id, err)
	}
	animationTracks := fromMotionTracks(tracks)
	if fullPhraseTimeline {
		animationTracks = retimePhraseEntrance(animationTracks, int64(authoredEnter), duration)
	} else {
		animationTracks = retimeMotionTracks(animationTracks, entrance, int64(authoredEnter))
	}
	animation := &LayerAnimation{Tracks: animationTracks}
	if textPlugin, ok := plugin.(motion.TextMotionPlugin); ok {
		definitions, err := textPlugin.CompileText(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("overlay: compile text motion %q: %w", id, err)
		}
		textAnimators, err := fromTextMotionDefinitions(definitions, entrance)
		if err != nil {
			return nil, fmt.Errorf("overlay: lower text motion %q: %w", id, err)
		}
		if fullPhraseTimeline {
			animation.TextAnimators = retimePhraseTextEntrance(textAnimators, int64(authoredEnter), duration)
		} else {
			animation.TextAnimators = retimeTextAnimators(textAnimators, entrance)
		}
	}
	if !fullPhraseTimeline {
		animation.Tracks = appendExitTracks(animation.Tracks, exit, duration)
	}
	return animation, nil
}

// retimePhraseEntrance keeps only the authored entrance and holds its final
// value for the rest of the concrete layer lifetime. Recipe exits can exceed
// short overlay clips and must not move text out of frame at the clip edge.
func retimePhraseEntrance(tracks []AnimationTrack, authoredEnter, duration int64) []AnimationTrack {
	if duration <= 0 || len(tracks) == 0 {
		return tracks
	}
	for i := range tracks {
		tracks[i].Keyframes = retimePhraseKeyframes(tracks[i].Keyframes, authoredEnter, duration)
		clampAnimationTrack(&tracks[i], duration)
	}
	return tracks
}

func retimePhraseKeyframes(keyframes []AnimationKeyframe, authoredEnter, duration int64) []AnimationKeyframe {
	if authoredEnter <= 0 {
		return keyframes
	}
	// Some catalog recipes declare Enter a few frames before their final
	// entrance keyframe (for example Enter=90, opacity reaches 1 at frame 96).
	// Include that short settling tail so clipping the later exit cannot leave
	// the layer stuck at its initial low opacity.
	sourceLimit := authoredEnter + 15
	targetLast := lastValidFrame(duration)
	result := make([]AnimationKeyframe, 0, len(keyframes))
	for _, keyframe := range keyframes {
		if keyframe.Frame > sourceLimit {
			continue
		}
		keyframe.Frame = keyframe.Frame * targetLast / sourceLimit
		result = append(result, keyframe)
	}
	return result
}

func retimePhraseTextEntrance(animators []TextAnimator, authoredEnter, duration int64) []TextAnimator {
	for i := range animators {
		for j := range animators[i].Properties {
			animators[i].Properties[j].Keyframes = retimePhraseKeyframes(
				animators[i].Properties[j].Keyframes, authoredEnter, duration,
			)
			clampAnimationTrack(&animators[i].Properties[j], duration)
		}
		// Selector sweeps are authored against the recipe window by
		// fromTextMotionDefinitions, so like retimeTextAnimators they are only
		// clamped into the concrete layer bounds, never rescaled. Without this
		// a duration-1 phrase clip keeps selector start/end keyframes at frame
		// 1, outside the [0, duration) boundary Chronon accepts.
		for j := range animators[i].Selectors {
			clampTextSelectorTracks(&animators[i].Selectors[j], duration)
		}
	}
	return animators
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
	animation, err := lowerMotion(m.ID, m.Enter, m.Exit, nil, "", 0, false)
	if err != nil {
		return nil, err
	}
	if animation == nil {
		return nil, nil
	}
	return animation.Tracks, nil
}

// animationForPreset is the shared lowering path for the official catalog. The
// preset owns both of its windows (the entrance it declares and the exit), and
// the same pass produces the layer tracks AND the text animators, so a preset's
// two halves can never drift apart on the wire again.
func animationForPreset(d PresetDefinition, text string, duration int64, phraseFloor ...bool) (*LayerAnimation, error) {
	isPhrase := len(phraseFloor) > 0 && phraseFloor[0]
	return lowerMotion(d.Motion.ID, d.Motion.Enter, d.Motion.Exit, nil, text, duration, isPhrase)
}

// withPhraseEntryExit adds a visible layer fade-in and, when the producer
// reserves one, an opacity-only fade-out for the phrase's selected text motion.
// The catalog MotionID owns the phrase-specific entrance; this shared envelope
// makes entry/exit explicit for every important phrase, including text-only
// selector motions that have no layer tracks of their own. Existing opacity
// tracks are replaced so two curves cannot fight over the same layer property.
// Non-opacity tracks are never rewritten here, so the exit cannot replay the
// entrance and dwell on the authoring gray.
func withPhraseEntryExit(animation *LayerAnimation, duration, exitFrames int64) *LayerAnimation {
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
	tracks := animation.Tracks[:0]
	for _, track := range animation.Tracks {
		if track.Property != "opacity" {
			tracks = append(tracks, track)
		}
	}
	last := lastValidFrame(duration)
	keyframes := []AnimationKeyframe{
		{Frame: 0, Value: 0.0},
		{Frame: enterEnd, Value: 1.0},
	}
	if exitFrames > 0 {
		exitStart := duration - exitFrames
		if exitStart <= enterEnd {
			exitStart = enterEnd + 1
		}
		if exitStart < last {
			keyframes = append(keyframes,
				AnimationKeyframe{Frame: exitStart, Value: 1.0},
				AnimationKeyframe{Frame: last, Value: 0.0})
		}
	}
	if keyframes[len(keyframes)-1].Frame < last {
		keyframes = append(keyframes, AnimationKeyframe{Frame: last, Value: 1.0})
	}
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

// animationUses3D reports whether the layer half of a lowered motion carries a
// camera-backed property. See motion.IsCameraBacked3DProperty for the closed set
// (position_z / rotation_x / rotation_y); rotation_z is in-plane (2D) and must
// NOT force the 3D/projected path, which clears the canvas to black when no
// camera is present.
func animationUses3D(animation *LayerAnimation) bool {
	if animation == nil {
		return false
	}
	for _, track := range animation.Tracks {
		if motion.IsCameraBacked3DProperty(track.Property) {
			return true
		}
	}
	return false
}

// layerUses3D reports whether a whole lowered motion needs the camera-backed
// path, scanning both halves of it: the layer tracks AND every per-unit text
// animator's property tracks. This mirrors Chronon's
// render_plan_decoder.cpp:find_3d_track, which walks animation.tracks and then
// text_animators[].properties, and it closes the hole that scanning only the
// layer tracks left: a motion whose 3D-ness lives in a per-glyph track compiled
// to enable_3d=false, and the engine then refused the plan with
// "3D transform data requires enable_3d=true; offending track: position_z".
func layerUses3D(animation *LayerAnimation) bool {
	if animation == nil {
		return false
	}
	if animationUses3D(animation) {
		return true
	}
	for _, animator := range animation.TextAnimators {
		for _, track := range animator.Properties {
			if motion.IsCameraBacked3DProperty(track.Property) {
				return true
			}
		}
	}
	return false
}

// applyMotionRouting is the single lowering from a resolved motion to the
// layer's animated half: the layer tracks, the per-unit text animators and the
// camera-backed routing bit that has to describe BOTH of them. Assigning
// Enable3D from one half is how a 3D text animator used to reach the engine
// with enable_3d=false and be rejected.
func applyMotionRouting(layer *Layer, animation *LayerAnimation) {
	if animation == nil {
		return
	}
	if layer.Type == "text" && layer.ScreenSpace {
		// Screen-space text is placed as a complete, centred canvas box. Keep
		// its in-plane choreography (opacity, 2D position/scale/rotation and
		// per-word/glyph animators), but drop camera-backed tracks: the overlay
		// canvas has no scene camera, so a position_z or rotation_x/y track
		// would either shift the box or be rejected by Chronon's 3D decoder.
		animation = screenSpaceTextAnimation(animation)
		layer.Enable3D = false
		if animation == nil {
			return
		}
	}
	// Preserve a 3D requirement established by the layer's own geometry (for
	// example a georeferenced map plane with camera_move) while adding any 3D
	// requirement carried by this animation.
	layer.Enable3D = layer.Enable3D || layerUses3D(animation)
	if len(animation.Tracks) > 0 {
		layer.Animation = animation
	}
	if len(animation.TextAnimators) > 0 {
		layer.TextAnimators = animation.TextAnimators
	}
}

// screenSpaceTextAnimation keeps the animation that can be evaluated on the
// flat overlay canvas and removes only the camera-backed half. The previous
// blanket reset made every screen-space text static (including pure fades and
// word reveals), while Chronon's placement resolver already owns the canvas
// centre for this layer kind.
func screenSpaceTextAnimation(animation *LayerAnimation) *LayerAnimation {
	if animation == nil {
		return nil
	}
	filtered := &LayerAnimation{}
	for _, track := range animation.Tracks {
		if !motion.IsCameraBacked3DProperty(track.Property) {
			filtered.Tracks = append(filtered.Tracks, track)
		}
	}
	for _, animator := range animation.TextAnimators {
		kept := animator
		kept.Properties = nil
		for _, track := range animator.Properties {
			if !motion.IsCameraBacked3DProperty(track.Property) {
				kept.Properties = append(kept.Properties, track)
			}
		}
		if len(kept.Properties) > 0 {
			filtered.TextAnimators = append(filtered.TextAnimators, kept)
		}
	}
	if len(filtered.Tracks) == 0 && len(filtered.TextAnimators) == 0 {
		return nil
	}
	return filtered
}

func fromTextMotionDefinitions(src []motion.TextAnimatorDefinition, duration int64) ([]TextAnimator, error) {
	result := make([]TextAnimator, 0, len(src))
	for _, definition := range src {
		selector := TextSelector{
			ID: definition.ID + "_selector", Unit: definition.Selector.Kind,
			SemanticID: definition.Selector.SemanticID,
			Shape:      definition.Selector.Shape, Order: definition.Selector.Order, Combine: "replace",
			ExcludeSpaces: true,
		}
		if definition.Selector.RangeStart != nil && definition.Selector.RangeEnd != nil {
			endFrame := lastValidFrame(duration)
			if endFrame < 1 {
				endFrame = 1
			}
			selector.Start = &AnimationTrack{Keyframes: []AnimationKeyframe{{Frame: 0, Value: *definition.Selector.RangeStart}, {Frame: endFrame, Value: *definition.Selector.RangeStart}}}
			selector.End = &AnimationTrack{Keyframes: []AnimationKeyframe{{Frame: 0, Value: *definition.Selector.RangeEnd}, {Frame: endFrame, Value: *definition.Selector.RangeEnd}}}
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
				return nil, fmt.Errorf("selector sweep duration %d exceeds Chronon's %d-frame keyframe limit", sweepDuration, MaxStaggerSweepFrames)
			}
			// Selector timing animates the selector's start value itself. The
			// Chronon selector contract has no property field; putting "start"
			// here makes the strict decoder reject otherwise valid phrase cards.
			keyframes := make([]AnimationKeyframe, sweepDuration+1)
			for frame := int64(0); frame <= sweepDuration; frame++ {
				t := float64(frame) / float64(sweepDuration)
				// Bake the authored out-cubic sweep because selector tracks
				// accept explicit linear keyframes only.
				value := 1 - (1-t)*(1-t)*(1-t)
				keyframes[frame] = AnimationKeyframe{Frame: frame, Value: 100 * value}
			}
			selector.Start = &AnimationTrack{Easing: "linear", Keyframes: keyframes}
		}
		properties := make([]AnimationTrack, 0, len(definition.Properties))
		for _, track := range definition.Properties {
			sampled, err := sampleTextPropertyTrack(track)
			if err != nil {
				return nil, fmt.Errorf("text animator %q property %q: %w", definition.ID, track.Property, err)
			}
			properties = append(properties, sampled)
		}
		animator := TextAnimator{ID: definition.ID, Selectors: []TextSelector{selector}, Properties: properties}
		result = append(result, animator)
	}
	return result, nil
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

// resolveTextLayout returns the absolute canvas coordinate of a centered text
// layer. Chronon converts it to a center-relative LayerBuilder position by
// subtracting half the canvas, so the canvas midpoint is [w/2,h/2].
func resolveTextLayout(canvasW, canvasH int) []float64 {
	return []float64{float64(canvasW) / 2, float64(canvasH) / 2}
}

// The functions below are the boundary between the overlay AnimationTrack and
// the pure motionmath package. They preserve the historical in-place mutation
// semantics: callers that hold a *AnimationTrack or a *TextSelector observe the
// same object updated, not a replacement.

func toMotionMathTrack(track AnimationTrack) motionmath.Track {
	keyframes := make([]motionmath.Keyframe, len(track.Keyframes))
	for i, keyframe := range track.Keyframes {
		keyframes[i] = motionmath.Keyframe{Frame: keyframe.Frame, Value: keyframe.Value}
	}
	return motionmath.Track{
		Property:  track.Property,
		Easing:    track.Easing,
		Keyframes: keyframes,
		Component: track.Component,
	}
}

func fromMotionMathTrack(track motionmath.Track) AnimationTrack {
	keyframes := make([]AnimationKeyframe, len(track.Keyframes))
	for i, keyframe := range track.Keyframes {
		keyframes[i] = AnimationKeyframe{Frame: keyframe.Frame, Value: keyframe.Value}
	}
	return AnimationTrack{
		Property:  track.Property,
		Easing:    track.Easing,
		Keyframes: keyframes,
		Component: track.Component,
	}
}

func toMotionMathTracks(tracks []AnimationTrack) []motionmath.Track {
	out := make([]motionmath.Track, len(tracks))
	for i, track := range tracks {
		out[i] = toMotionMathTrack(track)
	}
	return out
}

func retimeMotionTracks(tracks []AnimationTrack, duration, authoredEnter int64) []AnimationTrack {
	if duration <= 0 {
		return tracks
	}
	converted := toMotionMathTracks(tracks)
	result := motionmath.Retime(converted, duration, authoredEnter)
	for i := range tracks {
		tracks[i] = fromMotionMathTrack(result[i])
	}
	return tracks
}

func lastValidFrame(duration int64) int64 {
	return motionmath.LastValidFrame(duration)
}

func clampAnimationTrack(track *AnimationTrack, duration int64) {
	if track == nil || duration <= 0 {
		return
	}
	converted := toMotionMathTrack(*track)
	motionmath.ClampTrack(&converted, duration)
	*track = fromMotionMathTrack(converted)
}

func clampTextSelectorTracks(selector *TextSelector, duration int64) {
	if selector == nil || duration <= 0 {
		return
	}
	converted := motionmath.Selector{}
	if selector.Start != nil {
		track := toMotionMathTrack(*selector.Start)
		converted.Start = &track
	}
	if selector.End != nil {
		track := toMotionMathTrack(*selector.End)
		converted.End = &track
	}
	if selector.Offset != nil {
		track := toMotionMathTrack(*selector.Offset)
		converted.Offset = &track
	}
	if selector.Amount != nil {
		track := toMotionMathTrack(*selector.Amount)
		converted.Amount = &track
	}
	motionmath.ClampSelector(&converted, duration)
	if selector.Start != nil && converted.Start != nil {
		*selector.Start = fromMotionMathTrack(*converted.Start)
	}
	if selector.End != nil && converted.End != nil {
		*selector.End = fromMotionMathTrack(*converted.End)
	}
	if selector.Offset != nil && converted.Offset != nil {
		*selector.Offset = fromMotionMathTrack(*converted.Offset)
	}
	if selector.Amount != nil && converted.Amount != nil {
		*selector.Amount = fromMotionMathTrack(*converted.Amount)
	}
}

func sampleTextPropertyTrack(track motion.TrackDefinition) (AnimationTrack, error) {
	keys := make([]motionmath.Keyframe, len(track.Keyframes))
	for i, keyframe := range track.Keyframes {
		keys[i] = motionmath.Keyframe{Frame: keyframe.Frame, Value: keyframe.Value}
	}
	sampled, err := motionmath.SamplePropertyTrack(track.Property, track.Easing, keys)
	if err != nil {
		return AnimationTrack{}, err
	}
	return fromMotionMathTrack(sampled), nil
}

func easingValue(easing string, t float64) (float64, error) {
	return motionmath.EasingValue(easing, t)
}
