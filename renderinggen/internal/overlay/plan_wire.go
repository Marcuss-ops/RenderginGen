package overlay

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Marshal serializes the typed plan once at the Chronon boundary.
func (p *Plan) Marshal() ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("overlay: cannot marshal a nil render plan")
	}
	clone := *p
	clone.Schema, clone.Version = RenderPlanWireVersion(&clone)
	return json.Marshal(&clone)
}

// MarshalIndent serializes the same canonical plan representation using the
// formatting used by command-line artifacts.
func (p *Plan) MarshalIndent(prefix, indent string) ([]byte, error) {
	compact, err := p.Marshal()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, prefix, indent); err != nil {
		return nil, fmt.Errorf("overlay: indent render plan: %w", err)
	}
	return out.Bytes(), nil
}

func textAnimatorsNeedV3(animators []TextAnimator) bool {
	for _, animator := range animators {
		for _, property := range animator.Properties {
			switch property.Property {
			case "fill_color":
				return true
			}
		}
	}
	return false
}

func layerAnimationNeedsV3(animation *LayerAnimation) bool {
	if animation == nil {
		return false
	}
	for _, track := range animation.Tracks {
		switch track.Property {
		case "stroke_width", "stroke_color", "blur":
			return true
		}
	}
	return false
}
