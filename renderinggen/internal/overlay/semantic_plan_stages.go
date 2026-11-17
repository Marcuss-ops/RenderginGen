package overlay

// semantic_plan_stages.go owns the two plan-wide stages that run after item
// lowering: the full-frame overlays (grid and adjustment stack) and the final
// duration reconciliation. Both are independent of item order, which is why
// they are a separate stage instead of trailing statements inside the lowering.
// appendFullFrameStages appends the clip grid and lowers the clip effects.
// Effects are attached to the source video layer: that path is part of the
// ordinary layer effect stack and is compiled by Chronon without the
// effect-only adjustment-layer bridge, which has no timing fields in the v3
// wire contract and is not a reliable render path today. The grid participates
// in the composite like any other shape and is appended after the source.
func appendFullFrameStages(plan *Plan, src *semanticPlan) error {
	if src.ClipGrid != nil {
		grid, err := compileClipGrid(src.ClipGrid, src.Width, src.Height, plan.Canvas.DurationFrames, src.ForegroundScale)
		if err != nil {
			return err
		}
		plan.Layers = append(plan.Layers, grid)
	}
	if len(src.ClipEffects) == 0 {
		return nil
	}
	effects, err := resolveClipEffects(src.ClipEffects)
	if err != nil {
		return err
	}
	// Prefer the source plate: effects then travel through the ordinary video
	// layer effect pass. Fall back to the background plate for plans without a
	// source, and only then to the legacy adjustment layer.
	for _, id := range []string{"source", "background"} {
		for i := range plan.Layers {
			if plan.Layers[i].ID == id {
				plan.Layers[i].Effects = append(plan.Layers[i].Effects, effects...)
				return nil
			}
		}
	}
	plan.Layers = append(plan.Layers, Layer{ID: "clip__adjustment", Type: "adjustment", Effects: effects})
	return nil
}

// ensureClipGridOnTop pins the clip grid as the final composited layer.
//
// Chronon composites layers in declaration order, so the grid must come after
// every visual layer (source, card frame, items, watermark) to sit over the
// footage instead of only on the margins. The grid is appended in
// appendFullFrameStages today, but later stages may append layers; moving it
// here makes the paint order an explicit invariant rather than an accident of
// call ordering.
func ensureClipGridOnTop(plan *Plan) {
	for i, layer := range plan.Layers {
		if layer.ID != "clip__grid" || i == len(plan.Layers)-1 {
			continue
		}
		grid := layer
		plan.Layers = append(plan.Layers[:i], plan.Layers[i+1:]...)
		plan.Layers = append(plan.Layers, grid)
		return
	}
}

// finalizeLayerDurations makes every full-clip layer span the final canvas.
// Item layers keep their own windows; only the stages that are frame-wide are
// patched here.
func finalizeLayerDurations(plan *Plan, sourceLayerIndex int) {
	// Patch background duration to match the final canvas duration.
	if len(plan.Layers) > 0 && plan.Layers[0].ID == "background" {
		plan.Layers[0].DurationFrames = plan.Canvas.DurationFrames
	}
	// Patch source layer duration — it spans the full clip.
	if sourceLayerIndex >= 0 {
		plan.Layers[sourceLayerIndex].DurationFrames = plan.Canvas.DurationFrames
		if sourceLayerIndex+1 < len(plan.Layers) && plan.Layers[sourceLayerIndex+1].ID == "source__stroke" {
			plan.Layers[sourceLayerIndex+1].DurationFrames = plan.Canvas.DurationFrames
		}
	}
	// Patch subtitle, watermark and the effect-only adjustment layers — they
	// span the full clip. The adjustment timing stays off the Chronon wire (its
	// schema forbids timing fields), but the typed plan and prepared sidecar
	// must describe the same full-canvas lifetime.
	for i := range plan.Layers {
		switch plan.Layers[i].ID {
		case "clip__grid", "clip__adjustment":
			plan.Layers[i].DurationFrames = plan.Canvas.DurationFrames
		}
		if plan.Layers[i].DurationFrames == 0 {
			switch plan.Layers[i].ID {
			case "subtitles", "watermark":
				plan.Layers[i].DurationFrames = plan.Canvas.DurationFrames
			}
		}
	}
}
