// Package motion owns RenderingGen's renderer-neutral motion plugins.
// Motion IDs never cross into Chronon; text plugins lower to generic
// TextAnimator definitions (selector + property tracks).
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
	Kind    string `json:"kind,omitempty"`  // glyph, grapheme, character, word, line
	Shape   string `json:"shape,omitempty"` // square, ramp_up, ramp_down, triangle, round, smooth
	Order   string `json:"order,omitempty"` // forward, reverse, from_center, to_center, random
	Stagger int64  `json:"stagger,omitempty"`
}

type TextAnimatorDefinition struct {
	ID         string             `json:"id,omitempty"`
	Selector   SelectorDefinition `json:"selector"`
	Properties []TrackDefinition  `json:"properties"`
}

type StaggerDefinition struct {
	Frames int64 `json:"frames,omitempty"`
}

// MotionDefinition is the declarative motion contract. ID is the ONLY
// identity: the historical `Name` alias (read as a fallback by the registry,
// the overlay resolver and the preset catalog) has been removed, because two
// fields that can each name the same motion let one producer set only the
// other and silently fail to resolve at render time.
type MotionDefinition struct {
	ID            string                   `json:"id"`
	Category      string                   `json:"category,omitempty"`
	Targets       []string                 `json:"targets,omitempty"`
	Unit          string                   `json:"unit,omitempty"`
	Enter         int                      `json:"enter,omitempty"` // legacy preset timing
	Exit          int                      `json:"exit,omitempty"`
	Tracks        []TrackDefinition        `json:"tracks,omitempty"`
	TextAnimators []TextAnimatorDefinition `json:"text_animators,omitempty"`
	Selector      SelectorDefinition       `json:"selector,omitempty"`
	Stagger       StaggerDefinition        `json:"stagger,omitempty"`
}

type MotionPlugin interface {
	ID() string
	Validate(params MotionParams) error
	Compile(ctx MotionContext, params MotionParams) ([]AnimationTrack, error)
}

// TextMotionPlugin is optional: layer/image plugins keep using MotionPlugin,
// while text plugins can lower selectors and per-glyph properties as well.
type TextMotionPlugin interface {
	MotionPlugin
	CompileText(ctx MotionContext, params MotionParams) ([]TextAnimatorDefinition, error)
}

func ValidateDefinition(d MotionDefinition) error {
	if d.ID == "" {
		return fmt.Errorf("motion: definition has no id")
	}
	for _, t := range d.Tracks {
		if err := validateTrackDefinition(d.ID, "track", t); err != nil {
			return err
		}
	}
	for _, animator := range d.TextAnimators {
		if len(animator.Properties) == 0 {
			return fmt.Errorf("motion %q: text animator %q has no properties", d.ID, animator.ID)
		}
		for _, t := range animator.Properties {
			if err := validateTrackDefinition(d.ID, "text animator "+animator.ID, t); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateTrackDefinition protects the renderer boundary, where an empty or
// malformed track otherwise becomes a late render-plan failure. All authored
// tracks are layer-relative, so they must begin at frame zero and use strictly
// increasing frame numbers. A separate pack-level gate checks that the
// authored value curves are actually varying where a reveal is expected; this
// structural check keeps malformed timelines from reaching the renderer.
func validateTrackDefinition(motionID, owner string, t TrackDefinition) error {
	if t.Property == "" || len(t.Keyframes) < 2 {
		return fmt.Errorf("motion %q: %s requires a property and at least two keyframes", motionID, owner)
	}
	if t.Keyframes[0].Frame != 0 {
		return fmt.Errorf("motion %q: %s must start at frame 0, got %d", motionID, owner, t.Keyframes[0].Frame)
	}
	for i, keyframe := range t.Keyframes {
		if keyframe.Frame < 0 {
			return fmt.Errorf("motion %q: %s has negative keyframe %d", motionID, owner, keyframe.Frame)
		}
		if i > 0 && keyframe.Frame <= t.Keyframes[i-1].Frame {
			return fmt.Errorf("motion %q: %s keyframes are not strictly increasing at index %d", motionID, owner, i)
		}
	}
	return nil
}
