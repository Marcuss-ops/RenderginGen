package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
)

// visual_style_resolver.go owns the single resolution path from the semantic
// plan's typed style blocks (subtitles/watermark) to concrete Chronon layer
// geometry. It replaces the historical hardcoded defaults (FontSize 52/42,
// white fill, shadow 0.95/8, nominal 200×80/360×80 boxes, 0.80 vertical
// placement) — RenderingGen is an execution worker and must NEVER invent
// visual style. Every geometry value it emits is either:
//   - resolved from the plan's typed style block, or
//   - derived from the canvas (geometry that is mathematically implied), or
//   - rejected fail-closed.
//
// One owner, one path: both the watermark compiler and BurnASSIntoPlan call
// into this resolver. No other file may hardcode a font size, color, shadow
// or box dimension for these layers.

// styleBlock is the wire projection of the canonical kernel/script
// VideoVisualStyleSpec (PipelineGen side). It mirrors that struct
// field-for-field so the typed owner in PipelineGen's kernel remains the
// single source of truth; the resolver only interprets it.
type styleBlock struct {
	Font         string           `json:"font,omitempty"`
	Position     string           `json:"position,omitempty"`
	Size         float64          `json:"size,omitempty"`
	Color        string           `json:"color,omitempty"`
	FontSizePX   float64          `json:"font_size_px,omitempty"`
	WidthPX      int              `json:"width_px,omitempty"`
	HeightPX     int              `json:"height_px,omitempty"`
	ScalePercent float64          `json:"scale_percent,omitempty"`
	Shadow       *shadowBlock     `json:"shadow,omitempty"`
	TransitionIn *transitionBlock `json:"transition_in,omitempty"`
}

type shadowBlock struct {
	Color   string  `json:"color,omitempty"`
	Opacity float64 `json:"opacity,omitempty"`
	BlurPX  float64 `json:"blur_px,omitempty"`
	OffsetX float64 `json:"offset_x,omitempty"`
	OffsetY float64 `json:"offset_y,omitempty"`
}

