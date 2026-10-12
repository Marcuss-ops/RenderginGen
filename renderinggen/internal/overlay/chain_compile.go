package overlay

// chain_compile.go owns the Multi-Animations Runtime lowering: one
// phrase_chain / image_chain / mixed_chain item becomes N timed layers
// inside the SAME Chronon render pass, each with its own motion.
//
// The producer (PipelineGen CoalesceChainedOverlays) spans the chain from
// first.Start to last.End and rides every segment in Params["chain_segments"]
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

// isChainKind reports whether a resolved kind is a fused multi-animation
// chain rather than a standalone item.
func isChainKind(kind ItemKind) bool {
	switch ItemKind(strings.ToLower(strings.TrimSpace(string(kind)))) {
	case ItemKind("phrase_chain"), ItemKind("image_chain"), ItemKind("mixed_chain"), ItemKind("map_chain"):
		return true
	}
	return false
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

// segRIItem rebuilds one chain segment as the standalone semantic item it
// was fused from: the segment's own kind/template/text/motion/assets/
// layers/map/caption/style with the chain's absolute timing. AssetRefs fall
// back to the chain-level assets filtered by AssetID for segments produced
// before per-segment asset lists existed.
func segRIItem(seg chainSegment, chain semanticItem) semanticItem {
	item := chain
	item.ID = chain.ID
	// A segment without its own kind/template must NEVER inherit the
	// chain's: it would resolve back to a chain kind and recurse forever
	// through compileItem. Default from content (map → map, assets →
	// image, otherwise text), mirroring the producer's chain families.
	segKind := strings.TrimSpace(seg.Kind)
	segTemplate := strings.TrimSpace(seg.TemplateID)
	if segKind == "" {
		switch {
		case seg.Map != nil:
			segKind = "map"
		case len(seg.Assets) > 0 || strings.TrimSpace(seg.AssetID) != "" || len(seg.ImageLayers) > 0:
			segKind = "image"
		default:
			segKind = "text_phrase"
		}
	}
	if segTemplate == "" {
		switch segKind {
		case "map":
			segTemplate = "MAP"
		case "image", "entity_image", "product", "logo":
			segTemplate = "IMAGE_OVERLAY"
		case "entity_card", "organization", "location", "concept", "lower_third":
			segTemplate = "person_default"
		case "number":
			segTemplate = "NUMBER"
		default:
			segTemplate = "IMPORTANT_PHRASE"
		}
	}
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
	return item
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
		segItem := segRIItem(seg, ri.Item)
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
		var layers []Layer
		// Lower through the same dispatcher as standalone items (map and
		// entity branches included); segment kinds are never chains, so
		// this recurses at most one level.
		if layers, err = compileItem(segRI, src, registry); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		}
		// The camera passes under between segments: stamp the fly-under
		// dip + fade on every non-first segment's layers so the handoff
		// reads as one continuous shot. The daemon executes it natively
		// (NativeBlend dip offset + opacity) with a CPU fallback, so the
		// stamp is safe on every execution path. The fade is clamped to
		// the segment length so short segments still decode (the daemon
		// clamps degenerate windows to a single-frame cut itself).
		if n > 0 {
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
