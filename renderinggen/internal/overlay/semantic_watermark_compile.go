package overlay

// semantic_watermark_compile.go owns the watermark lowering: text or image,
// positioned and styled ONLY from the plan's typed blocks. It is extracted from
// compileSemanticDecoded so that function stays a sequence of plan stages
// (background, source, watermark, items, grid, adjustment) instead of carrying
// the whole watermark geometry contract inline.
import "fmt"

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
	position, size, err := resolveWatermarkGeometry(wm.Position, src.Width, src.Height, margin, wmStyle)
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