type transitionBlock struct {
	Preset     string `json:"preset,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// parseStyleBlock decodes the semantic plan's free-form style map into the
// typed block. RenderingGen validates mechanically; it never re-derives a
// caller decision, so an unparsable style is a compile error, not a default.
func parseStyleBlock(raw map[string]any) (*styleBlock, error) {
	if raw == nil {
		return nil, nil
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("overlay: encode style block: %w", err)
	}
	var out styleBlock
	if err := json.Unmarshal(blob, &out); err != nil {
		return nil, fmt.Errorf("overlay: decode style block: %w", err)
	}
	return &out, nil
}

// numField reads a numeric style field from a decoded style map. JSON
// numbers decode as float64; any other shape reports not-ok so callers can
// distinguish "declared" from "missing" instead of treating 0 as a value.
func numField(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key].(float64)
	if !ok {
		return 0, false
	}
	return v, true
}

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

// subtitleLayerStyle converts the plan's subtitle style into a concrete
// Chronon text style. Fails closed when the plan carries no usable style.
func subtitleLayerStyle(s *styleBlock, fontPath string) (*concreteStyle, error) {
	size, err := effectiveFontSize(s)
	if err != nil {
		return nil, fmt.Errorf("overlay: subtitle %w", err)
	}
	fill := strings.TrimSpace(s.Color)
	if fill == "" {
		return nil, fmt.Errorf("overlay: subtitle style carries no color — PipelineGen must resolve the fill (the worker never invents one)")
	}
	style := &concreteStyle{Font: fontPath, FontSize: size, Fill: fill}
	if s.Shadow != nil {
		style.Shadow = &concreteShadow{
			Color:   s.Shadow.Color,
			Opacity: s.Shadow.Opacity,
			Blur:    s.Shadow.BlurPX,
			Offset:  []float64{s.Shadow.OffsetX, s.Shadow.OffsetY},
		}
	}
	return style, nil
}

// watermarkLayerStyle converts the plan's watermark style into a concrete
// Chronon text style. Fails closed when the plan carries no usable style.
func watermarkLayerStyle(s *styleBlock, fontPath string) (*concreteStyle, error) {
	size, err := effectiveFontSize(s)
	if err != nil {
		return nil, fmt.Errorf("overlay: watermark %w", err)
	}
	fill := strings.TrimSpace(s.Color)
	if fill == "" {
		return nil, fmt.Errorf("overlay: watermark style carries no color — PipelineGen must resolve the fill (the worker never invents one)")
	}
	style := &concreteStyle{Font: fontPath, FontSize: size, Fill: fill}
	if s.Shadow != nil {
		style.Shadow = &concreteShadow{
			Color:   s.Shadow.Color,
			Opacity: s.Shadow.Opacity,
			Blur:    s.Shadow.BlurPX,
			Offset:  []float64{s.Shadow.OffsetX, s.Shadow.OffsetY},
		}
	}
	return style, nil
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

// resolveWatermarkPosition converts the requested position name and the
// plan's typed geometry (width/height, margin) into a concrete [x, y] centre
// offset relative to the canvas centre. Unknown positions are a compile
// failure, never a silent "center" fallback.
func resolveWatermarkPosition(position string, canvasW, canvasH, margin int, s *styleBlock) ([]float64, error) {
	if strings.TrimSpace(position) == "" {
		return nil, fmt.Errorf("overlay: watermark position is required (layout is owned by PipelineGen)")
	}
	// Box: the plan's explicit geometry when provided, otherwise derived
	// from canvas size (geometry implied by the request, not a visual style).
	boxW, boxH := float64(canvasW)/6, 80.0
	if s != nil && s.WidthPX > 0 {
		boxW = float64(s.WidthPX)
	}
	if s != nil && s.HeightPX > 0 {
		boxH = float64(s.HeightPX)
	}
	m := float64(margin)
	toCenterOffset := func(x, y float64) []float64 {
		return []float64{x + boxW/2 - float64(canvasW)/2, y + boxH/2 - float64(canvasH)/2}
	}
	switch strings.ToLower(strings.TrimSpace(position)) {
	case "top_left":
		return toCenterOffset(m, m), nil
	case "top_right":
		return toCenterOffset(float64(canvasW)-boxW-m, m), nil
	case "center":
		return toCenterOffset((float64(canvasW)-boxW)/2, (float64(canvasH)-boxH)/2), nil
	case "bottom_left":
		return toCenterOffset(m, float64(canvasH)-boxH-m), nil
	case "bottom_right":
		return toCenterOffset(float64(canvasW)-boxW-m, float64(canvasH)-boxH-m), nil
	default:
		return nil, fmt.Errorf("overlay: unsupported watermark position %q (supported: top_left, top_right, center, bottom_left, bottom_right)", position)
	}
}

// subtitleCueGeometry resolves the placement of one burned-in ASS cue from
// the plan's typed style (position + width) and the canvas. Returns the
// Chronon centre-offset position and the box size. Unknown positions are a
// compile failure — the worker never silently relocates subtitles.
func subtitleCueGeometry(s *styleBlock, canvasW, canvasH, cueLayerCount int) (position, size []float64, err error) {
	if strings.TrimSpace(s.Position) == "" {
		return nil, nil, fmt.Errorf("overlay: subtitle style carries no position — PipelineGen must resolve subtitle placement")
	}
	// Box width: the requested width, or the canvas minus a symmetric safe
	// margin derived from the canvas itself (geometry, not style).
	boxW := float64(canvasW) - 120
	if s.WidthPX > 0 {
		boxW = float64(s.WidthPX)
	}
	const lineH = 70.0
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
		anchorY = float64(canvasH)*0.80 - boxH/2
	case "top_center":
		anchorX = (float64(canvasW) - boxW) / 2
		anchorY = float64(canvasH) * 0.10
	case "middle_center":
		anchorX = (float64(canvasW) - boxW) / 2
		anchorY = (float64(canvasH) - boxH) / 2
	default:
		return nil, nil, fmt.Errorf("overlay: unsupported subtitle position %q (supported: bottom_center, top_center, middle_center)", pos)
	}
	// Chronon layer positions are offsets from the canvas centre and address
	// the layer centre: convert the absolute top-left anchor once, here.
	position = []float64{anchorX + boxW/2 - float64(canvasW)/2, anchorY}
	size = []float64{boxW, boxH}
	return position, size, nil
}
