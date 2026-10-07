package overlay

import (
	"fmt"
	"math"
	"sort"

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
	// The caller's windows win (an official preset owns its own entrance and
	// exit); a producer-selected motion_id supplies only the exit fallback, so
	// the motion's registered windows fill the gaps.
	authoredEnter := 0
	if definition := resolved.definition; definition != nil {
		authoredEnter = definition.Enter
		if enter <= 0 {
			enter = definition.Enter
		}
		if exit <= 0 {
			exit = definition.Exit
		}
	}
	// ChrononTemplate product short-phrase recipes author one complete
	// enter/hold/exit timeline. Treating those frames as an entrance and then
	// collapsing every keyframe after Definition.Enter used to collapse the
	// authored opacity 0 at the end onto the opacity 1 entrance frame. The
	// result was black/transparent text for most of the clip, then a late pop
	// or no visible animation. Retiming the authored timeline over the phrase
	// lifetime preserves its complete choreography and exit.
	fullPhraseTimeline := resolved.definition != nil && resolved.definition.Category == "short_phrase_style"
	entrance := entranceFrames(enter, exit, duration, phraseEntranceFloor)
	if fullPhraseTimeline && duration > 0 {
		entrance = duration
	}
	if phraseEntranceFloor && duration > 0 && exit > 0 {
		// The phrase entrance duration is invariant (one third of its window).
		// When a caller's exit would overlap it, shorten the exit rather than
		// stretching or clipping the entrance.
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
		animationTracks = retimeFullMotionTimeline(animationTracks, duration)
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
		animation.TextAnimators = retimeTextAnimators(textAnimators, entrance)
	}
	if !fullPhraseTimeline {
		animation.Tracks = appendExitTracks(animation.Tracks, exit, duration)
	}
	return animation, nil
}

