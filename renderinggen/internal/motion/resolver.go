package motion

type DeclarativePlugin struct{ Definition MotionDefinition }

func (p DeclarativePlugin) ID() string {
	if p.Definition.ID != "" {
		return p.Definition.ID
	}
	return p.Definition.Name
}
func (p DeclarativePlugin) Validate(params MotionParams) error {
	return ValidateDefinition(p.Definition)
}

func (p DeclarativePlugin) Compile(ctx MotionContext, params MotionParams) ([]AnimationTrack, error) {
	if err := p.Validate(params); err != nil {
		return nil, err
	}
	tracks := make([]AnimationTrack, 0, len(p.Definition.Tracks))
	for _, d := range p.Definition.Tracks {
		keyframes := append([]AnimationKeyframe(nil), d.Keyframes...)
		tracks = append(tracks, AnimationTrack{Property: d.Property, Keyframes: keyframes, Easing: d.Easing})
	}
	return tracks, nil
}

func (p DeclarativePlugin) CompileText(ctx MotionContext, params MotionParams) ([]TextAnimatorDefinition, error) {
	if err := p.Validate(params); err != nil {
		return nil, err
	}
	return append([]TextAnimatorDefinition(nil), p.Definition.TextAnimators...), nil
}

// CompileDefinition is useful to callers that load catalog definitions from JSON.
func CompileDefinition(d MotionDefinition, ctx MotionContext, params MotionParams) ([]AnimationTrack, error) {
	return DeclarativePlugin{Definition: d}.Compile(ctx, params)
}

func LegacyDefinition(name, unit string, enter, exit int) MotionDefinition {
	if name == "" || name == "static" || enter <= 0 {
		return MotionDefinition{ID: name, Name: name, Unit: unit, Enter: enter, Exit: exit}
	}
	property, start, end := "opacity", any(0.0), any(1.0)
	switch name {
	case "scale_drop":
		property, start = "scale", 0.85
	case "reveal_from_bottom", "slide_up":
		property, start, end = "position_y", 40.0, 0.0
	case "slide_in", "slide_left":
		property, start, end = "position_x", -40.0, 0.0
	case "slide_from_right":
		property, start, end = "position_x", 40.0, 0.0
	case "slide_down":
		property, start, end = "position_y", -40.0, 0.0
	case "slide_right":
		property, start, end = "position_x", 40.0, 0.0
	case "soft_pop":
		property, start = "scale", 0.9
	case "focus_in":
		property, start = "scale", 1.08
	case "scale_in":
		property, start = "scale", 0.85
	case "scale_out":
		property, start, end = "scale", 1.0, 0.0
	case "elastic_pop":
		return MotionDefinition{ID: name, Name: name, Unit: unit, Enter: enter, Exit: exit, Tracks: []TrackDefinition{
			{Property: "scale", Easing: "out_back", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.5}, {Frame: int64(enter / 2), Value: 1.12}, {Frame: int64(enter * 3 / 4), Value: 0.96}, {Frame: int64(enter), Value: 1.0}}},
			{Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: int64(enter / 3), Value: 1.0}}},
		}}
	case "bounce_in":
		return MotionDefinition{ID: name, Name: name, Unit: unit, Enter: enter, Exit: exit, Tracks: []TrackDefinition{{Property: "scale", Easing: "out_back", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.4}, {Frame: 7, Value: 1.15}, {Frame: 11, Value: 0.94}, {Frame: int64(enter), Value: 1.0}}}, {Property: "opacity", Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: 0.0}, {Frame: 5, Value: 1.0}}}}}
	}
	return MotionDefinition{ID: name, Name: name, Unit: unit, Enter: enter, Exit: exit, Tracks: []TrackDefinition{{Property: property, Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: start}, {Frame: int64(enter), Value: end}}}}}
}

var _ MotionPlugin = DeclarativePlugin{}
