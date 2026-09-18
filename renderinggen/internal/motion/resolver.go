package motion

// DeclarativePlugin adapts one catalog definition to the plugin interface the
// overlay lowering consumes. The definitions themselves come from the canonical
// catalog (see catalog.go); this file only adapts them, so a motion cannot be
// authored in two places.
type DeclarativePlugin struct{ Definition MotionDefinition }

func (p DeclarativePlugin) ID() string {
	return p.Definition.ID
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

var _ MotionPlugin = DeclarativePlugin{}