// retimeFullMotionTimeline fits every authored keyframe to the concrete layer
// lifetime. It is used for ChrononTemplate recipes whose tracks already
// contain their enter, hold and exit phases.
func retimeFullMotionTimeline(tracks []AnimationTrack, duration int64) []AnimationTrack {
	if duration <= 0 || len(tracks) == 0 {
		return tracks
	}
	var sourceLast int64
	for _, track := range tracks {
		for _, keyframe := range track.Keyframes {
			if keyframe.Frame > sourceLast {
				sourceLast = keyframe.Frame
			}
		}
	}
	if sourceLast <= 0 {
		return clampMotionTracks(tracks, duration)
	}
	targetLast := lastValidFrame(duration)
	for i := range tracks {
		for j := range tracks[i].Keyframes {
			tracks[i].Keyframes[j].Frame = tracks[i].Keyframes[j].Frame * targetLast / sourceLast
		}
		clampAnimationTrack(&tracks[i], duration)
	}
	return tracks
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

func retimeMotionTracks(tracks []AnimationTrack, duration, authoredEnter int64) []AnimationTrack {
	if duration <= 0 {
		return tracks
	}
	if authoredEnter <= 0 {
		for _, track := range tracks {
			for _, keyframe := range track.Keyframes {
				if keyframe.Frame > authoredEnter {
					authoredEnter = keyframe.Frame
				}
			}
		}
	}
	if authoredEnter <= 0 || authoredEnter == duration {
		return clampMotionTracks(tracks, duration)
	}
	targetLast := lastValidFrame(duration)
	for i := range tracks {
		for j := range tracks[i].Keyframes {
			frame := tracks[i].Keyframes[j].Frame
			if frame > authoredEnter {
				// Catalogs may append long hold keyframes after the entrance.
				// They define a settled state, not a longer entrance; collapse
				// them to the entrance's final frame before appending the exit.
				tracks[i].Keyframes[j].Frame = targetLast
				continue
			}
			tracks[i].Keyframes[j].Frame = frame * targetLast / authoredEnter
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
	// Retiming and clamping can collapse authored hold keyframes onto the
	// same frame (for example an entrance endpoint and a later settled hold).
	// Chronon requires unique frames; keep the last authored value when that
	// happens so the settled state wins at the collapsed boundary.
	if len(track.Keyframes) < 2 {
		return
	}
	unique := track.Keyframes[:0]
	for _, keyframe := range track.Keyframes {
		if len(unique) > 0 && unique[len(unique)-1].Frame == keyframe.Frame {
			unique[len(unique)-1] = keyframe
			continue
		}
		unique = append(unique, keyframe)
	}
	track.Keyframes = unique
}

func clampTextSelectorTracks(selector *TextSelector, duration int64) {
	if selector == nil || duration <= 0 {
		return
	}
	clampAnimationTrack(selector.Start, duration)
	clampAnimationTrack(selector.End, duration)
	clampAnimationTrack(selector.Offset, duration)
	clampAnimationTrack(selector.Amount, duration)
	// Clamping a sweep whose authored extent equals the last valid frame
	// collapses its keyframes onto that frame (the 95-frame layer with a
	// 0..95 sweep: every authored keyframe clamps to 95, leaving duplicates
	// that Chronon's strict decoder rejects). Coalesce to the LAST value so
	// the sweep keeps its settled 100 semantics instead of restarting.
	for _, track := range []*AnimationTrack{selector.Start, selector.End, selector.Offset, selector.Amount} {
		if track == nil || len(track.Keyframes) < 2 {
			continue
		}
		unique := track.Keyframes[:0]
		for _, keyframe := range track.Keyframes {
			if len(unique) > 0 && unique[len(unique)-1].Frame == keyframe.Frame {
				unique[len(unique)-1] = keyframe
				continue
			}
			unique = append(unique, keyframe)
		}
		track.Keyframes = unique
	}
}

// animationForPreset is the shared lowering path for the official catalog. The
// preset owns both of its windows (the entrance it declares and the exit), and
// the same pass produces the layer tracks AND the text animators, so a preset's
// two halves can never drift apart on the wire again.
func animationForPreset(d PresetDefinition, text string, duration int64, phraseFloor ...bool) (*LayerAnimation, error) {
	isPhrase := len(phraseFloor) > 0 && phraseFloor[0]
	return lowerMotion(d.Motion.ID, d.Motion.Enter, d.Motion.Exit, nil, text, duration, isPhrase)
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
	layer.Enable3D = layerUses3D(animation)
	if len(animation.Tracks) > 0 {
		layer.Animation = animation
	}
	if len(animation.TextAnimators) > 0 {
		layer.TextAnimators = animation.TextAnimators
	}
}

func fromTextMotionDefinitions(src []motion.TextAnimatorDefinition, duration int64) ([]TextAnimator, error) {
	result := make([]TextAnimator, 0, len(src))
	for _, definition := range src {
		selector := TextSelector{
			ID: definition.ID + "_selector", Unit: definition.Selector.Kind,
			Shape: definition.Selector.Shape, Order: definition.Selector.Order, Combine: "replace",
			ExcludeSpaces: true,
		}
		if definition.Selector.RangeStart != nil && definition.Selector.RangeEnd != nil {
			endFrame := lastValidFrame(duration)
			if endFrame < 1 {
				endFrame = 1
			}
			selector.Start = &AnimationTrack{Property: "start", Keyframes: []AnimationKeyframe{{Frame: 0, Value: *definition.Selector.RangeStart}, {Frame: endFrame, Value: *definition.Selector.RangeStart}}}
			selector.End = &AnimationTrack{Property: "end", Keyframes: []AnimationKeyframe{{Frame: 0, Value: *definition.Selector.RangeEnd}, {Frame: endFrame, Value: *definition.Selector.RangeEnd}}}
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

// sampleTextPropertyTrack bakes catalog easing into frame-sampled linear
// keyframes. Chronon intentionally accepts no easing metadata on per-glyph
// property tracks, so forwarding a catalog's "out_cubic" made phrase overlays
// fail during plan validation.
func sampleTextPropertyTrack(track motion.TrackDefinition) (AnimationTrack, error) {
	if len(track.Keyframes) == 0 {
		return AnimationTrack{}, fmt.Errorf("no keyframes")
	}
	keys := append([]motion.AnimationKeyframe(nil), track.Keyframes...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].Frame < keys[j].Frame })
	for i := 1; i < len(keys); i++ {
		if keys[i].Frame <= keys[i-1].Frame {
			return AnimationTrack{}, fmt.Errorf("keyframe frames must be unique and increasing")
		}
	}
	if len(keys) == 1 {
		return AnimationTrack{Property: track.Property, Easing: "linear", Keyframes: []AnimationKeyframe{{Frame: keys[0].Frame, Value: keys[0].Value}}}, nil
	}
	first, last := keys[0].Frame, keys[len(keys)-1].Frame
	if first < 0 || last-first > 65535 {
		return AnimationTrack{}, fmt.Errorf("sample window [%d,%d] exceeds Chronon's 65536-keyframe limit", first, last)
	}
	out := AnimationTrack{Property: track.Property, Easing: "linear", Keyframes: make([]AnimationKeyframe, 0, last-first+1)}
	segment := 0
	for frame := first; frame <= last; frame++ {
		for segment+1 < len(keys)-1 && frame > keys[segment+1].Frame {
			segment++
		}
		a, err := numericComponents(keys[segment].Value)
		if err != nil {
			return AnimationTrack{}, err
		}
		b, err := numericComponents(keys[segment+1].Value)
		if err != nil {
			return AnimationTrack{}, err
		}
		if len(a) != len(b) {
			return AnimationTrack{}, fmt.Errorf("keyframe value arity changed")
		}
		rawT := float64(frame-keys[segment].Frame) / float64(keys[segment+1].Frame-keys[segment].Frame)
		t, err := easingValue(track.Easing, rawT)
		if err != nil {
			return AnimationTrack{}, err
		}
		values := make([]float64, len(a))
		for i := range values {
			values[i] = a[i] + (b[i]-a[i])*t
		}
		var value any = values
		if len(values) == 1 {
			value = values[0]
		}
		out.Keyframes = append(out.Keyframes, AnimationKeyframe{Frame: frame, Value: value})
	}
	return out, nil
}

func numericComponents(value any) ([]float64, error) {
	switch v := value.(type) {
	case float64:
		return []float64{v}, nil
	case float32:
		return []float64{float64(v)}, nil
	case int:
		return []float64{float64(v)}, nil
	case int64:
		return []float64{float64(v)}, nil
	case []float64:
		return append([]float64(nil), v...), nil
	case []any:
		out := make([]float64, len(v))
		for i, component := range v {
			n, ok := component.(float64)
			if !ok {
				return nil, fmt.Errorf("keyframe component %d is %T, want number", i, component)
			}
			out[i] = n
		}
		return out, nil
	default:
		return nil, fmt.Errorf("keyframe value is %T, want numeric scalar or vector", value)
	}
}

func easingValue(easing string, t float64) (float64, error) {
	switch easing {
	case "", "linear":
		return t, nil
	case "out_cubic":
		return 1 - math.Pow(1-t, 3), nil
	case "in_out_cubic":
		if t < 0.5 {
			return 4 * t * t * t, nil
		}
		return 1 - math.Pow(-2*t+2, 3)/2, nil
	case "in_out_sine":
		return -(math.Cos(math.Pi*t) - 1) / 2, nil
	case "out_expo":
		if t == 1 {
			return 1, nil
		}
		return 1 - math.Pow(2, -10*t), nil
	case "out_back":
		const c1, c3 = 1.70158, 2.70158
		return 1 + c3*math.Pow(t-1, 3) + c1*math.Pow(t-1, 2), nil
	case "per_word_land":
		const split, residual = 0.78, 0.07
		const slope = ((1 - residual) / split) * 0.5
		if t < split {
			x := t / split
			return (1 - residual) * (0.5*(1-math.Pow(1-x, 3)) + 0.5*x), nil
		}
		u := (t - split) / (1 - split)
		b := slope * (1 - split)
		c := 3*residual - 2*b
		d := b - 2*residual
		return 1 - residual + b*u + c*u*u + d*u*u*u, nil
	default:
		return 0, fmt.Errorf("unsupported easing %q", easing)
	}
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
