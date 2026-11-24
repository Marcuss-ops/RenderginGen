package overlay

// These helpers are kept for tests only. They used to live in production files
// even though no production path called them; deadcode -test cannot see that
// because the test binaries keep them alive. Keeping them here makes the
// production surface honest without deleting the behavioural coverage.

// isTextLikeKind derives policy eligibility from the semantic registry's
// rendering behavior instead of maintaining another list of text-oriented
// kinds. Entity cards retain text behavior when compiled without an image.
func isTextLikeKind(kind ItemKind) bool {
	behavior := behaviorOf(kind)
	return behavior == behaviorText || behavior == behaviorEntity
}

func animationUsesMultiImageRecipe(id string) bool {
	definition, err := resolveMotionDefinition(id)
	return err == nil && isMultiImageRecipe(definition)
}

// autoFitEntityPortrait covers a LANDSCAPE entity card image into the
// supported portrait box, keeping its height and center: the caption
// compiled from its bounds follows automatically. Vertical and square boxes
// are already caption-safe and are returned untouched — a square box the
// producer declared (box_width == box_height) is a producer decision, not a
// landscape box to be rescued into the portrait ratio.
func autoFitEntityPortrait(layer *Layer) bool {
	if layer == nil || len(layer.Size) < 2 || layer.Size[0] <= 0 || layer.Size[1] <= 0 {
		return false
	}
	if layer.Size[0] <= layer.Size[1] {
		return false
	}
	layer.Size = []float64{layer.Size[1] * entityPortraitRatio, layer.Size[1]}
	layer.Fit = FitCover
	return true
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

// compileSemantic is the internal one-shot entry (decode + lower). Production
// goes through CompileSemantic, which decodes the raw document exactly once and
// hands the decoded plan to compileSemanticDecoded.
func compileSemantic(raw []byte) (*Plan, []Asset, Stats, []string, error) {
	src, err := decodeSemanticPlan(raw)
	if err != nil {
		var zero Stats
		return nil, nil, zero, nil, err
	}
	return compileSemanticDecoded(&src, CompileOptions{})
}

// animationForMotion lowers a producer-selected motion_id through the shared
// spine (see lowerMotion). Semantic clips hold their resolved pose until the
// clip boundary; an exit is opt-in through motion_params.exit_frames.
func animationForMotion(id string, params map[string]any, textValue string, duration int64, presetExit int, phraseFloor ...bool) (*LayerAnimation, error) {
	isPhrase := len(phraseFloor) > 0 && phraseFloor[0]
	return animationForMotionTarget(id, params, textValue, duration, presetExit, "", "", isPhrase)
}

// animationForDefinition lowers a preset's motion without a concrete layer
// duration: the entrance is used exactly as authored and no exit window is
// emitted. Callers that know the layer duration use animationForPreset, which
// is the same lowering with both windows resolved against that duration.
func animationForDefinition(d PresetDefinition) (*LayerAnimation, error) {
	return animationForPreset(d, "", 0)
}
