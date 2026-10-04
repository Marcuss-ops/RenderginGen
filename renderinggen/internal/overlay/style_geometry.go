package overlay

import (
	"fmt"
	"strings"
)

// effectiveFontSize resolves the requested font size. PipelineGen owns the
// size; the resolver only normalises the two payload spellings (Size alias /
// FontSizePX). A missing size is a compile failure — the worker has no
// business inventing a default.
func effectiveFontSize(s *styleBlock) (float64, error) {
	if s == nil {
		return 0, fmt.Errorf("overlay: style block is required (font size is owned by PipelineGen, not the worker)")
	}
	size := s.FontSizePX
	if size == 0 {
		size = s.Size
	}
	if size <= 0 {
		return 0, fmt.Errorf("overlay: style block carries no positive font_size_px/size")
	}
	return size, nil
}
func layerStyleFromBlock(s *styleBlock, fontPath string, role string) (*LayerStyle, error) {
	size, err := effectiveFontSize(s)
	if err != nil {
		return nil, fmt.Errorf("overlay: %s %w", role, err)
	}
	fill := strings.TrimSpace(s.Color)
	if fill == "" {
		return nil, fmt.Errorf("overlay: %s style carries no color — PipelineGen must resolve the fill (the worker never invents one)", role)
	}
	style := &LayerStyle{Font: fontPath, FontSize: size, Fill: fill}
	if s.Stroke != nil {
		style.Stroke = &LayerStroke{Color: s.Stroke.Color, Width: s.Stroke.Width}
	}
	if s.Shadow != nil {
		style.Shadow = &LayerShadow{
			Color:   s.Shadow.Color,
			Opacity: s.Shadow.Opacity,
			Blur:    s.Shadow.BlurPX,
			Offset:  []float64{s.Shadow.OffsetX, s.Shadow.OffsetY},
		}
	}
	return style, nil
}

// subtitleLayerStyle converts the plan's subtitle style into a concrete
// Chronon text style. Fails closed when the plan carries no usable style.
func subtitleLayerStyle(s *styleBlock, fontPath string) (*LayerStyle, error) {
	return layerStyleFromBlock(s, fontPath, "subtitle")
}

// watermarkLayerStyle converts the plan's watermark style into a concrete
// Chronon text style. Fails closed when the plan carries no usable style.
func watermarkLayerStyle(s *styleBlock, fontPath string) (*LayerStyle, error) {
	return layerStyleFromBlock(s, fontPath, "watermark")
}

// watermarkMargin resolves the requested distance from the canvas edge.
// MarginPX=0 explicit stays 0; unset is a compile error — the worker never
// guesses layout.
func watermarkMargin(marginPX *int) (int, error) {
	if marginPX == nil {
		return 0, fmt.Errorf("overlay: watermark margin_px is required (layout is owned by PipelineGen)")
	}
	if *marginPX < 0 {
		return 0, fmt.Errorf("overlay: watermark margin_px=%d is negative", *marginPX)
	}
	return *marginPX, nil
}

// resolveWatermarkGeometry converts the requested position name and the plan's
// typed geometry (width/height, margin) into BOTH concrete outputs a watermark
// layer needs: the [x, y] top-left box origin in canvas coordinates, and the
// layer size. The render-plan contract owns top-left coordinates; Chronon's
// lowering converts them to its canvas-centred scene basis exactly once.
// Unknown or missing inputs are a compile failure, never a silent "center"
// fallback.
//
// The size is returned from here — not recomputed by the caller — because the
// box dimensions are an input to the position math itself. When the compiler
// derived the size a second time, the layer's declared size and the box the
// position was computed against were two independent copies of one rule and
// could drift; this function is the single owner of both.
func resolveWatermarkGeometry(position string, canvasW, canvasH, margin int, s *styleBlock) (pos, size []float64, err error) {
	if strings.TrimSpace(position) == "" {
		return nil, nil, fmt.Errorf("overlay: watermark position is required (layout is owned by PipelineGen)")
	}
	// Box: the plan's explicit geometry when provided, otherwise derived
	// from canvas size (geometry implied by the request, not a visual style).
	boxW, boxH := float64(canvasW)/WatermarkBoxWidthDivisor, DefaultWatermarkBoxHeight
	if s != nil && s.WidthPX > 0 {
		boxW = float64(s.WidthPX)
	}
	if s != nil && s.HeightPX > 0 {
		boxH = float64(s.HeightPX)
	}
	m := float64(margin)
	// The returned anchor is the box's ABSOLUTE canvas top-left, not a Chronon
	// position: the engine reads the plan's position field with two different
	// semantics for text and non-text layers, so turning this anchor into that
	// field is canvasBoxPosition's job, at the layer-construction site.
	toTopLeft := func(x, y float64) []float64 { return []float64{x, y} }
	size = []float64{boxW, boxH}
	switch strings.ToLower(strings.TrimSpace(position)) {
	case "top_left":
		return toTopLeft(m, m), size, nil
	case "top_right":
		return toTopLeft(float64(canvasW)-boxW-m, m), size, nil
	case "center":
		return toTopLeft((float64(canvasW)-boxW)/2, (float64(canvasH)-boxH)/2), size, nil
	case "bottom_left":
		return toTopLeft(m, float64(canvasH)-boxH-m), size, nil
	case "bottom_right":
		return toTopLeft(float64(canvasW)-boxW-m, float64(canvasH)-boxH-m), size, nil
	default:
		return nil, nil, fmt.Errorf("overlay: unsupported watermark position %q (supported: top_left, top_right, center, bottom_left, bottom_right)", position)
	}
}

