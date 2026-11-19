package overlay

// chain_compile.go owns the Multi-Animations Runtime lowering: one
// phrase_chain / image_chain / mixed_chain item becomes N timed layers
// inside the SAME Chronon render pass, each with its own motion.
//
// The producer (BrainRust `coalesce_chained_overlays`, formerly PipelineGen
// CoalesceChainedOverlays) spans the chain from first.Start to last.End and
// rides every segment in Params["chain_segments"]
// (absolute plan microseconds + per-segment text/motion/preset/asset). This
// file never re-derives editorial timing: it offsets each segment against the
// chain window and lowers it through the SAME per-kind compiler a standalone
// item would use. Segments keep their own entrance motions and hand off with
// the chain_camera fly-under dip + fade: every non-first segment's layers
// carry TransitionIn fly_under_fade, which the daemon executes natively
// (NativeBlend) or on the CPU fallback.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// chainSegment mirrors PipelineGen's ChainSegment on the wire. StartUS and
// DurationUS are absolute plan microseconds; map layers, captions, styles,
// composite layers and assets ride per segment so numbers, cards, composite
// images and maps lower through their own compilers.
type chainSegment struct {
	Text                string               `json:"text"`
	GroupID             string               `json:"group_id,omitempty"`
	SubgroupID          string               `json:"subgroup_id,omitempty"`
	MotionID            string               `json:"motion_id"`
	PresetID            string               `json:"preset_id"`
	ImagePresetID       string               `json:"image_preset_id"`
	TemplateID          string               `json:"template_id"`
	Kind                string               `json:"kind"`
	EntityID            string               `json:"entity_id"`
	EntityCaption       string               `json:"entity_caption"`
	CaptionMotionID     string               `json:"caption_motion_id"`
	CaptionMotionParams map[string]any       `json:"caption_motion_params"`
	CaptionFontFamily   string               `json:"caption_font_family"`
	CaptionLayout       string               `json:"caption_layout"`
	CaptionColor        string               `json:"caption_color"`
	EntityStyleID       string               `json:"entity_style_id"`
	CameraMotionID      string               `json:"camera_motion_id"`
	StartUS             int64                `json:"start_us"`
	DurationUS          int64                `json:"duration_us"`
	MotionParams        map[string]any       `json:"motion_params"`
	AssetID             string               `json:"asset_id"`
	Assets              []SemanticAssetRef   `json:"assets"`
	ImageLayers         []SemanticImageLayer `json:"image_layers"`
	Frame               *semanticSourceFrame `json:"frame"`
	Params              map[string]any       `json:"params"`
	Style               map[string]any       `json:"style"`
	Map                 *SemanticMap         `json:"map"`
}

// decodeChainSegments extracts the producer's segment list from the merged
// params (item.Params + item.Style). Params arrive as map[string]any from
// JSON, so decode via round-trip instead of assuming concrete Go types — a
// failed decode is a producer bug and fails closed.
func decodeChainSegments(params map[string]any, itemID string) ([]chainSegment, error) {
	raw, ok := params["chain_segments"]
	if !ok {
		return nil, fmt.Errorf("overlay: chain item %q carries no chain_segments", itemID)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("overlay: chain item %q segments are not serializable: %w", itemID, err)
	}
	var segments []chainSegment
	if err := json.Unmarshal(data, &segments); err != nil {
		return nil, fmt.Errorf("overlay: chain item %q chain_segments decode: %w", itemID, err)
	}
	if len(segments) < 2 {
		return nil, fmt.Errorf("overlay: chain item %q needs at least two segments, got %d", itemID, len(segments))
	}
	return segments, nil
}

