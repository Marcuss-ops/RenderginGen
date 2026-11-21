package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
)

// chain_segments.go owns the chain wire format: the chainSegment mirror of
// PipelineGen's ChainSegment, its decode/validation, the per-segment identity
// that keeps segment layers unique, and the per-kind parameter narrowing that
// reuses each standalone compiler. Timing math lives in chain_pan.go.

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
