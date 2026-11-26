// semantic_plan_pipeline.go owns the typed stages of the semantic overlay
// compiler: decode → validate → resolve → lower → seal.
//
// The stages exist to make the authoritative value explicit. Validation never
// mutates the decoded document. resolvePlan applies the live document
// contracts (group motion pins, animation groups, retired motions, map feature
// moves) directly to the owned document before resolving its items: there is
// no compatibility copy stage on the hot path (DEMOL-11). Replay decodes the
// raw bytes again, so determinism does not depend on keeping the decoded tree
// pristine.
//
// map[string]any boundary: open maps are wire/preset data only. After
// resolvePlan, compilers read itemPresentation.Params through the existing
// typed accessors; a new key must be normalized by resolveItemPresentation (or
// its accessor) and covered by a default/type-error test, never read ad hoc by
// a lowering file.

package overlay

import (
	"fmt"
)

type canvasSpec struct {
	Width  int
	Height int
}

type frameRate struct {
	Num int
	Den int
}

// validatedPlan is the decoded document plus the identity facts validation
// proved. Source is owned by the resolve stage: validation itself never writes.
type validatedPlan struct {
	PlanID string
	Canvas canvasSpec
	FPS    frameRate
	Source *semanticPlan
}

// resolvedPlan is everything lowering needs: the constructed concrete plan,
// the resolved items, the asset registry and the camera authority.
type resolvedPlan struct {
	Plan             *Plan
	Document         semanticPlan
	Items            []resolvedItem
	Registry         *assetRegistry
	Cameras          cameraControllerSet
	PendingSFX       []pendingSFXClip
	Duration         int64
	SourceLayerIndex int
	Unknown          []string
}

type pendingSFXClip struct {
	clip            AudioClipPlan
	visibleEndFrame int64
}

// validateSemanticPlan performs only checks that need no catalogs or assets.
// It never mutates src.
func validateSemanticPlan(src *semanticPlan) (validatedPlan, error) {
	if src == nil {
		return validatedPlan{}, fmt.Errorf("overlay: semantic plan is nil")
	}
	if src.PlanID == "" || src.VideoID == "" || src.Width <= 0 || src.Height <= 0 || src.FPSNum <= 0 || src.FPSDen <= 0 {
		return validatedPlan{}, fmt.Errorf("overlay: semantic plan requires plan_id, video_id and positive canvas/fps")
	}
	if src.DurationMS < 0 {
		return validatedPlan{}, fmt.Errorf("overlay: semantic plan duration_ms must not be negative")
	}
	// A plan must have at least one renderable primitive: a source clip, a
	// background, or an overlay item.
	if src.Source == nil && src.Background == nil && len(src.Items) == 0 {
		return validatedPlan{}, fmt.Errorf("overlay: semantic plan has no renderable primitives (source, background or items required)")
	}
	return validatedPlan{
		PlanID: src.PlanID,
		Canvas: canvasSpec{Width: src.Width, Height: src.Height},
		FPS:    frameRate{Num: src.FPSNum, Den: src.FPSDen},
		Source: src,
	}, nil
}

// applyDocumentContracts runs the live pre-resolve contract stages ONCE, in
// place, on the document the resolve stage owns. None of them is a pre-v2
// compatibility shim: each enforces a published v2 rule that lowering depends
// on. Plan-level animation_policies is retired at decode, so the composite
// group pin is the only motion-selector floor left.
func applyDocumentContracts(src *semanticPlan) error {
	if src == nil {
		return fmt.Errorf("overlay: validated plan lost its source document")
	}
	// The composite group pin validates group_motion_id and makes it the item's
	// motion before groups or per-kind lowering run.
	if err := pinGroupMotionIDs(src); err != nil {
		return err
	}
	if len(src.AnimationGroups) > 0 {
		// Groups resolve after the group pin so the two selector generations
		// cannot silently stack.
		if err := resolveAnimationGroups(src); err != nil {
			return err
		}
	}
	// Retirement is checked after the selectors expand, so a motion arriving
	// through a group is rejected exactly like an explicit one.
	if err := validateDeprecatedMotions(src); err != nil {
		return err
	}
	return resolveMapFeatureMoves(src)
}