// segmentIdentity resolves a chain segment's kind and template.
//
// The producer writes both (`ChainSegment` in BrainRust's planner), so this is
// a compatibility shim for hand-authored plans and for segments produced
// before per-segment identity existed — not a second decision layer. It
// therefore only infers what the content names unambiguously: a segment that
// carries both a map and image bytes, or carries no content at all, fails
// closed instead of being guessed into the nearest family.
func segmentIdentity(seg chainSegment, itemID string, index int) (string, string, error) {
	segKind := strings.TrimSpace(seg.Kind)
	segTemplate := strings.TrimSpace(seg.TemplateID)
	hasMap := seg.Map != nil
	hasImage := len(seg.Assets) > 0 || strings.TrimSpace(seg.AssetID) != "" || len(seg.ImageLayers) > 0
	if segKind == "" {
		switch {
		case hasMap && hasImage:
			return "", "", fmt.Errorf("overlay: chain item %q segment %d carries both a map and image content; its kind cannot be inferred", itemID, index)
		case hasMap:
			segKind = string(KindMap)
		case hasImage:
			segKind = string(KindImageSynonym)
		default:
			segKind = string(KindTextPhraseSynonym)
		}
	}
	if !segmentCarriesContent(seg) {
		return "", "", fmt.Errorf("overlay: chain item %q segment %d carries no content", itemID, index)
	}
	if segTemplate == "" {
		switch ItemKind(segKind) {
		case KindMap:
			segTemplate = "MAP"
		case KindImageSynonym, KindEntityImage, KindProduct, KindLogo:
			segTemplate = "IMAGE_OVERLAY"
		case KindEntityCard, KindOrganization, KindLocation, KindConcept, KindLowerThird:
			segTemplate = "person_default"
		case KindNumber:
			segTemplate = "NUMBER"
		default:
			segTemplate = "IMPORTANT_PHRASE"
		}
	}
	return segKind, segTemplate, nil
}

// segmentCarriesContent mirrors the producer's fail-closed segment check: a
// segment with nothing to draw is a producer bug, never an empty layer.
func segmentCarriesContent(seg chainSegment) bool {
	return strings.TrimSpace(seg.Text) != "" ||
		strings.TrimSpace(seg.EntityCaption) != "" ||
		strings.TrimSpace(seg.AssetID) != "" ||
		len(seg.Assets) > 0 ||
		len(seg.ImageLayers) > 0 ||
		seg.Map != nil ||
		strings.TrimSpace(seg.MotionID) != "" ||
		seg.Params != nil ||
		seg.Style != nil
}

// segRIItem rebuilds one chain segment as the standalone semantic item it
// was fused from: the segment's own kind/template/text/motion/assets/
// layers/map/caption/style with the chain's absolute timing. AssetRefs fall
// back to the chain-level assets filtered by AssetID for segments produced
// before per-segment asset lists existed.
func segRIItem(seg chainSegment, chain semanticItem, itemID string, index int) (semanticItem, error) {
	segKind, segTemplate, err := segmentIdentity(seg, itemID, index)
	if err != nil {
		return semanticItem{}, err
	}
	item := chain
	item.ID = chain.ID
	// A segment without its own kind must NEVER inherit the chain's: it would
	// resolve back to a chain kind and recurse forever through compileItem.
	item.Kind = segKind
	item.GroupID = seg.GroupID
	item.SubgroupID = seg.SubgroupID
	item.Template = segTemplate
	item.Text = seg.Text
	item.MotionID = seg.MotionID
	item.PresetID = seg.PresetID
	item.ImagePresetID = seg.ImagePresetID
	item.MotionParams = seg.MotionParams
	item.EntityID = seg.EntityID
	item.EntityCaption = seg.EntityCaption
	item.CaptionMotionID = seg.CaptionMotionID
	item.CaptionMotionParams = seg.CaptionMotionParams
	item.CaptionFontFamily = seg.CaptionFontFamily
	item.CaptionLayout = seg.CaptionLayout
	item.CaptionColor = seg.CaptionColor
	item.EntityStyleID = seg.EntityStyleID
	item.CameraMotionID = seg.CameraMotionID
	item.ImageLayers = append([]SemanticImageLayer(nil), seg.ImageLayers...)
	item.Frame = seg.Frame
	item.Params = seg.Params
	item.Style = seg.Style
	item.Map = seg.Map
	item.Metric = nil
	item.Date = nil
	// Chain coalescing excludes item-level SFX because the Chronon audio bus
	// is plan-scoped, not segment-scoped. Fail closed for hand-authored chains
	// that attempt to smuggle a chain-level cue into a segment.
	item.SoundEffect = nil
	switch {
	case len(seg.Assets) > 0:
		item.Assets = append([]SemanticAssetRef(nil), seg.Assets...)
	case strings.TrimSpace(seg.AssetID) != "":
		kept := make([]SemanticAssetRef, 0, len(chain.Assets))
		for _, a := range chain.Assets {
			if strings.TrimSpace(a.ID) == strings.TrimSpace(seg.AssetID) {
				kept = append(kept, a)
			}
		}
		item.Assets = kept
	default:
		item.Assets = nil
	}
	return item, nil
}

