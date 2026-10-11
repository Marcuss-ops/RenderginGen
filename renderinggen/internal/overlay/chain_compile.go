package overlay

// chain_compile.go owns the Multi-Animations Runtime lowering: one
// phrase_chain / image_chain item becomes N timed layers inside the SAME
// Chronon render pass, each with its own motion, linked by a camera
// fly-under dip + fade.
//
// The producer (PipelineGen CoalesceChainedOverlays) spans the chain from
// first.Start to last.End and rides every segment in Params["chain_segments"]
// (absolute plan microseconds + per-segment text/motion/preset/asset). This
// file never re-derives editorial timing: it offsets each segment against the
// chain window and lowers it through the SAME per-kind compiler a standalone
// item would use — then stamps the transition (fly-under dip + fade) on every
// segment after the first, so the camera visibly passes under and reveals the
// next animation instead of popping.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// chainSegment mirrors PipelineGen's ChainSegment on the wire. StartUS and
// DurationUS are absolute plan microseconds; MotionParams rides through.
type chainSegment struct {
	Text         string         `json:"text"`
	MotionID     string         `json:"motion_id"`
	PresetID     string         `json:"preset_id"`
	TemplateID   string         `json:"template_id"`
	Kind         string         `json:"kind"`
	StartUS      int64          `json:"start_us"`
	DurationUS   int64          `json:"duration_us"`
	MotionParams map[string]any `json:"motion_params"`
	AssetID      string         `json:"asset_id"`
}

// isChainKind reports whether a resolved kind is a fused multi-animation
// chain rather than a standalone item.
func isChainKind(kind ItemKind) bool {
	switch ItemKind(strings.ToLower(strings.TrimSpace(string(kind)))) {
	case ItemKind("phrase_chain"), ItemKind("image_chain"):
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

// compileChainItem lowers one chain item to N layers in the same pass. Text
// chains reuse compileTextLayer per segment; image chains reuse
// compileImageItem per segment (single-asset clone). Every segment after the
// first receives the fly-under dip + fade transition.
func compileChainItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	segments, err := decodeChainSegments(ri.Params, ri.Item.ID)
	if err != nil {
		return nil, err
	}
	isImage := ItemKind(strings.ToLower(strings.TrimSpace(string(ri.Kind)))) == ItemKind("image_chain")
	out := make([]Layer, 0, len(segments))
	for n, seg := range segments {
		startMS := seg.StartUS / 1000
		endMS := (seg.StartUS + seg.DurationUS + 999) / 1000
		if seg.DurationUS <= 0 || endMS <= startMS {
			return nil, fmt.Errorf("overlay: chain item %q segment %d has invalid timing", ri.Item.ID, n)
		}
		if startMS < ri.Item.StartMS || endMS > ri.Item.EndMS {
			return nil, fmt.Errorf("overlay: chain item %q segment %d [%d,%d) escapes the chain window [%d,%d)", ri.Item.ID, n, startMS, endMS, ri.Item.StartMS, ri.Item.EndMS)
		}
		start, end := msFrames(startMS, endMS, int64(src.FPSNum), int64(src.FPSDen))
		if end <= start {
			return nil, fmt.Errorf("overlay: chain item %q segment %d collapses to no frames", ri.Item.ID, n)
		}
		segRI := ri
		segRI.Start, segRI.End = start, end
		segItem := segRI.Item
		if strings.TrimSpace(seg.MotionID) != "" {
			segItem.MotionID = seg.MotionID
		}
		if strings.TrimSpace(seg.PresetID) != "" {
			segItem.PresetID = seg.PresetID
		}
		if seg.MotionParams != nil {
			segItem.MotionParams = seg.MotionParams
		}
		if !isImage && strings.TrimSpace(seg.Text) != "" {
			segItem.Text = seg.Text
		}
		segRI.Item = segItem
		// Re-resolve the preset for this segment's (possibly different)
		// preset_id through the same official catalog as standalone items.
		if preset, err := presetFor(segItem, segRI.Spec); err != nil {
			return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
		} else if preset != "" {
			segRI.PresetID = preset
			def, err := resolveOfficialPreset(preset, string(segRI.Spec.Family))
			if err != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
			}
			segRI.Preset = def
		}
		var layers []Layer
		if isImage {
			clone := segRI
			if strings.TrimSpace(seg.AssetID) != "" {
				kept := make([]SemanticAssetRef, 0, len(clone.Item.Assets))
				for _, a := range clone.Item.Assets {
					if strings.TrimSpace(a.ID) == strings.TrimSpace(seg.AssetID) {
						kept = append(kept, a)
					}
				}
				if len(kept) == 0 {
					return nil, fmt.Errorf("overlay: chain item %q segment %d asset %q not in item assets", ri.Item.ID, n, seg.AssetID)
				}
				clone.Item.Assets = kept
			}
			layers, err = compileImageItem(clone, src, registry)
			if err != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
			}
		} else {
			if strings.TrimSpace(segItem.Text) == "" {
				return nil, fmt.Errorf("overlay: chain item %q segment %d carries no text", ri.Item.ID, n)
			}
			layer, err := compileTextLayer(segRI, src, fmt.Sprintf("%s:seg%d", ri.Item.ID, n))
			if err != nil {
				return nil, fmt.Errorf("overlay: chain item %q segment %d: %w", ri.Item.ID, n, err)
			}
			textLayers, err := compileTextVisualAccentLayers(segRI, src, layer)
			if err != nil {
				return nil, err
			}
			layers = textLayers
		}
		if n > 0 {
			// The camera passes under between segments: stamp the
			// fly-under + fade transition on every non-first segment's
			// layers so the handoff reads as one continuous shot.
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
			for i := range layers {
				layers[i].TransitionIn = &LayerTransition{
					ID: "fly_under_fade", Kind: "fly_under_fade",
					DurationFrames: fadeFrames, Direction: "under",
					Easing: "ease_in_out",
					Params: map[string]any{"dip_px": 60.0},
				}
			}
		}
		out = append(out, layers...)
	}
	return out, nil
}
