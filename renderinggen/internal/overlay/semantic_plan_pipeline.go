// semantic_plan_pipeline.go owns the typed stages of the semantic overlay
// compiler: decode → validate → normalize → resolve → lower → seal.
//
// The stages exist to make the authoritative value explicit. Validation never
// mutates the decoded document; compatibility normalization is the one stage
// that mutates, and it mutates a deep copy so replay/snapshot callers keep the
// document they decoded.
//
// map[string]any boundary: open maps are wire/preset data only. After
// resolvePlan, compilers read itemPresentation.Params through the existing
// typed accessors; a new key must be normalized by resolveItemPresentation (or
// its accessor) and covered by a default/type-error test, never read ad hoc by
// a lowering file.
package overlay

import "fmt"

type canvasSpec struct {
	Width  int
	Height int
}

type frameRate struct {
	Num int
	Den int
}

// validatedPlan is the decoded document plus the identity facts validation
// proved. Source is read-only.
type validatedPlan struct {
	PlanID string
	Canvas canvasSpec
	FPS    frameRate
	Source *semanticPlan
}

// normalizedPlan is the only mutable generation. Notes records the legacy
// compatibility stages that actually ran.
type normalizedPlan struct {
	Plan  semanticPlan
	Notes []string
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

// normalizeCompatibility applies the legacy policy/group/map transformations
// to a deep copy. The validated input remains byte-identical.
func normalizeCompatibility(validated validatedPlan) (normalizedPlan, error) {
	if validated.Source == nil {
		return normalizedPlan{}, fmt.Errorf("overlay: validated plan lost its source document")
	}
	plan := *validated.Source
	// Only copy when a compatibility stage can write. The notes are also the
	// copy trigger, so the common phrase-only plan keeps the historical
	// allocation profile while every legacy shape gets an owned value tree.
	notes := semanticPlanCompatibilityNotes(&plan)
	if len(notes) > 0 {
		plan = cloneSemanticPlan(plan)
	}
	// Plan-level animation_policies are retired at decode (the decoder rejects
	// the key). The live compatibility floor is the composite group pin: it
	// validates group_motion_id and makes it the item's motion before groups or
	// per-kind lowering run.
	if err := pinGroupMotionIDs(&plan); err != nil {
		return normalizedPlan{}, err
	}
	if len(plan.AnimationGroups) > 0 {
		// Groups resolve after the legacy policy selector so the two
		// generations cannot silently stack.
		if err := resolveAnimationGroups(&plan); err != nil {
			return normalizedPlan{}, err
		}
	}
	// Retirement is checked after policies expand, so a motion arriving
	// through a plan-level default is rejected exactly like an explicit one.
	if err := validateDeprecatedMotions(&plan); err != nil {
		return normalizedPlan{}, err
	}
	if err := resolveMapFeatureMoves(&plan); err != nil {
		return normalizedPlan{}, err
	}
	return normalizedPlan{Plan: plan, Notes: notes}, nil
}

// resolvePlan builds the concrete plan's non-item layers and resolves every
// item once. Asset registration order is the historical order.
func resolvePlan(normalized normalizedPlan) (resolvedPlan, error) {
	src := &normalized.Plan
	plan := newPlan(src.PlanID, src.Width, src.Height, src.FPSNum, src.FPSDen, 0)
	plan.Output.ProfileID = src.OutputProfileID
	plan.Output.ColorSpace = canonicalProfileColorSpace(src.OutputProfileID)
	if src.DurationMS > 0 {
		_, endFrame := msFrames(0, src.DurationMS, int64(src.FPSNum), int64(src.FPSDen))
		plan.Canvas.DurationFrames = endFrame
	}

	registry := newAssetRegistry()
	if src.Audio != nil {
		plan.Output.Audio = &Audio{
			Mode: src.Audio.Mode, Codec: src.Audio.Codec,
			SampleRate: src.Audio.SampleRate, Channels: src.Audio.Channels,
		}
	}
	bgLayer, bgPresent, err := compileBackgroundLayer(src, registry)
	if err != nil {
		return resolvedPlan{}, err
	}
	if bgPresent {
		plan.Layers = append(plan.Layers, bgLayer)
	}

	resolved, err := resolveSemanticItems(src)
	if err != nil {
		return resolvedPlan{}, err
	}
	resolvedByID := make(map[string]resolvedItem, len(resolved))
	for _, item := range resolved {
		resolvedByID[item.Item.ID] = item
	}
	var pendingSFX []pendingSFXClip
	// Pre-register every item asset so collisions fail before any layer is
	// emitted (the registry is the single owner of the id → path mapping).
	for _, item := range src.Items {
		for _, ref := range item.Assets {
			if _, err := registry.Register(ref); err != nil {
				return resolvedPlan{}, fmt.Errorf("overlay: item %q asset: %w", item.ID, err)
			}
		}
		if sfx := item.SoundEffect; sfx != nil {
			resolvedItem, ok := resolvedByID[item.ID]
			if !ok || !isImageKind(resolvedItem.Kind) {
				return resolvedPlan{}, fmt.Errorf("overlay: item %q sound_effect is only valid on an image overlay", item.ID)
			}
			clip, visibleEndFrame, err := resolveSoundEffectCue(item, resolvedItem, src, registry)
			if err != nil {
				return resolvedPlan{}, err
			}
			pendingSFX = append(pendingSFX, pendingSFXClip{clip: clip, visibleEndFrame: visibleEndFrame})
		}
	}
	unknown := unknownTemplates(resolved)

	var sourceLayerIndex = -1
	if src.Source != nil && src.Source.AssetID != "" {
		path := src.Source.Path
		ref := SemanticAssetRef{ID: src.Source.AssetID, SHA256: src.Source.SHA256}
		if path == "" {
			registered, err := registry.Register(ref)
			if err != nil {
				return resolvedPlan{}, fmt.Errorf("overlay: source asset: %w", err)
			}
			path = registered
		} else {
			registered, err := registry.RegisterAtPath(ref, path)
			if err != nil {
				return resolvedPlan{}, fmt.Errorf("overlay: source asset: %w", err)
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
			return resolvedPlan{}, err
		}
		sourceLayerIndex = len(plan.Layers)
		plan.Layers = append(plan.Layers, srcLayer)
		if srcLayer.FrameStroke != nil {
			strokeLayer, err := compileMediaStrokeLayer(srcLayer, src.Width, src.Height)
			if err != nil {
				return resolvedPlan{}, fmt.Errorf("overlay: source_frame: %w", err)
			}
			plan.Layers = append(plan.Layers, strokeLayer)
		}
	} else if src.SourceFrame != nil {
		return resolvedPlan{}, fmt.Errorf("overlay: source_frame requires a source clip")
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
		Plan: plan, Document: normalized.Plan, Items: resolved, Registry: registry,
		Cameras: cameras, PendingSFX: pendingSFX, Duration: plan.Canvas.DurationFrames,
		SourceLayerIndex: sourceLayerIndex, Unknown: unknown,
	}, nil
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
	ensureClipGridOnTop(plan)
	return plan, stats, nil
}

// sealPlan performs the final duration/SFX/camera/schema seal and returns the
// public compile tuple.
func sealPlan(plan *Plan, stats Stats, resolved resolvedPlan) (*Plan, []Asset, Stats, []string, error) {
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
	if err := bakeResolvedCameraSamples(plan); err != nil {
		return nil, nil, zeroStats(), nil, err
	}
	plan.Schema, plan.Version = RenderPlanWireVersion(plan)
	return plan, resolved.Registry.Assets(), stats, resolved.Unknown, nil
}

func semanticPlanHasGroupMotionPins(src *semanticPlan) bool {
	for i := range src.Items {
		if src.Items[i].GroupMotionID != "" {
			return true
		}
	}
	return false
}

func semanticPlanHasMapFeatureMoves(src *semanticPlan) bool {
	for i := range src.Items {
		if src.Items[i].Map != nil && src.Items[i].Map.FlyToFeature != nil {
			return true
		}
	}
	return false
}

// semanticPlanCompatibilityNotes lists the live compatibility stages that can
// write to the document. An empty list means normalization is validation-only
// and the decoded value tree can be reused without a copy.
func semanticPlanCompatibilityNotes(src *semanticPlan) []string {
	if src == nil {
		return nil
	}
	var notes []string
	if semanticPlanHasGroupMotionPins(src) {
		notes = append(notes, "group_motion_pins")
	}
	if len(src.AnimationGroups) > 0 {
		notes = append(notes, "animation_groups")
	}
	if semanticPlanHasMapFeatureMoves(src) {
		notes = append(notes, "map_feature_moves")
	}
	return notes
}

// cloneSemanticPlan deep-copies the fields normalization may touch. The
// decoded document is a value tree; maps are copied one level because every
// mutation replaces the map value rather than editing it in place.
func cloneSemanticPlan(src semanticPlan) semanticPlan {
	out := src
	out.Items = make([]semanticItem, len(src.Items))
	for i := range src.Items {
		out.Items[i] = cloneSemanticItem(src.Items[i])
	}
	if src.AnimationGroups != nil {
		out.AnimationGroups = make([]semanticAnimationGroup, len(src.AnimationGroups))
		for i, group := range src.AnimationGroups {
			copy := group
			copy.Params = cloneAnyMap(group.Params)
			copy.Members = append([]semanticAnimationGroupMember(nil), group.Members...)
			for j := range copy.Members {
				copy.Members[j].MotionParams = cloneAnyMap(group.Members[j].MotionParams)
			}
			out.AnimationGroups[i] = copy
		}
	}
	return out
}

func cloneSemanticItem(item semanticItem) semanticItem {
	out := item
	out.TemplateSlots = cloneAnyMap(item.TemplateSlots)
	out.MotionParams = cloneAnyMap(item.MotionParams)
	out.CaptionMotionParams = cloneAnyMap(item.CaptionMotionParams)
	out.Params = cloneAnyMap(item.Params)
	out.Style = cloneAnyMap(item.Style)
	out.Assets = append([]SemanticAssetRef(nil), item.Assets...)
	out.Frame = cloneSourceFrame(item.Frame)
	out.Map = cloneSemanticMap(item.Map)
	out.Metric = cloneMetric(item.Metric)
	out.Date = cloneDate(item.Date)
	if item.SoundEffect != nil {
		sfx := *item.SoundEffect
		out.SoundEffect = &sfx
	}
	if item.ImageLayers != nil {
		out.ImageLayers = make([]SemanticImageLayer, len(item.ImageLayers))
		for i, child := range item.ImageLayers {
			copy := child
			copy.MotionParams = cloneAnyMap(child.MotionParams)
			copy.CaptionMotionParams = cloneAnyMap(child.CaptionMotionParams)
			copy.Params = cloneAnyMap(child.Params)
			copy.Frame = cloneSourceFrame(child.Frame)
			out.ImageLayers[i] = copy
		}
	}
	return out
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func cloneSourceFrame(src *semanticSourceFrame) *semanticSourceFrame {
	if src == nil {
		return nil
	}
	out := *src
	if src.Border != nil {
		b := *src.Border
		out.Border = &b
	}
	if src.Shadow != nil {
		s := *src.Shadow
		out.Shadow = &s
	}
	if src.Shadows != nil {
		out.Shadows = append([]semanticFrameShadow(nil), src.Shadows...)
	}
	if src.Stroke != nil {
		s := *src.Stroke
		out.Stroke = &s
	}
	return &out
}

func cloneSemanticMap(src *SemanticMap) *SemanticMap {
	if src == nil {
		return nil
	}
	out := *src
	if src.Pins != nil {
		out.Pins = make([]SemanticMapPin, len(src.Pins))
		for i, pin := range src.Pins {
			out.Pins[i] = pin
			if pin.LabelStyle != nil {
				style := *pin.LabelStyle
				out.Pins[i].LabelStyle = &style
			}
			out.Pins[i].LabelOffsetPX = append([]float64(nil), pin.LabelOffsetPX...)
		}
	}
	out.LODs = append([]SemanticMapLOD(nil), src.LODs...)
	if src.CameraMove != nil {
		move := *src.CameraMove
		out.CameraMove = &move
	}
	if src.FlyToFeature != nil {
		f := *src.FlyToFeature
		out.FlyToFeature = &f
	}
	if src.Routes != nil {
		out.Routes = make([]SemanticMapRoute, len(src.Routes))
		for i, route := range src.Routes {
			out.Routes[i] = route
			out.Routes[i].Stops = append([]SemanticMapPoint(nil), route.Stops...)
		}
	}
	return &out
}

func cloneMetric(src *SemanticMetricData) *SemanticMetricData {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}

func cloneDate(src *SemanticDateData) *SemanticDateData {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}