func chainSequenceCamera(ri resolvedItem, fpsNum, fpsDen int) (string, semanticItem, int64, int64, bool) {
	segments, err := decodeChainSegments(ri.Params, ri.Item.ID)
	if err != nil {
		return "", semanticItem{}, 0, 0, false
	}
	for _, segment := range segments {
		if segment.CameraMotionID == "" || len(segment.ImageLayers) < 2 || segment.DurationUS <= 0 {
			continue
		}
		startUS := segment.StartUS
		endUS := startUS + segment.DurationUS
		startFrame, endFrame := usFrames(startUS, endUS, int64(fpsNum), int64(fpsDen))
		// A segment whose identity cannot be resolved is reported by
		// compileChainItem, which fails the whole compile; here it only means
		// this segment contributes no extra camera controller.
		segItem, err := segRIItem(segment, ri.Item, ri.Item.ID, 0)
		if err != nil {
			continue
		}
		return segment.CameraMotionID, segItem, startFrame, endFrame, true
	}
	return "", semanticItem{}, 0, 0, false
}

// segParamsForKind scopes the chain-level params to one segment. A mixed
// chain inherits every segment's Params from the chain item, which mirrors
// the FIRST segment: when the first segment is text, its text runtime
// overrides (font_size_px, glow_size, …) would ride along on image, map,
// shape and video siblings, where validateTextRuntimeOverrides must reject
// them. Drop exactly the keys the validator forbids for that kind, keeping
// the full runtime text style for captioned entity images, which the
// validator allows and the caption compiler lowers onto the caption layer —
// the payload's font, glow, stroke and shadow reach the caption instead of
// falling back to preset constants.
func segParamsForKind(chainParams map[string]any, segKind ItemKind, hasImage, hasCaption bool) map[string]any {
	captionedEntityImage := (isEntityKind(segKind) || isImageKind(segKind)) && hasImage && hasCaption
	if !isShapeKind(segKind) && !isImageKind(segKind) && !isVideoKind(segKind) && !(isEntityKind(segKind) && hasImage) {
		return chainParams
	}
	filtered := make(map[string]any, len(chainParams))
	for k, v := range chainParams {
		filtered[k] = v
	}
	for _, key := range runtimeTextStyleKeys {
		if captionedEntityImage {
			continue
		}
		if key == "fill" && isShapeKind(segKind) {
			// Shape segments own `fill` as geometry (bar/rect color),
			// mirroring validateTextRuntimeOverrides: stripping it here
			// would silently drop a shape's color on mixed chains.
			continue
		}
		delete(filtered, key)
	}
	return filtered
}

