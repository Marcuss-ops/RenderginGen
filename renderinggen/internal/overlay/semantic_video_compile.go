// semantic_video_compile.go owns the video-overlay compiler: a pre-rendered
// segment lowered to a timed video layer inside the same render pass.
// Dispatched from semantic_compile.go's compileItem.

package overlay

import (
	"fmt"
)

// compileVideoOverlayLayer lowers a rendered video overlay to a TIMED video
// layer inside the same render pass. It is the single-pass replacement for
// compositing a pre-rendered segment with a post-render transcoder:
//
//   - Source is the overlay segment's content-addressed path (video layers
//     carry `source`, exactly like the source clip and the background);
//   - StartFrame/DurationFrames come from the item's start_ms/end_ms, and
//     Chronon's VideoNode samples the segment at (frame - layer_start), so
//     the segment's first frame lands on the declared start frame and the
//     layer is composited only inside its window;
//   - the layer is appended AFTER the source layer, so its z-order places the
//     overlay over the clip.
//
// Geometry is full-canvas "cover" by default (the producer renders the
// segment at the output contract), overridable via params.fit. Fail-closed:
// a video overlay without an asset is rejected during resolution.
func compileVideoOverlayLayer(ri resolvedItem, src *semanticPlan, registry *assetRegistry) (Layer, error) {
	if len(ri.Item.Assets) == 0 {
		return Layer{}, fmt.Errorf("overlay: video overlay template %q item %q requires asset_refs", ri.Item.Template, ri.Item.ID)
	}
	fit := stringParam(ri.Params, "fit", FitCover)
	if !isSupportedFit(fit) {
		// The same closed vocabulary as the background; only the message names
		// the item, because this is the only place a per-item fit is validated.
		return Layer{}, fmt.Errorf("overlay: video overlay item %q has unsupported fit %q (supported: %s)", ri.Item.ID, fit, supportedFits)
	}
	layer := Layer{
		ID:             overlayLayerID(ri.Item.ID),
		Type:           "video",
		Source:         registry.Path(ri.Item.Assets[0].ID),
		Size:           []float64{float64(src.Width), float64(src.Height)},
		Fit:            fit,
		StartFrame:     ri.Start,
		DurationFrames: ri.End - ri.Start,
	}
	if opacity, ok := ri.Params["opacity"].(float64); ok {
		if opacity < 0 || opacity > 1 {
			return Layer{}, fmt.Errorf("overlay: video overlay item %q has opacity %v outside [0,1]", ri.Item.ID, opacity)
		}
		layer.Opacity = &opacity
	}
	// scale_percent insets a full-canvas overlay segment so its declared frame
	// is actually visible. It uses the same centred transform contract as the
	// source clip's foreground_scale_percent (which is the only place the
	// modular resolver's implicit canvas-centre shift is cancelled).
	if raw, exists := ri.Params["scale_percent"]; exists {
		percent, ok := numericValue(raw)
		if !ok || percent <= 0 || percent > 100 {
			return Layer{}, fmt.Errorf("overlay: video overlay item %q has scale_percent outside (0,100]", ri.Item.ID)
		}
		if percent < 100 {
			layer.Position = []float64{-float64(src.Width) * 0.5, -float64(src.Height) * 0.5}
			layer.Scale = []float64{percent / 100, percent / 100}
		}
	}
	if err := applyFrameTreatment(&layer, ri.Item.Frame); err != nil {
		return Layer{}, fmt.Errorf("overlay: item %q: %w", ri.Item.ID, err)
	}
	return layer, nil
}

// The watermark lowering: text or image,
// positioned and styled ONLY from the plan's typed blocks. It is extracted from
// compileSemanticDecoded so that function stays a sequence of plan stages
// (background, source, watermark, items, grid, adjustment) instead of carrying
// the whole watermark geometry contract inline.

// compileWatermarkLayer returns the watermark layer when the plan declares one.
// The boolean distinguishes "no watermark" from a zero Layer, and every
// unresolvable declaration is an error instead of a silent fallback.
func compileWatermarkLayer(src *semanticPlan, registry *assetRegistry, durationFrames int64) (Layer, bool, error) {
	wm := src.Watermark
	if wm == nil {
		return Layer{}, false, nil
	}
	font := ""
	if wm.FontRef != nil {
		registered, err := registry.Register(*wm.FontRef)
		if err != nil {
			return Layer{}, false, fmt.Errorf("overlay: watermark font asset: %w", err)
		}
		font = registered
	}
	if font == "" && wm.Text != "" {
		return Layer{}, false, fmt.Errorf("overlay: text watermark requires font_ref")
	}
	wmStyle, err := parseStyleBlock(wm.Style)
	if err != nil {
		return Layer{}, false, err
	}
	style, err := watermarkLayerStyle(wmStyle, font)
	if err != nil {
		return Layer{}, false, err
	}
	margin, err := watermarkMargin(wm.MarginPX)
	if err != nil {
		return Layer{}, false, err
	}
	// Position AND size come back from the one geometry resolver: the box
	// the position was computed against is the box the layer declares.
	position, size, err := resolveWatermarkGeometry(wm.Position, src.Width, src.Height, margin, wm.Text, wmStyle)
	if err != nil {
		return Layer{}, false, err
	}
	wmLayer := Layer{ID: "watermark", StartFrame: 0, DurationFrames: durationFrames,
		Style: style, Size: size}
	if wm.Opacity != nil {
		// Same contract as the background: opacity 0 is a declared value,
		// not an absent key.
		opacity := *wm.Opacity
		wmLayer.Opacity = &opacity
	}
	if wm.Text != "" && len(wm.AssetRefs) == 0 {
		// Text-only watermark.
		wmLayer.Type = "text"
		wmLayer.Text = wm.Text
	} else if len(wm.AssetRefs) > 0 {
		registered, err := registry.Register(wm.AssetRefs[0])
		if err != nil {
			return Layer{}, false, fmt.Errorf("overlay: watermark asset: %w", err)
		}
		wmLayer.Type = "image"
		wmLayer.Asset = registered
		wmLayer.Fit = FitContain
		if wm.Text != "" {
			wmLayer.Text = wm.Text
		}
	} else {
		return Layer{}, false, fmt.Errorf("overlay: watermark requires text or asset_refs")
	}
	// resolveWatermarkGeometry returns the box's absolute canvas top-left;
	// the plan's position field needs the per-type form, so the conversion
	// happens exactly once, after the layer type is known. A text watermark
	// takes the absolute centre, an image watermark the canvas-centre offset.
	wmLayer.Position = canvasBoxPosition(wmLayer.Type,
		position[0], position[1], size[0], size[1], src.Width, src.Height)
	return wmLayer, true, nil
}
