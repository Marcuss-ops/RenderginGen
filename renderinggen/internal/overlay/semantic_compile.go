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
	// LegacyCompositeImage marks an image_layers item with no group_motion_id.
	// It only compiles when the plan carries legacy_composite_replay; the
	// marker is counted separately so a sealed replay is never confused with
	// a new plan that should have declared a group recipe.
	LegacyCompositeImage  bool
	LegacyCompositeReplay bool
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
	// animation_policies is a retired pre-v2 wire field. Check it before the
	// strict decode so a historical producer gets an actionable migration
	// error instead of the generic DisallowUnknownFields diagnostic.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err == nil {
		if _, retired := envelope["animation_policies"]; retired {
			return semanticPlan{}, fmt.Errorf("animation_policies is retired; migrate to animation groups or explicit motion_id")
		}
	}
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
	return compileSemanticDecoded(&src, CompileOptions{})
}

// compileSemanticDecoded lowers an ALREADY-decoded semantic plan. It is the
// single semantic overlay-plan.v1 → concrete render-plan.v2 lowering; the
// decode happens once upstream so the prepare path never re-reads the raw
// document.
func compileSemanticDecoded(src *semanticPlan, opts CompileOptions) (*Plan, []Asset, Stats, []string, error) {
	validated, err := validateSemanticPlan(src)
	if err != nil {
		return nil, nil, Stats{}, nil, err
	}
	// normalizeCompatibility is the only mutable stage; it works on a deep
	// copy, so the decoded document remains a replayable input.
	normalized, err := normalizeCompatibility(validated)
	if err != nil {
		return nil, nil, Stats{}, nil, err
	}
	resolved, err := resolvePlan(normalized)
	if err != nil {
		return nil, nil, Stats{}, nil, err
	}
	// The probe belongs to the document being lowered; set it after the copy
	// stages so cloneSemanticPlan cannot drop it.
	resolved.Document.assetSizes = opts.AssetSizes
	concrete, stats, err := lowerPlan(resolved)
	if err != nil {
		return nil, nil, Stats{}, nil, err
	}
	return sealPlan(concrete, stats, resolved, opts)
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

// validateCaptionMotionTargets is the live half of the retired animation
// policy pass: a caption motion is admitted only by the canonical catalog's
// text target, whether it arrives on the item or on a composite child.
func validateCaptionMotionTargets(item semanticItem) error {
	if strings.TrimSpace(item.EntityCaption) != "" && item.CaptionMotionID != "" && !motionAdmitsTarget(item.CaptionMotionID, TargetCaption) {
		return fmt.Errorf("overlay: item %q caption motion %q is not supported for text captions", item.ID, item.CaptionMotionID)
	}
	for _, child := range item.ImageLayers {
		if strings.TrimSpace(child.Caption) != "" && child.CaptionMotionID != "" && !motionAdmitsTarget(child.CaptionMotionID, TargetCaption) {
			return fmt.Errorf("overlay: item %q image layer %q caption motion %q is not supported for text captions", item.ID, child.ID, child.CaptionMotionID)
		}
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

// itemEnvelope is the validated timeline projection of one item.
type itemEnvelope struct {
	Start, End int64
}

// itemPresentation is the resolved style/preset projection of one item. It is
// produced after classification and consumed by the same resolvedItem.
type itemPresentation struct {
	Params      map[string]any
	PresetID    string
	Preset      PresetDefinition
	ImagePreset PresetDefinition
}

func resolveSemanticItems(src *semanticPlan) ([]resolvedItem, error) {
	out := make([]resolvedItem, 0, len(src.Items))
	for _, item := range src.Items {
		envelope, err := resolveItemEnvelope(item, src)
		if err != nil {
			return nil, err
		}
		spec, kind, err := classifyItem(item)
		if err != nil {
			return nil, err
		}
		if err := validateResolvedItem(item, spec, kind, src); err != nil {
			return nil, err
		}
		presentation, err := resolveItemPresentation(item, spec, kind, src)
		if err != nil {
			return nil, err
		}
		mapSpec, err := resolveItemMap(item, kind, src, envelope)
		if err != nil {
			return nil, err
		}
		// A chain's segments are decoded ONCE here, at the same stage that
		// validates the item and resolves its params. The lowering then reads
		// the typed list, so a decode failure can no longer surface halfway
		// through layer construction (or twice for the camera controller).
		if isChainKind(kind) {
			segments, err := decodeChainSegments(presentation.Params, item.ID)
			if err != nil {
				return nil, err
			}
			item.chainSegments = segments
		}
		ri := resolvedItem{
			Item: item, Spec: spec, Kind: kind, Params: presentation.Params,
			Start: envelope.Start, End: envelope.End,
			PresetID: presentation.PresetID, Preset: presentation.Preset,
			ImagePreset: presentation.ImagePreset, Map: mapSpec,
		}
		ri.LegacyCompositeImage = len(item.ImageLayers) > 0 && strings.TrimSpace(item.GroupMotionID) == ""
		ri.LegacyCompositeReplay = ri.LegacyCompositeImage && src.LegacyCompositeReplay
		out = append(out, ri)
	}
	if err := validateMapComposition(src.MapCompositionID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveItemEnvelope validates the item identity and timeline. It performs no
// catalog or asset lookup.
func resolveItemEnvelope(item semanticItem, src *semanticPlan) (itemEnvelope, error) {
	start, end := itemFrames(item, int64(src.FPSNum), int64(src.FPSDen))
	startUS, endUS := item.StartMS*1000, item.EndMS*1000
	if item.StartUS != nil && item.DurationUS != nil {
		startUS, endUS = *item.StartUS, *item.StartUS+*item.DurationUS
	}
	if item.ID == "" || item.StartMS < 0 || item.EndMS <= item.StartMS {
		return itemEnvelope{}, fmt.Errorf("overlay: invalid semantic item %q", item.ID)
	}
	if end <= start {
		return itemEnvelope{}, fmt.Errorf("overlay: item %q window [%d,%d) us collapses to no frames at %d/%d fps",
			item.ID, startUS, endUS, src.FPSNum, src.FPSDen)
	}
	if err := validateTextElementContract(item); err != nil {
		return itemEnvelope{}, err
	}
	return itemEnvelope{Start: start, End: end}, nil
}

// classifyItem resolves the template registry row and the authoritative kind.
func classifyItem(item semanticItem) (TemplateSpec, ItemKind, error) {
	spec := templateSpecFor(item.Template)
	kind, err := spec.resolveKind(item.Kind, item.ID)
	if err != nil {
		return TemplateSpec{}, "", err
	}
	// Unknown templates fail closed: a non-empty template_id that resolves to
	// no registry row and no legacy alias is a producer typo or an unregistered
	// addition. An empty template_id stays a valid simple text primitive;
	// compatibility travels only through legacyTemplateAliases.
	if strings.TrimSpace(item.Template) != "" && !spec.Registered {
		return TemplateSpec{}, "", fmt.Errorf("overlay: item %q names unknown template %q: register it in the template registry or add an explicit legacy alias", item.ID, item.Template)
	}
	return spec, kind, nil
}

// validateResolvedItem enforces the per-family contracts that need no preset
// resolution.
func validateResolvedItem(item semanticItem, spec TemplateSpec, kind ItemKind, src *semanticPlan) error {
	if err := validateSemanticImageLayers(item, kind); err != nil {
		return err
	}
	if item.EntityStyleID != "" {
		if _, ok := ResolveEntityStyle(item.EntityStyleID, src.entityStyleSampleKey(), src.VideoID, item.ID); !ok {
			return fmt.Errorf("overlay: item %q has unsupported entity_style_id %q", item.ID, item.EntityStyleID)
		}
		behavior := behaviorOf(kind)
		if behavior != behaviorEntity && behavior != behaviorImage {
			return fmt.Errorf("overlay: item %q entity_style_id is only valid for entity or image items", item.ID)
		}
		if len(item.ImageLayers) > 0 {
			return fmt.Errorf("overlay: item %q entity_style_id does not support composite image_layers", item.ID)
		}
		if len(item.Assets) == 0 || strings.TrimSpace(item.EntityCaption) == "" {
			return fmt.Errorf("overlay: item %q entity_style_id %q requires an image and entity_caption", item.ID, item.EntityStyleID)
		}
	}
	// The map declaration is validated for EVERY item, not only map ones: a map
	// block on a phrase item would otherwise be silently ignored.
	if err := validateMapContract(item, kind, src.Width, src.Height); err != nil {
		return err
	}
	if isImageKind(kind) && kind != KindLightLeak && len(item.Assets) == 0 && len(item.ImageLayers) == 0 {
		return fmt.Errorf("overlay: image template %q item %q requires asset_refs", item.Template, item.ID)
	}
	if kind == KindLightLeak && strings.TrimSpace(item.MotionID) == "" {
		return fmt.Errorf("overlay: light leak item %q requires a certified lightleak motion_id", item.ID)
	}
	if isVideoKind(kind) && len(item.Assets) == 0 {
		return fmt.Errorf("overlay: video overlay template %q item %q requires asset_refs", item.Template, item.ID)
	}
	return nil
}

// resolveItemPresentation resolves the preset family, style precedence and
// text/caption contracts. It never touches map geometry or assets.
func resolveItemPresentation(item semanticItem, spec TemplateSpec, kind ItemKind, src *semanticPlan) (itemPresentation, error) {
	// The resolved kind is authoritative for the preset family too, so an
	// unknown template paired with an explicit image kind still validates
	// against the image catalog. DATE/METRIC presentation kinds stay on the
	// text preset family while their catalog motion_id controls the animation.
	if isImageKind(kind) {
		spec.Family = PresetImage
	} else if spec.Kind == KindPrimitive || kind == KindNumber || kind == KindMetricStat || kind == KindTimelineDate {
		spec.Family = PresetText
	}
	params := mergeSemanticParams(item.Params, item.Style)
	// A chain container has no presentation of its own: its segments do, and a
	// mixed chain may combine text and image families. PipelineGen mirrors the
	// FIRST segment's preset and text overrides onto the container for legacy
	// consumers; validating that mirror against the container kind rejects an
	// image-first mixed chain as "preset is image, not text". compileChainItem
	// validates every segment against its own family, so the container stops
	// here before preset, style and caption resolution.
	if isChainKind(kind) {
		return itemPresentation{Params: params}, nil
	}
	if _, requested := params["image_backdrop"]; requested && !isImageKind(kind) {
		return itemPresentation{}, fmt.Errorf("overlay: item %q image_backdrop is valid only for image items", item.ID)
	}
	preset, err := presetFor(item, spec)
	if err != nil {
		return itemPresentation{}, err
	}
	out := itemPresentation{Params: params, PresetID: preset}
	if preset != "" {
		def, err := resolveOfficialPreset(preset, string(spec.Family))
		if err != nil {
			return itemPresentation{}, err
		}
		out.Preset = def
	}
	if preset == ClassicPhrasePresetID && !isOpaqueWhiteCanvas(src.Background) {
		return itemPresentation{}, fmt.Errorf("overlay: preset %q requires an opaque white canvas background", preset)
	}
	if err := validateChildCaptionStyles(item); err != nil {
		return itemPresentation{}, err
	}
	if err := validateCaptionMotionTargets(item); err != nil {
		return itemPresentation{}, err
	}
	hasCaption := itemHasCaption(item)
	if err := validateTextRuntimeOverrides(params, item.ID, kind, preset, len(item.Assets) > 0, hasCaption); err != nil {
		return itemPresentation{}, err
	}
	if (isEntityKind(kind) || (isImageKind(kind) && item.EntityStyleID != "")) && len(item.Assets) > 0 {
		if imagePreset := strings.TrimSpace(item.ImagePresetID); imagePreset != "" {
			def, err := resolveOfficialPreset(imagePreset, string(PresetImage))
			if err != nil {
				return itemPresentation{}, err
			}
			out.ImagePreset = def
		}
	}
	return out, nil
}

// resolveItemMap resolves only map geometry, after presentation validation so
// error precedence stays the historical one.
func resolveItemMap(item semanticItem, kind ItemKind, src *semanticPlan, envelope itemEnvelope) (*resolvedMap, error) {
	if !isMapKind(kind) || isModernMap1(item) {
		return nil, nil
	}
	mapSpec, err := resolveMap(item.Map, item.Assets, src.Width, src.Height, envelope.End-envelope.Start)
	if err != nil {
		return nil, fmt.Errorf("overlay: map item %q resolve: %w", item.ID, err)
	}
	return mapSpec, nil
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
		// compileEntityGroup owns the members' backdrops through the shared
		// member primitive; the item-level decoration is the shared finalizer.
		return finalizeImageLayers(ri, src, registry, layers, false)
	case isEntityKind(ri.Kind) && len(ri.Item.ImageLayers) > 0:
		return compileImageItem(ri, src, registry)
	case isEntityKind(ri.Kind) && len(ri.Item.Assets) > 0:
		layers, err := compileEntityCard(ri, src, registry)
		if err != nil {
			return nil, err
		}
		// compileEntityCard emits its own single-photo backdrop; only the
		// remaining decoration belongs to the finalizer.
		return finalizeImageLayers(ri, src, registry, layers, false)
	case isImageKind(ri.Kind) && ri.Item.EntityStyleID != "" && len(ri.Item.Assets) > 0:
		if ri.Item.ImagePresetID == "" {
			return nil, fmt.Errorf("overlay: entity image item %q with entity_style_id requires image_preset_id", ri.Item.ID)
		}
		layers, err := compileEntityCard(ri, src, registry)
		if err != nil {
			return nil, err
		}
		return finalizeImageLayers(ri, src, registry, layers, false)
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
		if isModernMap1(ri.Item) {
			return compileModernMap1Layers(ri, src, registry)
		}
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