// compileChainItem lowers one chain item to N layers in the same pass. Every
// segment is rebuilt as its own semantic item (kind/template/text/motion/
// assets/layers/map/caption/style) and lowered through the SAME dispatcher a
// standalone item uses, so numbers, cards, composite images and maps keep
// their own compilers. Every segment after the first receives the fly-under
// dip + fade transition.
func compileChainItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	segments, err := decodeChainSegments(ri.Params, ri.Item.ID)
	if err != nil {
		return nil, err
	}
	// A chain camera-pan turns the image run into ONE continuous camera move:
	// the segments sit on a strip one canvas apart and the strip travels so each
	// segment lands at the centre in turn. That is the group choreography, not N
	// independent single-image entrances. It is opt-in through the producer's
	// `chain_camera` value and only valid for single-asset image segments.
	panSpec, panMode := chainPanSpecFor(ri.Params)
	if panMode && ri.Kind != KindImageChain {
		return nil, fmt.Errorf("overlay: chain item %q chain_camera %q requires an image_chain", ri.Item.ID, ri.Params["chain_camera"])
	}
	panSpacing := 0.0
	var panArrivals []int64
	if panMode {
		panSpacing = float64(src.Width)
		if panSpec.axis == "y" {
			// One canvas height leaves the second-next member peeking into
			// the frame while an earlier member holds; Chronon then prepares
			// an inactive full-canvas surface and reuses it. 1.5x keeps every
			// non-active vertical member fully off-canvas.
			panSpacing = float64(src.Height) * 3 / 2
		}
		panArrivals = make([]int64, len(segments))
		for n, seg := range segments {
			kind, _, err := segmentIdentity(seg, ri.Item.ID, n)
			if err != nil {
				return nil, err
			}
			if !isImageKind(ItemKind(kind)) || len(seg.Assets) != 1 || len(seg.ImageLayers) != 0 || seg.Map != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d cannot join a camera pan: it must be a single-asset image segment", ri.Item.ID, n)
			}
			startFrame, _ := usFrames(seg.StartUS, seg.StartUS+seg.DurationUS, int64(src.FPSNum), int64(src.FPSDen))
			panArrivals[n] = startFrame - ri.Start
			if panArrivals[n] < 0 {
				panArrivals[n] = 0
			}
		}
		chainFrames := ri.End - ri.Start
		for n := 1; n < len(panArrivals); n++ {
			if panArrivals[n] <= panArrivals[n-1] {
				panArrivals[n] = panArrivals[n-1] + 1
			}
			if chainFrames > 1 && panArrivals[n] >= chainFrames {
				panArrivals[n] = chainFrames - 1
			}
		}
	}
	// Maps are standalone by contract. A hand-built mixed/image/text chain
	// carrying a map would lower a georeferenced full-canvas segment inside a
	// container that cannot validate its LOD window; reject it before any
	// layer is produced. A map_chain, conversely, is only valid when every
	// segment actually carries a map.
	mapChain := ri.Kind == KindMapChain
	for n, seg := range segments {
		if !mapChain && seg.Map != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d carries a map; maps must stay standalone", ri.Item.ID, n)
		}
		if mapChain && seg.Map == nil {
			return nil, fmt.Errorf("overlay: map chain item %q segment %d has no map payload", ri.Item.ID, n)
		}
	}
	out := make([]Layer, 0, len(segments))
	for n, seg := range segments {
		if seg.DurationUS <= 0 || seg.StartUS < 0 || seg.StartUS > int64(^uint64(0)>>1)-seg.DurationUS {
			return nil, fmt.Errorf("overlay: chain item %q segment %d has invalid timing", ri.Item.ID, n)
		}
		segEndUS := seg.StartUS + seg.DurationUS
		chainStartUS := ri.Item.StartMS * 1000
		chainEndUS := ri.Item.EndMS * 1000
		if ri.Item.StartUS != nil && ri.Item.DurationUS != nil {
			chainStartUS = *ri.Item.StartUS
			chainEndUS = *ri.Item.StartUS + *ri.Item.DurationUS
		}
		if seg.StartUS < chainStartUS || segEndUS > chainEndUS {
			return nil, fmt.Errorf("overlay: chain item %q segment %d [%d,%d) us escapes the chain window [%d,%d) us", ri.Item.ID, n, seg.StartUS, segEndUS, chainStartUS, chainEndUS)
		}
		start, end := usFrames(seg.StartUS, segEndUS, int64(src.FPSNum), int64(src.FPSDen))
		if end <= start {
			return nil, fmt.Errorf("overlay: chain item %q segment %d collapses to no frames", ri.Item.ID, n)
		}
		segID := fmt.Sprintf("%s:seg%d", ri.Item.ID, n)
		segItem, err := segRIItem(seg, ri.Item, ri.Item.ID, n)
		if err != nil {
			return nil, err
		}
		segItem.ID = segID
		segSpec := templateSpecFor(segItem.Template)
		segKind, err := segSpec.resolveKind(segItem.Kind, segID)
		if err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		if strings.TrimSpace(segItem.Template) != "" && !segSpec.Registered {
			return nil, fmt.Errorf("overlay: chain item %q segment %d names unknown template %q", ri.Item.ID, n, segItem.Template)
		}
		if err := validateTextElementContract(segItem); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		if err := validateSemanticImageLayers(segItem, segKind); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		if err := validateMapContract(segItem, segKind, src.Width, src.Height); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		if isImageKind(segKind) {
			segSpec.Family = PresetImage
		} else if segSpec.Kind == KindPrimitive || segKind == KindNumber || segKind == KindMetricStat || segKind == KindTimelineDate {
			segSpec.Family = PresetText
		}
		segParams := mergeSemanticParams(segItem.Params, segItem.Style)
		segRI := resolvedItem{Item: segItem, Spec: segSpec, Kind: segKind, Params: segParams, Start: start, End: end}
		if preset, err := presetFor(segItem, segSpec); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		} else if preset != "" {
			segRI.PresetID = preset
			def, err := resolveOfficialPreset(preset, string(segSpec.Family))
			if err != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
			}
			segRI.Preset = def
		}
		if err := validateChildCaptionStyles(segItem); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		segHasImage := len(segItem.Assets) > 0
		segHasCaption := itemHasCaption(segItem)
		segParams = segParamsForKind(segParams, segKind, segHasImage, segHasCaption)
		if err := validateTextRuntimeOverrides(segParams, segID, segKind, segRI.PresetID, segHasImage, segHasCaption); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		segRI.Params = segParams
		// A chain owns N independent photos, so a chain-level image_backdrop is
		// projected onto every image segment and blurred behind its own card.
		// A segment that declares its own block keeps it.
		if isImageKind(segKind) && len(segItem.Assets) > 0 {
			if _, declared := segParams["image_backdrop"]; !declared {
				if backdrop, requested := ri.Params["image_backdrop"]; requested && backdrop != nil {
					segParams["image_backdrop"] = backdrop
					segRI.Params = segParams
				}
			}
		}
		if isMapKind(segKind) {
			resolved, err := resolveMap(segItem.Map, segItem.Assets, src.Width, src.Height, end-start)
			if err != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d resolve: %w", ri.Item.ID, n, err)
			}
			segRI.Map = resolved
		}
		if (isEntityKind(segKind) || (isImageKind(segKind) && segItem.EntityStyleID != "")) && segItem.ImagePresetID != "" && len(segItem.Assets) > 0 {
			def, err := resolveOfficialPreset(segItem.ImagePresetID, string(PresetImage))
			if err != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
			}
			segRI.ImagePreset = def
		}
		if panMode {
			// The strip owns the camera; a per-segment scene camera would be a
			// second controller and a per-segment position still would fight the
			// group track.
			segItem.CameraMotionID = ""
			segRI.Item.CameraMotionID = ""
		}
		var layers []Layer
		// Lower through the same dispatcher as standalone items (map and
		// entity branches included); segment kinds are never chains, so
		// this recurses at most one level.
		if layers, err = compileItem(segRI, src, registry); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		if panMode {
			chainFrames := ri.End - ri.Start
			pan := chainPanTrack(panArrivals, len(segments), panSpacing, chainFrames, panSpec)
			panStarts, panEnds := chainPanWindows(panArrivals, len(segments), chainFrames)
			offset := float64(n) * panSpacing * panSpec.layoutSign
			segStart, segEnd := usFrames(seg.StartUS, seg.StartUS+seg.DurationUS, int64(src.FPSNum), int64(src.FPSDen))
			if segEnd > ri.End {
				segEnd = ri.End
			}
			for i := range layers {
				// Images (and their card decorations) live on the strip for the
				// whole chain; a caption only exists during its own segment, so it
				// appears exactly when its image reaches the centre and cannot sit
				// on a neighbouring standalone overlay for the rest of the run.
				isCaption := layers[i].CaptionForImageID != ""
				// A blurred backdrop is the SET, not a member: it stays centred
				// while the strip travels, so the camera move never drags the
				// background off-canvas.
				isBackdrop := strings.HasSuffix(layers[i].ID, ":image-backdrop")
				// A member exists only while the camera can see it (plus the
				// travel overlap with its neighbour), so Chronon never has to
				// keep three full-size off-canvas surfaces resident for the
				// whole chain.
				imageStart := ri.Start + panStarts[n]
				imageEnd := ri.Start + panEnds[n]
				// Members overlap for the whole handoff: the outgoing image is
				// still on screen while the incoming one travels in, so the
				// chain never falls back to the bare background. Chain-camera
				// plans run on the software encoder (see the execution policy),
				// where two animated full-canvas surfaces are supported.
				if imageStart < ri.Start {
					imageStart = ri.Start
				}
				if imageEnd > ri.End {
					imageEnd = ri.End
				}
				if imageEnd <= imageStart {
					imageEnd = imageStart + 1
				}
				if !isCaption {
					layers[i].StartFrame = imageStart
					layers[i].DurationFrames = imageEnd - imageStart
				} else {
					captionStart := segStart
					captionEnd := segEnd
					// The caption must be gone before its image's surface is
					// retired; ending both on the same frame is the native
					// residency race this trim exists to avoid.
					if captionEnd > imageEnd {
						captionEnd = imageEnd
					}
					if captionEnd-captionStart > 2 {
						captionEnd -= 2
					}
					if captionEnd <= captionStart {
						captionEnd = captionStart + 1
					}
					layers[i].StartFrame = captionStart
					layers[i].DurationFrames = captionEnd - captionStart
				}
				if !isCaption {
					if isBackdrop {
						layers[i].Position = []float64{0, 0}
						layers[i].Animation = nil
						layers[i].ChainPanOffset = nil
					} else {
						layers[i].Position = offsetChainPosition(layers[i].Position, panSpec.axis, offset)
						// Chronon evaluates a layer's tracks in layer-local frames; the
						// shared strip track is authored on the chain timeline, so shift
						// it back by this member's start before attaching it.
						layers[i].Animation = rebaseLayerAnimation(
							chainPanAnimation(pan, chainFrames, panSpec.axis),
							layers[i].StartFrame,
						)
						// Chronon samples a not-yet-started image at local frame 0, so
						// park incoming members fully off-canvas at that key instead of
						// one spacing step away (which leaves a visible edge).
						if n > 0 {
							for t := range layers[i].Animation.Tracks {
								track := &layers[i].Animation.Tracks[t]
								if track.Property == "position_"+panSpec.axis && len(track.Keyframes) > 0 {
									track.Keyframes[0].Value = panSpacing / 4
								}
							}
						}
						layers[i].ChainPanOffset = []float64{0, 0}
						if panSpec.axis == "y" {
							layers[i].ChainPanOffset[1] = offset
						} else {
							layers[i].ChainPanOffset[0] = offset
						}
					}
				} else {
					// A moving mtsdf text surface can be released late by Chronon
					// while the composite still samples it. Keep the caption at the
					// segment's resting slot: it is already centred when the image
					// arrives and never needs a full-canvas moving text surface.
					layers[i].Position = append([]float64(nil), layers[i].Position...)
					// Captions share the strip timeline but must stay invisible
					// before their own segment: a pre-start layer is sampled at
					// local frame 0, so gate it with a one-frame opacity ramp.
					captionLast := layers[i].DurationFrames - 1
					if captionLast < 1 {
						captionLast = 1
					}
					layers[i].Animation = &LayerAnimation{Tracks: []AnimationTrack{{
						Property: "opacity",
						Keyframes: []AnimationKeyframe{
							{Frame: 0, Value: 0.0},
							{Frame: 1, Value: 1.0},
							{Frame: captionLast, Value: 1.0},
						},
					}}}
					layers[i].ChainPanOffset = nil
					layers[i].CaptionForImageID = ""
				}
				layers[i].TextAnimators = nil
				// A projected image gets a tight producer surface; a flat screen-
				// space animated image is forced to a full-canvas surface by the
				// native video lane, and two of those exhaust the graph slots.
				layers[i].Enable3D = false
				layers[i].TransitionIn = nil
			}
		}
		// The camera passes under between segments: stamp the fly-under
		// dip + fade on every non-first segment's layers so the handoff
		// reads as one continuous shot. The daemon executes it natively
		// (NativeBlend dip offset + opacity) with a CPU fallback, so the
		// stamp is safe on every execution path. The fade is clamped to
		// the segment length so short segments still decode (the daemon
		// clamps degenerate windows to a single-frame cut itself).
		// A map's own camera_move is its entrance animation. Applying the
		// generic fly-under transition to its georeferenced layers compounds
		// a second camera movement and makes the map smear/double during entry.
		if !panMode && n > 0 && !isMapKind(segKind) {
			fadeFrames := int64(12)
			if fps := int64(src.FPSNum); fps > 0 && int64(src.FPSDen) > 0 {
				fadeFrames = fps * 12 / 24 / int64(src.FPSDen)
				if fadeFrames < 6 {
					fadeFrames = 6
				}
				if fadeFrames > 24 {
					fadeFrames = 24
				}
			}
			if segFrames := end - start; fadeFrames > segFrames {
				fadeFrames = segFrames
			}
			for i := range layers {
				layers[i].TransitionIn = &LayerTransition{
					ID: "fly_under_fade", Kind: "fly_under_fade",
					DurationFrames: fadeFrames, Direction: "up",
					Easing: "in_out_cubic",
					Params: map[string]any{"dip_px": 60.0},
				}
			}
		}
		out = append(out, layers...)
	}
	return out, nil
}

