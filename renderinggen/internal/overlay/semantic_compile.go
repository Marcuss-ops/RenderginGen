// semantic_compile.go owns the single semantic overlay-plan.v1 → concrete
// render-plan.v2 lowering. It is a mechanical compiler: every style,
// geometry and motion decision comes from the plan or from RenderingGen's
// official preset catalog, and anything untyped or unresolved is rejected
// fail-closed instead of silently defaulted.
//
// The item pipeline is a single resolve → compile pass:
//
//	semanticItem ──resolve (registry.go)──▶ resolvedItem ──compile per kind─▶ []Layer
//	                                                └──▶ Stats
//
// The kind is the semantic SSOT: PipelineGen decides WHAT the item is, the
// template registry validates it, and each kind has exactly one compiler —
// dispatched by compileItem below and owned by the sibling
// semantic_*_compile.go / semantic_text_accents.go files.
package overlay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
)

// resolvedItem is RenderingGen's canonical internal representation of one
// overlay item. It is produced once by resolveSemanticItems and consumed by
// the per-kind compilers and the ledger — the raw JSON is never re-read.
type resolvedItem struct {
	Item   semanticItem
	Spec   TemplateSpec
	Kind   ItemKind
	Params map[string]any
	Start  int64
	End    int64
	// PresetID is the validated official preset for the (text/image) layer.
	PresetID string
	// Preset and ImagePreset are the resolved official definitions.
	Preset      PresetDefinition
	ImagePreset PresetDefinition
	Map         *resolvedMap
}

// decodeSemanticPlan performs the ONE strict decode of a semantic overlay
// plan. The published contract declares additionalProperties:false at every
// level, and the field set is pinned to the schema by
// contract_schema_parity_test.go. A key outside that set is a producer bug
// (usually a rename) that must fail loudly here instead of being dropped —
// the historical permissive decode silently discarded project_id,
// renderer_version and fingerprint on every job.
//
// The error is returned UNWRAPPED so each entry point can apply its own
// historical error taxonomy on top (see CompileSemantic's error path).
func decodeSemanticPlan(raw []byte) (semanticPlan, error) {
	var src semanticPlan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&src); err != nil {
		return semanticPlan{}, err
	}
	return src, nil
}

// compileSemantic is the internal one-shot entry (decode + lower). The
// processor's hot path goes through CompileSemantic, which decodes the raw
// document exactly once and hands the decoded plan to compileSemanticDecoded.
func compileSemantic(raw []byte) (*Plan, []Asset, Stats, []string, error) {
	src, err := decodeSemanticPlan(raw)
	if err != nil {
		return nil, nil, Stats{}, nil, fmt.Errorf("overlay: decode semantic plan: %w", err)
	}
	return compileSemanticDecoded(&src)
}

