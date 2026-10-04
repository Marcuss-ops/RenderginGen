package overlay

import "encoding/json"

// Marshal serializes the typed plan once at the Chronon boundary.
func (p *Plan) Marshal() ([]byte, error) {
	clone := *p
	hasV3 := clone.Camera != nil && len(clone.Layers) > 0
	for _, l := range p.Layers {
		if l.Type == "shape" || len(l.Effects) > 0 || l.Shape != nil || l.ScreenSpace ||
			len(l.EffectParamTracks) > 0 || l.Parent != "" || l.TransitionIn != nil || len(l.Masks) > 0 ||
			layerAnimationNeedsV3(l.Animation) {
			hasV3 = true
			break
		}
	}
	if hasV3 {
		clone.Schema = "chronon.render-plan.v3"
		clone.Version = 3
	}
	return json.Marshal(&clone)
}

func layerAnimationNeedsV3(animation *LayerAnimation) bool {
	if animation == nil {
		return false
	}
	for _, track := range animation.Tracks {
		switch track.Property {
		case "stroke_width", "stroke_color", "fill_color", "blur":
			return true
		}
	}
	return false
}
