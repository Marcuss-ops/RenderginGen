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
