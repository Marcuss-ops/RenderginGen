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
		// compileBackdropGridWithScale routes line grids through explicit
		// 0..width path commands. The generated `grid` shape used by
		// compileClipGrid is centred on its local origin and the renderer
		// clips it to the positive quadrant, so a full-canvas line grid only
		// painted the bottom-right quarter of the frame. Dot grids are still
		// delegated to compileClipGrid.
		grid, err := compileBackdropGridWithScale(src.ClipGrid, src.Width, src.Height, plan.Canvas.DurationFrames, src.ForegroundScale)
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

// ensureClipGridAboveSource pins the clip grid directly above the footage and
// below every text layer.
//
// Chronon composites layers in declaration order. A grid appended last sits on
// top of the card stroke, the item overlays, the subtitles and the watermark,
// so its lines cut through the typography and the treatment reads as dirt. A
// grid appended before the source is hidden by the card and shows only on the
// margins (the R7 defect). The sane paint order is background, source, grid,
// card stroke, item overlays, subtitles, watermark: the grid belongs to the
// footage, not to the text.
func ensureClipGridAboveSource(plan *Plan) {
	gridIndex := -1
	for i, layer := range plan.Layers {
		if layer.ID == "clip__grid" {
			gridIndex = i
			break
		}
	}
	if gridIndex < 0 {
		return
	}
	grid := plan.Layers[gridIndex]
	rest := make([]Layer, 0, len(plan.Layers)-1)
	rest = append(rest, plan.Layers[:gridIndex]...)
	rest = append(rest, plan.Layers[gridIndex+1:]...)
	anchor := -1
	for i, layer := range rest {
		if layer.ID == "source" {
			anchor = i
		}
	}
	if anchor < 0 {
		// No source clip: the blurred background is the footage plate, so the
		// grid still belongs above it and below the items.
		for i, layer := range rest {
			if layer.ID == "background" {
				anchor = i
			}
		}
	}
	if anchor < 0 {
		// Nothing to sit on: keep the grid where it is instead of losing it.
		plan.Layers = append(rest, grid)
		return
	}
	out := make([]Layer, 0, len(rest)+1)
	out = append(out, rest[:anchor+1]...)
	out = append(out, grid)
	out = append(out, rest[anchor+1:]...)
	plan.Layers = out
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