// resolvePlan applies the document contracts and builds the concrete plan's
// non-item layers, then resolves every item once. Asset registration order is
// the historical order.
func resolvePlan(validated validatedPlan) (resolvedPlan, error) {
	src := validated.Source
	if err := applyDocumentContracts(src); err != nil {
		return resolvedPlan{}, err
	}
	plan := newResolvedPlanShell(src)
	registry := newAssetRegistry()
	if err := appendPlanBackgroundLayer(plan, src, registry); err != nil {
		return resolvedPlan{}, err
	}

	resolved, err := resolveSemanticItems(src)
	if err != nil {
		return resolvedPlan{}, err
	}
	pendingSFX, err := registerResolvedItemAssets(src, registry, resolved)
	if err != nil {
		return resolvedPlan{}, err
	}
	unknown := unknownTemplates(resolved)

	sourceLayerIndex, err := appendSourceClipLayer(plan, src, registry)
	if err != nil {
		return resolvedPlan{}, err
	}

	if sub := src.Subtitles; sub != nil {
		if len(sub.AssetRefs) == 0 {
			return resolvedPlan{}, fmt.Errorf("overlay: subtitles require at least one asset_ref")
		}
		if _, err := registry.Register(sub.AssetRefs[0]); err != nil {
			return resolvedPlan{}, fmt.Errorf("overlay: subtitle asset: %w", err)
		}
		if sub.FontRef != nil {
			if _, err := registry.Register(*sub.FontRef); err != nil {
				return resolvedPlan{}, fmt.Errorf("overlay: subtitle font asset: %w", err)
			}
		}
	}

	wmLayer, wmPresent, err := compileWatermarkLayer(src, registry, plan.Canvas.DurationFrames)
	if err != nil {
		return resolvedPlan{}, err
	}
	if wmPresent {
		plan.Layers = append(plan.Layers, wmLayer)
	}
	cameras := cameraControllerSet{scene: src.SceneCamera}
	if err := validateSceneCamera(src.SceneCamera); err != nil {
		return resolvedPlan{}, err
	}
	return resolvedPlan{
		Plan: plan, Document: *src, Items: resolved, Registry: registry,
		Cameras: cameras, PendingSFX: pendingSFX, Duration: plan.Canvas.DurationFrames,
		SourceLayerIndex: sourceLayerIndex, Unknown: unknown,
	}, nil
}

// newResolvedPlanShell creates the concrete plan with its canvas, output
// profile, audio policy and duration already resolved.
func newResolvedPlanShell(src *semanticPlan) *Plan {
	plan := newPlan(src.PlanID, src.Width, src.Height, src.FPSNum, src.FPSDen, 0)
	plan.Output.ProfileID = src.OutputProfileID
	plan.Output.ColorSpace = canonicalProfileColorSpace(src.OutputProfileID)
	if src.DurationMS > 0 {
		_, endFrame := msFrames(0, src.DurationMS, int64(src.FPSNum), int64(src.FPSDen))
		plan.Canvas.DurationFrames = endFrame
	}
	if src.Audio != nil {
		plan.Output.Audio = &Audio{
			Mode: src.Audio.Mode, Codec: src.Audio.Codec,
			SampleRate: src.Audio.SampleRate, Channels: src.Audio.Channels,
		}
	}
	return plan
}

// appendPlanBackgroundLayer compiles and appends the optional background plate.
func appendPlanBackgroundLayer(plan *Plan, src *semanticPlan, registry *assetRegistry) error {
	bgLayer, bgPresent, err := compileBackgroundLayer(src, registry)
	if err != nil {
		return err
	}
	if bgPresent {
		plan.Layers = append(plan.Layers, bgLayer)
	}
	return nil
}

// registerResolvedItemAssets pre-registers item assets and resolves sound
// effects before any layer is emitted, so collisions fail early.
func registerResolvedItemAssets(src *semanticPlan, registry *assetRegistry, resolved []resolvedItem) ([]pendingSFXClip, error) {
	resolvedByID := make(map[string]resolvedItem, len(resolved))
	for _, item := range resolved {
		resolvedByID[item.Item.ID] = item
	}
	var pendingSFX []pendingSFXClip
	for _, item := range src.Items {
		for _, ref := range item.Assets {
			if _, err := registry.Register(ref); err != nil {
				return nil, fmt.Errorf("overlay: item %q asset: %w", item.ID, err)
			}
		}
		if sfx := item.SoundEffect; sfx != nil {
			resolvedItem, ok := resolvedByID[item.ID]
			if !ok || !isImageKind(resolvedItem.Kind) {
				return nil, fmt.Errorf("overlay: item %q sound_effect is only valid on an image overlay", item.ID)
			}
			clip, visibleEndFrame, err := resolveSoundEffectCue(item, resolvedItem, src, registry)
			if err != nil {
				return nil, err
			}
			pendingSFX = append(pendingSFX, pendingSFXClip{clip: clip, visibleEndFrame: visibleEndFrame})
		}
	}
	return pendingSFX, nil
}