// compileSemanticDecoded lowers an ALREADY-decoded semantic plan. It is the
// single semantic overlay-plan.v1 → concrete render-plan.v2 lowering; the
// decode happens once upstream so the prepare path never re-reads the raw
// document.
func compileSemanticDecoded(src *semanticPlan) (*Plan, []Asset, Stats, []string, error) {
	if src.PlanID == "" || src.VideoID == "" || src.Width <= 0 || src.Height <= 0 || src.FPSNum <= 0 || src.FPSDen <= 0 {
		return nil, nil, Stats{}, nil, fmt.Errorf("overlay: semantic plan requires plan_id, video_id and positive canvas/fps")
	}
	if err := resolveAnimationPolicies(src); err != nil {
		return nil, nil, Stats{}, nil, err
	}
	// Retirement is checked after the policies are expanded, so a motion that
	// arrives through a plan-level default is rejected exactly like an explicit
	// motion_id instead of only when the item names it directly.
	if err := validateDeprecatedMotions(src); err != nil {
		return nil, nil, Stats{}, nil, err
	}
	// A plan must have at least one renderable primitive: a source clip, a
	// background, or an overlay item. An empty plan with nothing to render is
	// always rejected fail-closed.
	if src.Source == nil && src.Background == nil && len(src.Items) == 0 {
		return nil, nil, Stats{}, nil, fmt.Errorf("overlay: semantic plan has no renderable primitives (source, background or items required)")
	}
	// The Plan constructor is the single owner of the schema/version and the
	// output defaults, so the compiler cannot drift from it.
	plan := *newPlan(src.PlanID, src.Width, src.Height, src.FPSNum, src.FPSDen, 0)
	plan.Output.ProfileID = src.OutputProfileID

	// Seed canvas duration from the explicit duration_ms when provided. Items
	// can extend it but cannot shrink it. For clip renders with items:[] this
	// is the only source of duration; the compiler rejects zero duration at the
	// end of the function.
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
		return nil, nil, Stats{}, nil, err
	}
	if bgPresent {
		plan.Layers = append(plan.Layers, bgLayer)
	}

	// Named map targets are producer-level inputs to item resolution, so resolve
	// them before the one authoritative semantic-item pass.
	if err := resolveMapFeatureMoves(src); err != nil {
		return nil, nil, Stats{}, nil, err
	}
	// Resolve every item once before lowering any item-specific assets. The
	// resolved kind and frame window are authoritative for SFX eligibility and
	// placement (template aliases and frame quantization included).
	resolved, err := resolveSemanticItems(src)
	if err != nil {
		return nil, nil, Stats{}, nil, err
	}
	resolvedByID := make(map[string]resolvedItem, len(resolved))
	for _, item := range resolved {
		resolvedByID[item.Item.ID] = item
	}
	type pendingSFX struct {
		clip            AudioClipPlan
		visibleEndFrame int64
	}
	var pendingSFXClips []pendingSFX
	// Pre-register every item asset so collisions fail before any layer is
	// emitted (the registry is the single owner of the id → path mapping).
	for _, item := range src.Items {
		for _, ref := range item.Assets {
			if _, err := registry.Register(ref); err != nil {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: item %q asset: %w", item.ID, err)
			}
		}
		if sfx := item.SoundEffect; sfx != nil {
			resolvedItem, ok := resolvedByID[item.ID]
			if !ok || !isImageKind(resolvedItem.Kind) {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: item %q sound_effect is only valid on an image overlay", item.ID)
			}
			clip, visibleEndFrame, err := resolveSoundEffectCue(item, resolvedItem, src, registry)
			if err != nil {
				return nil, nil, Stats{}, nil, err
			}
			pendingSFXClips = append(pendingSFXClips, pendingSFX{clip: clip, visibleEndFrame: visibleEndFrame})

		}
	}

	// Every resolved item passed the fail-closed template gate above, so this
	// list is empty for any plan that reaches here. It is COMPUTED rather than
	// assumed so CompileResult.UnknownTemplates keeps being a projection of what
	// was actually resolved — the day the gate is relaxed, the field says so.
	unknown := unknownTemplates(resolved)

	// Source clip — lowers to a full-canvas video layer. When foreground_scale
	// is set the source is scaled and centered on the canvas.
	var sourceLayerIndex = -1
	if src.Source != nil && src.Source.AssetID != "" {
		path := src.Source.Path
		ref := SemanticAssetRef{ID: src.Source.AssetID, SHA256: src.Source.SHA256}
		if path == "" {
			registered, err := registry.Register(ref)
			if err != nil {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: source asset: %w", err)
			}
			path = registered
		} else {
			registered, err := registry.RegisterAtPath(ref, path)
			if err != nil {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: source asset: %w", err)
			}
			path = registered
		}
		// FullGraph video layers must carry the same explicit fit contract as
		// background video layers. DirectYUV does not need it, which hid this
		// omission until an overlay/background selected the compositor: Chronon
		// then rendered the overlay over a black canvas instead of the source.
		srcLayer := Layer{ID: "source", Type: "video", Source: path,
			Size: []float64{float64(src.Width), float64(src.Height)}, Fit: FitCover, StartFrame: 0}
		// Foreground scale: keep the sampled video surface at canvas size and
		// express the centred transform in Chronon's modular coordinate space.
		// ForegroundScale == 0 or 100 means full-canvas (no scaling).
		if src.ForegroundScale > 0 && src.ForegroundScale < 100 {
			// The modular resolver adds the canvas half-size to unpinned 2D
			// layers. Cancelling that implicit shift here leaves the transform
			// centred in TransformNode's pixel-space contract. Position [0,0]
			// would apply the implicit centre a second time and place the video
			// in the lower-right quadrant.
			srcLayer.Position = []float64{-float64(src.Width) * 0.5, -float64(src.Height) * 0.5}
			srcLayer.Scale = []float64{float64(src.ForegroundScale) / 100, float64(src.ForegroundScale) / 100}
		}
		// Card treatment of the clip (border frame + drop shadow + perimeter
		// stroke). The border is lowered behind the source layer; the stroke gets
		// its own transparent shape above the media box. A declaration the worker
		// cannot honour is a compile error (see applyFrameTreatment), never dropped.
		if err := applyFrameTreatment(&srcLayer, src.SourceFrame); err != nil {
			return nil, nil, Stats{}, nil, err
		}
		sourceLayerIndex = len(plan.Layers)
		plan.Layers = append(plan.Layers, srcLayer)
		if srcLayer.FrameStroke != nil {
			strokeLayer, err := compileMediaStrokeLayer(srcLayer, src.Width, src.Height)
			if err != nil {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: source_frame: %w", err)
			}
			plan.Layers = append(plan.Layers, strokeLayer)
		}
	} else if src.SourceFrame != nil {
		// A frame with no source clip to attach to would be dropped silently.
		return nil, nil, Stats{}, nil, fmt.Errorf("overlay: source_frame requires a source clip")
	}

	// Sidecar subtitles remain a published companion asset. Chronon's current
	// render-plan schema has no `subtitle` layer type, so do not emit one.
	if sub := src.Subtitles; sub != nil {
		if len(sub.AssetRefs) == 0 {
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: subtitles require at least one asset_ref")
		}
		if _, err := registry.Register(sub.AssetRefs[0]); err != nil {
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: subtitle asset: %w", err)
		}
		// Sidecar subtitles remain a published companion asset: the manifest
		// entry (above) is what gets published; Chronon has no subtitle layer.
	}

	// Watermark — lowers to a text or image layer at the requested position.
	// Geometry and style come ONLY from the plan's typed blocks: font size,
	// color, shadow (style), position + margin_px (layout). Unknown or missing
	// values are compile errors, never silent fallbacks.
	wmLayer, wmPresent, err := compileWatermarkLayer(src, registry, plan.Canvas.DurationFrames)
	if err != nil {
		return nil, nil, Stats{}, nil, err
	}
	if wmPresent {
		plan.Layers = append(plan.Layers, wmLayer)
	}

	// Item overlay layers — one compile per resolved kind, counters from the
	// same pass.
	var stats Stats
	cameras := cameraControllerSet{scene: src.SceneCamera}
	// The plan-level whole-scene camera is validated up front so a malformed
	// rig fails before any layer work. Cross-controller conflicts are decided
	// after the loop, once every item has been observed.
	if err := validateSceneCamera(src.SceneCamera); err != nil {
		return nil, nil, Stats{}, nil, err
	}
	if err := appendResolvedItemLayers(&plan, resolved, src, registry, &cameras, &stats); err != nil {
		return nil, nil, Stats{}, nil, err
	}

	// The grid is an ordinary full-frame shape and therefore participates in
	// the composite before the optional final adjustment stack.
	if err := appendFullFrameStages(&plan, src); err != nil {
		return nil, nil, Stats{}, nil, err
	}

	// Resolve the single camera authority after the loop: every controller is
	// known, so a map camera or an entity camera on the final item cannot slip
	// through and the verdict does not depend on item order.
	if err := cameras.apply(&plan, src); err != nil {
		return nil, nil, Stats{}, nil, err
	}

	finalizeLayerDurations(&plan, sourceLayerIndex)
	if plan.Canvas.DurationFrames <= 0 {
		return nil, nil, Stats{}, nil, fmt.Errorf("overlay: semantic plan duration is zero — provide duration_ms or at least one item with end_ms > 0")
	}
	for _, pending := range pendingSFXClips {
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
			// The item's first image frame lies outside the finalized canvas;
			// there is no audible cue to emit for an invisible image.
			continue
		}
		if plan.Audio == nil {
			plan.Audio = &AudioMixPlan{}
		}
		plan.Audio.Clips = append(plan.Audio.Clips, clip)
	}

	if err := bakeResolvedCameraSamples(&plan); err != nil {
		return nil, nil, Stats{}, nil, err
	}
	plan.Schema, plan.Version = RenderPlanWireVersion(&plan)

	// Stable asset order (the registry sorts) keeps prepared-plan
	// fingerprints reproducible. The plan stays typed; the caller marshals it
	// exactly once at the Chronon boundary.
	return &plan, registry.Assets(), stats, unknown, nil
}

