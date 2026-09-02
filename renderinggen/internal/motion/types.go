// Package motion owns RenderingGen's renderer-neutral motion plugins.
// Chronon receives only expanded tracks; motion IDs never cross this boundary.
package motion

import "fmt"

type Params map[string]any

type MotionParams = Params

type MotionContext struct {
	Target         string
	Text           string
	DurationFrames int64
	CanvasWidth    int
	CanvasHeight   int
}

type AnimationTrack struct {
	Property  string              `json:"property"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
	Easing    string              `json:"easing,omitempty"`
}

type AnimationKeyframe struct {
	Frame int64 `json:"frame"`
	Value any   `json:"value"`
}

type TrackDefinition struct {
	Property  string              `json:"property"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
	Easing    string              `json:"easing,omitempty"`
}

type SelectorDefinition struct {
	Kind    string `json:"kind,omitempty"` // layer, line, word, glyph
	Stagger int64  `json:"stagger,omitempty"`
}

type StaggerDefinition struct {
	Frames int64 `json:"frames,omitempty"`
}

type MotionDefinition struct {
	ID       string             `json:"id"`
	Name     string             `json:"name,omitempty"` // legacy alias
	Category string             `json:"category,omitempty"`
	Targets  []string           `json:"targets,omitempty"`
	Unit     string             `json:"unit,omitempty"`
	Enter    int                `json:"enter,omitempty"` // legacy preset timing
	Exit     int                `json:"exit,omitempty"`
	Tracks   []TrackDefinition  `json:"tracks,omitempty"`
	Selector SelectorDefinition `json:"selector,omitempty"`
	Stagger  StaggerDefinition  `json:"stagger,omitempty"`
}

type MotionPlugin interface {
	ID() string
	Validate(params MotionParams) error
	Compile(ctx MotionContext, params MotionParams) ([]AnimationTrack, error)
}

func ValidateDefinition(d MotionDefinition) error {
	if d.ID == "" && d.Name == "" {
		return fmt.Errorf("motion: definition has no id")
	}
	for _, t := range d.Tracks {
		if t.Property == "" || len(t.Keyframes) == 0 {
			return fmt.Errorf("motion %q: track requires property and keyframes", d.ID)
		}
	}
	return nil
}