// chainPanSpecFor resolves the producer's chain-level camera choice. Only the
// four certified camera-pan directions switch the chain into strip mode; every
// other value (including the legacy fly_under_fade) keeps the segment handoff.
func chainPanSpecFor(params map[string]any) (cameraPanSpec, bool) {
	raw, ok := params["chain_camera"].(string)
	if !ok {
		return cameraPanSpec{}, false
	}
	spec, ok := cameraPanSpecs[strings.TrimSpace(raw)]
	return spec, ok
}

// chainPanTrack is the shared staircase for a chain camera pan. Arrivals are
// the segment starts relative to the chain, so each image reaches the centre
// exactly when its chain segment starts; the last image holds until the end.
func chainPanTrack(arrivals []int64, count int, spacing float64, duration int64, spec cameraPanSpec) AnimationTrack {
	property := "position_x"
	if spec.axis == "y" {
		property = "position_y"
	}
	track := AnimationTrack{Property: property, Easing: "in_out_sine"}
	if duration <= 0 || count < 2 {
		track.Keyframes = []AnimationKeyframe{{Frame: 0, Value: 0.0}}
		return track
	}
	keys := []AnimationKeyframe{{Frame: 0, Value: 0.0}}
	lastFrame := int64(0)
	step := duration / int64(count)
	if step < 1 {
		step = 1
	}
	travel := step * 3 / 5
	if travel < 1 {
		travel = 1
	}
	for index := 1; index < count && index < len(arrivals); index++ {
		arrival := arrivals[index]
		gap := arrival - lastFrame
		if gap < 2 {
			gap = 2
		}
		stepTravel := travel
		if stepTravel > gap*3/5 {
			stepTravel = gap * 3 / 5
		}
		if stepTravel < 1 {
			stepTravel = 1
		}
		moveStart := arrival - stepTravel
		if moveStart <= lastFrame {
			moveStart = lastFrame + 1
		}
		if moveStart >= arrival {
			arrival = moveStart + 1
		}
		if arrival >= duration {
			arrival = duration - 1
		}
		if arrival <= moveStart {
			break
		}
		keys = append(keys,
			AnimationKeyframe{Frame: moveStart, Value: spec.travelSign * float64(index-1) * spacing},
			AnimationKeyframe{Frame: arrival, Value: spec.travelSign * float64(index) * spacing},
		)
		lastFrame = arrival
	}
	end := lastValidFrame(duration)
	if end > lastFrame {
		keys = append(keys, AnimationKeyframe{Frame: end, Value: spec.travelSign * float64(count-1) * spacing})
	}
	track.Keyframes = keys
	return track
}

