package overlay

// semantic_plan_stages.go owns the two plan-wide stages that run after item
// lowering: the full-frame overlays (grid and adjustment stack) and the final
// duration reconciliation. Both are independent of item order, which is why
// they are a separate stage instead of trailing statements inside the lowering.
// appendFullFrameStages appends the clip grid and the adjustment stack. The
// grid participates in the composite like any other shape; adjustments consume
// every preceding layer and therefore come last.
func appendFullFrameStages(plan *Plan, src *semanticPlan) error {
	if src.ClipGrid != nil {
		grid, err := compileClipGrid(src.ClipGrid, src.Width, src.Height, plan.Canvas.DurationFrames)
		if err != nil {
			return err
		}
		plan.Layers = append(plan.Layers, grid)
	}
	// Adjustment layers consume the composite from every preceding layer.
	if len(src.ClipEffects) > 0 {
		effects, err := resolveClipEffects(src.ClipEffects)
		if err != nil {
			return err
		}
		plan.Layers = append(plan.Layers, Layer{ID: "clip__adjustment", Type: "adjustment", Effects: effects})
	}
	return nil
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
	// Patch subtitle and watermark layers — they span the full clip.
	for i := range plan.Layers {
		if plan.Layers[i].ID == "clip__grid" {
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