// resolveSemanticItems validates the plan's items once and lowers each to its
// canonical resolvedItem. This is the only place an item's kind, preset and
// timing are decided.
func resolveMapFeatureMoves(src *semanticPlan) error {
	registry, registryErr := geofeatures.DefaultRegistry()
	for index := range src.Items {
		item := &src.Items[index]
		if item.Map == nil || item.Map.FlyToFeature == nil {
			continue
		}
		if item.Map.CameraMove != nil {
			return fmt.Errorf("overlay: map item %q must not combine fly_to_feature and camera_move", item.ID)
		}
		spec := templateSpecFor(item.Template)
		kind, err := spec.resolveKind(item.Kind, item.ID)
		if err != nil {
			return err
		}
		if kind != KindMap {
			return fmt.Errorf("overlay: item %q fly_to_feature is only valid on a map", item.ID)
		}
		if registryErr != nil {
			return fmt.Errorf("overlay: map item %q cannot resolve fly_to_feature: %w", item.ID, registryErr)
		}
		if item.Map.MotionID != "" {
			return fmt.Errorf("overlay: map item %q fly_to_feature cannot be combined with motion_id", item.ID)
		}
		if len(item.Map.LODs) < 2 {
			return fmt.Errorf("overlay: map item %q fly_to_feature requires at least two offline LOD assets", item.ID)
		}
		start := SemanticMapPoint{Latitude: item.Map.Center.Latitude, Longitude: item.Map.Center.Longitude}
		move, feature, err := resolveFeatureCameraMove(registry, item.Map.FlyToFeature.Feature, item.Map.FlyToFeature.Padding, start, item.Map.Zoom, item.Map.LODs[len(item.Map.LODs)-1].Zoom, src.Width, src.Height)
		if err != nil {
			return fmt.Errorf("overlay: map item %q fly_to_feature: %w", item.ID, err)
		}
		if road := strings.TrimSpace(item.Map.FlyToFeature.Road); road != "" {
			if road != "world" {
				return fmt.Errorf("overlay: map item %q fly_to_feature road %q is unsupported (supported: world)", item.ID, road)
			}
			if feature.Type == "world" {
				return fmt.Errorf("overlay: map item %q fly_to_feature road world requires a non-world feature", item.ID)
			}
			move.From = SemanticMapPoint{Latitude: 0, Longitude: 0}
			move.StartZoom = float64(item.Map.LODs[0].Zoom)
		}
		item.Map.CameraMove = &move
	}
	return nil
}

