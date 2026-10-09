package overlay

import (
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// compilePhraseHighlightComponents lowers ChrononTemplate's accent shapes
// beside an already compiled phrase. The shape geometry is absolute catalog
// data and its animated properties use the same native layer-track contract as
// the phrase itself; no second renderer is introduced.
func compilePhraseHighlightComponents(src *semanticPlan, text Layer, definition motion.MotionDefinition) ([]Layer, error) {
	if len(definition.LayerComponents) == 0 {
		return nil, fmt.Errorf("overlay: phrase highlight motion %q has no layer components", definition.ID)
	}
	image := Layer{
		ID: text.ID, Type: "image", Size: []float64{float64(src.Width), float64(src.Height)},
		Position: []float64{0, 0}, StartFrame: text.StartFrame, DurationFrames: text.DurationFrames,
	}
	components := make([]Layer, 0, len(definition.LayerComponents))
	for _, component := range definition.LayerComponents {
		layer, err := compilePremiumComponent(src, image, definition, component)
		if err != nil {
			return nil, err
		}
		layer.ID = text.ID + ":accent:" + component.ID
		layer.DurationFrames = text.DurationFrames
		if len(component.Tracks) > 0 {
			entrance := entranceFrames(definition.Enter, definition.Exit, text.DurationFrames, false)
			tracks := make([]AnimationTrack, 0, len(component.Tracks))
			for _, authored := range component.Tracks {
				keys := make([]AnimationKeyframe, len(authored.Keyframes))
				for index, key := range authored.Keyframes {
					keys[index] = AnimationKeyframe{Frame: key.Frame, Value: key.Value}
				}

				tracks = append(tracks, AnimationTrack{
					Property: authored.Property, Easing: authored.Easing, Keyframes: keys,
				})
			}
			tracks = retimeMotionTracks(tracks, entrance, int64(definition.Enter))
			for index := range tracks {
				tracks[index].Component = component.ID
			}
			layer.Animation = phraseHighlightExitTracks(tracks, definition.Exit, text.DurationFrames)
		}
		components = append(components, layer)
	}
	textAnimation, err := animationForMotionTarget(definition.ID, nil, text.Text, text.DurationFrames, 0, text.ID, "important_phrase", true)
	if err != nil {
		return nil, err
	}
	applyMotionRouting(&text, textAnimation)
	out := make([]Layer, 0, len(components)+1)
	out = append(out, components...)
	out = append(out, text)
	return out, nil
}

func phraseHighlightExitTracks(tracks []AnimationTrack, exit int, duration int64) *LayerAnimation {
	if len(tracks) == 0 || duration <= 1 || exit <= 0 || int64(exit) >= duration {
		return &LayerAnimation{Tracks: tracks}
	}
	exitStart, last := duration-int64(exit), lastValidFrame(duration)
	for index := range tracks {
		track := &tracks[index]
		if len(track.Keyframes) == 0 || track.Keyframes[len(track.Keyframes)-1].Frame >= exitStart {
			continue
		}
		first := track.Keyframes[0].Value
		settled := track.Keyframes[len(track.Keyframes)-1].Value
		track.Keyframes = append(track.Keyframes,
			AnimationKeyframe{Frame: exitStart, Value: settled},
			AnimationKeyframe{Frame: last, Value: first})
	}
	return &LayerAnimation{Tracks: tracks}
}