// chainPanAnimation is the single animation every layer of a strip segment
// shares: the travelling position track on the pan axis, a neutral track on
// the other axis, and constant opacity. No per-image entrance survives.
func chainPanAnimation(pan AnimationTrack, duration int64, axis string) *LayerAnimation {
	last := lastValidFrame(duration)
	neutral := func(property string, value float64) AnimationTrack {
		return AnimationTrack{Property: property, Keyframes: []AnimationKeyframe{
			{Frame: 0, Value: value},
			{Frame: last, Value: value},
		}}
	}
	positionX := neutral("position_x", 0)
	positionY := neutral("position_y", 0)
	if axis == "y" {
		positionY = pan
	} else {
		positionX = pan
	}
	return &LayerAnimation{Tracks: []AnimationTrack{
		positionX,
		positionY,
		neutral("opacity", 1),
	}}
}

// chainPanWindows keeps only the active image and its incoming neighbour on
// the timeline. `starts[i]` is when member i begins its travel; `ends[i-1]` is
// the arrival of member i, so the two overlap exactly for the handoff.
func chainPanWindows(arrivals []int64, count int, duration int64) ([]int64, []int64) {
	starts := make([]int64, count)
	ends := make([]int64, count)
	if count == 0 {
		return starts, ends
	}
	step := duration / int64(count)
	if step < 1 {
		step = 1
	}
	travel := step * 3 / 5
	if travel < 1 {
		travel = 1
	}
	lastArrival := int64(0)
	for index := 1; index < count; index++ {
		arrival := arrivals[index]
		gap := arrival - lastArrival
		if gap < 2 {
			gap = 2
		}
		stepTravel := travel
		if stepTravel > gap*3/5 {
			stepTravel = gap * 3 / 5
		}
		if stepTravel < 1 {
			stepTravel = 1
		}
		start := arrival - stepTravel
		if start <= lastArrival {
			start = lastArrival + 1
		}
		starts[index] = start
		ends[index-1] = arrival
		lastArrival = arrival
	}
	ends[count-1] = duration
	return starts, ends
}