// mergeSemanticParams resolves the plan's explicit style precedence once:
// Params is the runtime base; Style overrides matching keys. Chain segments
// use this same helper so their standalone and fused lowering is identical.
func mergeSemanticParams(params, style map[string]any) map[string]any {
	merged := make(map[string]any, len(params)+len(style))
	for key, value := range params {
		merged[key] = value
	}
	for key, value := range style {
		merged[key] = value
	}
	return merged
}

func resolveSemanticItems(src *semanticPlan) ([]resolvedItem, error) {
	out := make([]resolvedItem, 0, len(src.Items))
	for _, item := range src.Items {
		start, end := itemFrames(item, int64(src.FPSNum), int64(src.FPSDen))
		startUS, endUS := item.StartMS*1000, item.EndMS*1000
		if item.StartUS != nil && item.DurationUS != nil {
			startUS, endUS = *item.StartUS, *item.StartUS+*item.DurationUS
		}
		if item.ID == "" || item.StartMS < 0 || item.EndMS <= item.StartMS {
			return nil, fmt.Errorf("overlay: invalid semantic item %q", item.ID)
		}
		if end <= start {
			return nil, fmt.Errorf("overlay: item %q window [%d,%d) us collapses to no frames at %d/%d fps",
				item.ID, startUS, endUS, src.FPSNum, src.FPSDen)
		}
		if err := validateTextElementContract(item); err != nil {
			return nil, err
		}
		spec := templateSpecFor(item.Template)
		kind, err := spec.resolveKind(item.Kind, item.ID)
		if err != nil {
			return nil, err
		}
		// Unknown templates fail closed: a non-empty template_id that
		// resolves to no registry row and no legacy alias is a producer
		// typo or an unregistered addition, and silently lowering it as a
		// bare primitive once produced visibly wrong videos with pixels as
		// the only evidence. An empty template_id stays valid (simple text
		// primitive); compatibility travels only through the explicit
		// legacyTemplateAliases table.
		if strings.TrimSpace(item.Template) != "" && !spec.Registered {
			return nil, fmt.Errorf("overlay: item %q names unknown template %q: register it in the template registry or add an explicit legacy alias", item.ID, item.Template)
		}
		if err := validateSemanticImageLayers(item, kind); err != nil {
			return nil, err
		}
		if item.EntityStyleID != "" {
			if _, ok := ResolveEntityStyle(item.EntityStyleID, src.entityStyleSampleKey(), src.VideoID, item.ID); !ok {
				return nil, fmt.Errorf("overlay: item %q has unsupported entity_style_id %q", item.ID, item.EntityStyleID)
			}
			behavior := behaviorOf(kind)
			if behavior != behaviorEntity && behavior != behaviorImage {
				return nil, fmt.Errorf("overlay: item %q entity_style_id is only valid for entity or image items", item.ID)
			}
			if len(item.ImageLayers) > 0 {
				return nil, fmt.Errorf("overlay: item %q entity_style_id does not support composite image_layers", item.ID)
			}
			if len(item.Assets) == 0 || strings.TrimSpace(item.EntityCaption) == "" {
				return nil, fmt.Errorf("overlay: item %q entity_style_id %q requires an image and entity_caption", item.ID, item.EntityStyleID)
			}
		}
		// The map declaration is validated for EVERY item, not only map ones: a
		// map block on a phrase item would otherwise be carried through the plan
		// and silently ignored, which is exactly the kind of contract drift the
		// worker exists to stop.
		if err := validateMapContract(item, kind, src.Width, src.Height); err != nil {
			return nil, err
		}
		// The resolved kind is authoritative for the preset family too, so an
		// unknown template paired with an explicit image kind still validates
		// against the image catalog. Registered DATE/METRIC presentation kinds
		// also stay on the text preset family while their dedicated catalog
		// motion_id controls the presentation animation.
		if isImageKind(kind) {
			spec.Family = PresetImage
		} else if spec.Kind == KindPrimitive || kind == KindNumber || kind == KindMetricStat || kind == KindTimelineDate {
			spec.Family = PresetText
		}
		params := mergeSemanticParams(item.Params, item.Style)
		ri := resolvedItem{Item: item, Spec: spec, Kind: kind, Params: params, Start: start, End: end}

		if isImageKind(kind) && kind != KindLightLeak && len(item.Assets) == 0 && len(item.ImageLayers) == 0 {
			return nil, fmt.Errorf("overlay: image template %q item %q requires asset_refs", item.Template, item.ID)
		}
		if kind == KindLightLeak && strings.TrimSpace(item.MotionID) == "" {
			return nil, fmt.Errorf("overlay: light leak item %q requires a certified lightleak motion_id", item.ID)
		}
		if isVideoKind(kind) && len(item.Assets) == 0 {
			return nil, fmt.Errorf("overlay: video overlay template %q item %q requires asset_refs", item.Template, item.ID)
		}
		preset, err := presetFor(item, spec)
		if err != nil {
			return nil, err
		}
		ri.PresetID = preset
		if preset != "" {
			def, err := resolveOfficialPreset(preset, string(spec.Family))
			if err != nil {
				return nil, err
			}
			ri.Preset = def
		}
		if preset == ClassicPhrasePresetID && !isOpaqueWhiteCanvas(src.Background) {
			return nil, fmt.Errorf("overlay: preset %q requires an opaque white canvas background", preset)
		}
		if err := validateChildCaptionStyles(item); err != nil {
			return nil, err
		}
		hasCaption := itemHasCaption(item)
		if item.CameraMotionID != "" && len(item.ImageLayers) > 0 {
			// Camera sequences render each child's caption beside its portrait;
			// the group itself intentionally has no aggregate EntityCaption.
			hasCaption = true
		}
		if err := validateTextRuntimeOverrides(params, item.ID, kind, preset, len(item.Assets) > 0, hasCaption); err != nil {
			return nil, err
		}
		if isMapKind(kind) {
			mapSpec, err := resolveMap(item.Map, item.Assets, src.Width, src.Height, end-start)
			if err != nil {
				return nil, fmt.Errorf("overlay: map item %q resolve: %w", item.ID, err)
			}
			ri.Map = mapSpec
		}
		if (isEntityKind(kind) || (isImageKind(kind) && item.EntityStyleID != "")) && len(item.Assets) > 0 {
			if imagePreset := strings.TrimSpace(item.ImagePresetID); imagePreset != "" {
				def, err := resolveOfficialPreset(imagePreset, string(PresetImage))
				if err != nil {
					return nil, err
				}
				ri.ImagePreset = def
			}
		}
		out = append(out, ri)
	}
	if err := validateMapComposition(src.MapCompositionID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// compileItem dispatches one resolved item to its per-kind compiler. The kind
// is the only discriminator; there is exactly one compiler per kind class and
// one compatibility path for legacy entity_card items that still carry an
// image asset.
func compileItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	switch {
	case isChainKind(ri.Kind):
		return compileChainItem(ri, src, registry)
	case isGroupKind(ri.Kind):
		layers, err := compileEntityGroup(ri, src, registry)
		if err != nil {
			return nil, err
		}
		return applyImageItemEffects(ri, layers)
	case isEntityKind(ri.Kind) && len(ri.Item.ImageLayers) > 0:
		return compileImageItem(ri, src, registry)
	case isEntityKind(ri.Kind) && len(ri.Item.Assets) > 0:
		layers, err := compileEntityCard(ri, src, registry)
		if err != nil {
			return nil, err
		}
		return applyImageItemEffects(ri, layers)
	case isImageKind(ri.Kind) && ri.Item.EntityStyleID != "" && len(ri.Item.Assets) > 0:
		if ri.Item.ImagePresetID == "" {
			return nil, fmt.Errorf("overlay: entity image item %q with entity_style_id requires image_preset_id", ri.Item.ID)
		}
		layers, err := compileEntityCard(ri, src, registry)
		if err != nil {
			return nil, err
		}
		return applyImageItemEffects(ri, layers)
	case isVideoKind(ri.Kind):
		layer, err := compileVideoOverlayLayer(ri, src, registry)
		if err != nil {
			return nil, err
		}
		return []Layer{layer}, nil
	case isImageKind(ri.Kind):
		return compileImageItem(ri, src, registry)
	case isShapeKind(ri.Kind):
		layer, err := resolveShape(ri, src)
		if err != nil {
			return nil, err
		}
		if ri.Item.MotionID != "" {
			if !isChartBarItem(ri) {
				return nil, fmt.Errorf("overlay: data visualization motion %q is only supported on chart_bar shapes", ri.Item.MotionID)
			}
			animation, err := animationForMotionTarget(ri.Item.MotionID, ri.Item.MotionParams, "", layer.DurationFrames, 0, ri.Item.ID, "data", false)
			if err != nil {
				return nil, err
			}
			applyMotionRouting(&layer, animation)
		}
		return []Layer{layer}, nil
	case isMapKind(ri.Kind):
		if ri.Map == nil {
			return nil, fmt.Errorf("overlay: map item %q was not resolved before compile", ri.Item.ID)
		}
		return compileResolvedMapLayers(ri, src, registry, ri.Map)
	default:
		layer, err := compileTextLayer(ri, src, ri.Item.ID)
		if err != nil {
			return nil, err
		}
		return compileTextVisualAccentLayers(ri, src, layer)
	}
}