// appendSourceClipLayer registers the source asset, appends the video layer and
// its optional frame stroke, and returns the source layer index for duration
// finalization.
func appendSourceClipLayer(plan *Plan, src *semanticPlan, registry *assetRegistry) (int, error) {
	sourceLayerIndex := -1
	if src.Source != nil && src.Source.AssetID != "" {
		path := src.Source.Path
		ref := SemanticAssetRef{ID: src.Source.AssetID, SHA256: src.Source.SHA256}
		if path == "" {
			registered, err := registry.Register(ref)
			if err != nil {
				return -1, fmt.Errorf("overlay: source asset: %w", err)
			}
			path = registered
		} else {
			registered, err := registry.RegisterAtPath(ref, path)
			if err != nil {
				return -1, fmt.Errorf("overlay: source asset: %w", err)
			}
			path = registered
		}
		srcLayer := Layer{ID: "source", Type: "video", Source: path,
			Size: []float64{float64(src.Width), float64(src.Height)}, Fit: FitCover, StartFrame: 0}
		if src.ForegroundScale > 0 && src.ForegroundScale < 100 {
			srcLayer.Position = []float64{-float64(src.Width) * 0.5, -float64(src.Height) * 0.5}
			srcLayer.Scale = []float64{float64(src.ForegroundScale) / 100, float64(src.ForegroundScale) / 100}
		}
		if err := applyFrameTreatment(&srcLayer, src.SourceFrame); err != nil {
			return -1, err
		}
		sourceLayerIndex = len(plan.Layers)
		plan.Layers = append(plan.Layers, srcLayer)
		if srcLayer.FrameStroke != nil {
			strokeLayer, err := compileMediaStrokeLayer(srcLayer, src.Width, src.Height)
			if err != nil {
				return -1, fmt.Errorf("overlay: source_frame: %w", err)
			}
			plan.Layers = append(plan.Layers, strokeLayer)
		}
	} else if src.SourceFrame != nil {
		return -1, fmt.Errorf("overlay: source_frame requires a source clip")
	}
	return sourceLayerIndex, nil
}

// lowerPlan emits item layers and the plan-wide stages. It reads only the
// resolved generation; it never re-reads the decoded document's Items.
func lowerPlan(resolved resolvedPlan) (*Plan, Stats, error) {
	if resolved.Plan == nil || resolved.Registry == nil {
		return nil, zeroStats(), fmt.Errorf("overlay: lowerPlan requires a resolved plan")
	}
	plan := resolved.Plan
	src := &resolved.Document
	var stats Stats
	if err := appendResolvedItemLayers(plan, resolved.Items, src, resolved.Registry, &resolved.Cameras, &stats); err != nil {
		return nil, zeroStats(), err
	}
	if err := appendFullFrameStages(plan, src); err != nil {
		return nil, zeroStats(), err
	}
	if err := resolved.Cameras.apply(plan, src); err != nil {
		return nil, zeroStats(), err
	}
	finalizeLayerDurations(plan, resolved.SourceLayerIndex)
	ensureClipGridAboveSource(plan)
	// Seal the capabilities the lowering recorded. Every item has compiled by
	// now, so this is the complete set for the plan.
	plan.Features = src.features.export()
	return plan, stats, nil
}

// sealPlan performs the final duration/SFX/camera/schema seal and returns the
// public compile tuple.
func sealPlan(plan *Plan, stats Stats, resolved resolvedPlan, opts CompileOptions) (*Plan, []Asset, Stats, []string, error) {
	if plan == nil || resolved.Registry == nil {
		return nil, nil, zeroStats(), nil, fmt.Errorf("overlay: sealPlan requires a lowered plan")
	}
	if plan.Canvas.DurationFrames <= 0 {
		return nil, nil, zeroStats(), nil, fmt.Errorf("overlay: semantic plan duration is zero — provide duration_ms or at least one item with end_ms > 0")
	}
	for _, pending := range resolved.PendingSFX {
		clip := pending.clip
		availableFrames := plan.Canvas.DurationFrames - clip.Start
		visibleFrames := pending.visibleEndFrame - clip.Start
		if availableFrames < visibleFrames {
			visibleFrames = availableFrames
		}
		if visibleFrames < clip.ClipOut {
			clip.ClipOut = visibleFrames
		}
		if clip.ClipOut <= 0 || clip.Start >= plan.Canvas.DurationFrames {
			continue
		}
		if plan.Audio == nil {
			plan.Audio = &AudioMixPlan{}
		}
		plan.Audio.Clips = append(plan.Audio.Clips, clip)
	}
	if !opts.SkipCameraBake {
		if err := bakeResolvedCameraSamples(plan, opts.CameraBaker); err != nil {
			return nil, nil, zeroStats(), nil, err
		}
	}
	plan.Schema, plan.Version = RenderPlanWireVersion(plan)
	return plan, resolved.Registry.Assets(), stats, resolved.Unknown, nil
}

// The two plan-wide stages that run after item
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