// subtitleCueGeometry resolves the placement of one burned-in ASS cue from
// the plan's typed style (position + width) and the canvas. Returns the box's
// ABSOLUTE canvas top-left anchor and its size; converting that anchor into the
// plan's position field belongs to canvasBoxPosition, because the engine reads
// that field differently for text and non-text layers. Unknown positions are a
// compile failure — the worker never silently relocates subtitles.
func subtitleCueGeometry(s *styleBlock, canvasW, canvasH, cueLayerCount int) (anchor, size []float64, err error) {
	if strings.TrimSpace(s.Position) == "" {
		return nil, nil, fmt.Errorf("overlay: subtitle style carries no position — PipelineGen must resolve subtitle placement")
	}
	// Box width: the requested width, or the canvas minus a symmetric safe
	// margin derived from the canvas itself (geometry, not style).
	boxW := float64(canvasW) - SubtitleSideMarginPX
	if s.WidthPX > 0 {
		boxW = float64(s.WidthPX)
	}
	const lineH = SubtitleLineHeightPX
	boxH := lineH
	if s.HeightPX > 0 {
		boxH = float64(s.HeightPX)
	} else if cueLayerCount > 1 {
		boxH = lineH * float64(cueLayerCount)
	}
	pos := strings.ToLower(strings.TrimSpace(s.Position))
	var anchorX, anchorY float64
	switch pos {
	case "bottom_center":
		anchorX = (float64(canvasW) - boxW) / 2
		anchorY = float64(canvasH)*SubtitleBottomCenterYFraction - boxH/2
	case "top_center":
		anchorX = (float64(canvasW) - boxW) / 2
		anchorY = float64(canvasH) * SubtitleTopCenterYFraction
	case "middle_center":
		anchorX = (float64(canvasW) - boxW) / 2
		anchorY = (float64(canvasH) - boxH) / 2
	default:
		return nil, nil, fmt.Errorf("overlay: unsupported subtitle position %q (supported: bottom_center, top_center, middle_center)", pos)
	}
	anchor = []float64{anchorX, anchorY}
	size = []float64{boxW, boxH}
	return anchor, size, nil
}

// canvasBoxPosition converts a box expressed in ABSOLUTE canvas coordinates
// (top-left anchor + size) into the render-plan `position` field for a layer of
// the given type.
//
// This function is the single owner of that conversion because the engine's
// decoder (render_plan_compiler_animation.cpp:apply_layer_primitives) reads the
// field with two different semantics:
//
//   - text  — the value IS the layer centre's absolute canvas coordinate; the
//     engine subtracts canvas/2 itself.
//   - other — the value is an offset from the canvas centre.
//
// Emitting one form for both types is what parked every burned cue in the
// top-left corner: an absolute top-left anchor was handed to the text branch,
// which subtracted canvas/2 a second time (960,540 on a 1920x1080 canvas), so a
// bottom_center cue's centre landed at the anchor minus half a canvas.
func canvasBoxPosition(layerType string, x, y, w, h float64, canvasW, canvasH int) []float64 {
	cx := x + w/2
	cy := y + h/2
	if layerType == "text" {
		return []float64{cx, cy}
	}
	return []float64{cx - float64(canvasW)/2, cy - float64(canvasH)/2}
}