// rebaseLayerAnimation moves a chain-timeline track onto a layer that starts
// at `start`. Chronon evaluates tracks in layer-local frames and rejects
// negative keyframes, so the first key is the sampled value at the layer's
// start and every later key is shifted back by the same amount.
func rebaseLayerAnimation(anim *LayerAnimation, start int64) *LayerAnimation {
	if anim == nil || start <= 0 {
		return anim
	}
	for t := range anim.Tracks {
		track := &anim.Tracks[t]
		lead := sampleTrackAt(*track, start)
		keys := []AnimationKeyframe{{Frame: 0, Value: lead}}
		for _, key := range track.Keyframes {
			frame := key.Frame - start
			if frame <= 0 || frame == keys[len(keys)-1].Frame {
				continue
			}
			key.Frame = frame
			keys = append(keys, key)
		}
		track.Keyframes = keys
	}
	return anim
}

// sampleTrackAt linearly interpolates a track at an absolute chain frame.
// The chain track uses the same easing for every segment, so sampling the
// endpoints through the eased curve is enough for the layer's first key.
func sampleTrackAt(track AnimationTrack, frame int64) float64 {
	keys := track.Keyframes
	if len(keys) == 0 {
		return 0
	}
	if frame <= keys[0].Frame {
		return asFloatValue(keys[0].Value)
	}
	for i := 1; i < len(keys); i++ {
		if frame > keys[i].Frame {
			continue
		}
		span := keys[i].Frame - keys[i-1].Frame
		if span <= 0 {
			return asFloatValue(keys[i].Value)
		}
		progress := float64(frame-keys[i-1].Frame) / float64(span)
		from, to := asFloatValue(keys[i-1].Value), asFloatValue(keys[i].Value)
		return from + (to-from)*progress
	}
	return asFloatValue(keys[len(keys)-1].Value)
}

func asFloatValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case uint:
		return float64(v)
	case uint64:
		return float64(v)
	default:
		return 0
	}
}

func offsetChainPosition(position []float64, axis string, offset float64) []float64 {
	out := append([]float64(nil), position...)
	for len(out) < 2 {
		out = append(out, 0)
	}
	if axis == "y" {
		out[1] += offset
	} else {
		out[0] += offset
	}
	return out
}
