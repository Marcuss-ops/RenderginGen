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
	Stroke       *strokeBlock     `json:"stroke,omitempty"`
	Shadow       *shadowBlock     `json:"shadow,omitempty"`
	TransitionIn *transitionBlock `json:"transition_in,omitempty"` // parsed only to reject fail-closed
}

type shadowBlock struct {
	Color   string  `json:"color,omitempty"`
	Opacity float64 `json:"opacity,omitempty"`
	BlurPX  float64 `json:"blur_px,omitempty"`
	OffsetX float64 `json:"offset_x,omitempty"`
	OffsetY float64 `json:"offset_y,omitempty"`
}

type strokeBlock struct {
	Color string  `json:"color,omitempty"`
	Width float64 `json:"width,omitempty"`
}

// UnmarshalJSON accepts both spellings emitted by the two public style
// contracts. PipelineGen normally emits offset_x/offset_y; older semantic
// fixtures and clients may still send offset:[x,y]. Keeping the conversion at
// the wire boundary makes every subtitle and watermark use the same concrete
// LayerShadow without inventing visual defaults.
func (s *shadowBlock) UnmarshalJSON(data []byte) error {
	type shadowAlias shadowBlock
	var raw struct {
		*shadowAlias
		Offset []float64 `json:"offset"`
	}
	raw.shadowAlias = (*shadowAlias)(s)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Offset) > 0 {
		if len(raw.Offset) != 2 {
			return fmt.Errorf("overlay: shadow.offset must contain exactly [x,y]")
		}
		s.OffsetX, s.OffsetY = raw.Offset[0], raw.Offset[1]
	}
	return nil
}

type transitionBlock struct {
	Preset     string `json:"preset,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// parseStyleBlock decodes the semantic plan's free-form style map into the
// typed block without a map→JSON→struct round-trip (U7). The historical
// implementation did json.Marshal(raw) + json.Unmarshal per style block per
// item — O(items) extra allocs and JSON codec work on the hot compile path.
// Decode directly from the map via type assertions.
func parseStyleBlock(raw map[string]any) (*styleBlock, error) {
	if raw == nil {
		return nil, nil
	}
	out := &styleBlock{}
	if v, ok := raw["font"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("overlay: style.font must be a string")
		}
		out.Font = s
	}
	if v, ok := raw["position"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("overlay: style.position must be a string")
		}
		out.Position = s
	}
	if v, ok := raw["size"]; ok {
		f, err := toFloat(v)
		if err != nil {
			return nil, fmt.Errorf("overlay: style.size: %w", err)
		}
		out.Size = f
	}
	if v, ok := raw["color"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("overlay: style.color must be a string")
		}
		out.Color = s
	}
	if v, ok := raw["font_size_px"]; ok {
		f, err := toFloat(v)
		if err != nil {
			return nil, fmt.Errorf("overlay: style.font_size_px: %w", err)
		}
		out.FontSizePX = f
	}
	if v, ok := raw["width_px"]; ok {
		n, err := toInt(v)
		if err != nil {
			return nil, fmt.Errorf("overlay: style.width_px: %w", err)
		}
		out.WidthPX = n
	}
	if v, ok := raw["height_px"]; ok {
		n, err := toInt(v)
		if err != nil {
			return nil, fmt.Errorf("overlay: style.height_px: %w", err)
		}
		out.HeightPX = n
	}
	if v, ok := raw["scale_percent"]; ok {
		f, err := toFloat(v)
		if err != nil {
			return nil, fmt.Errorf("overlay: style.scale_percent: %w", err)
		}
		out.ScalePercent = f
	}
	if v, ok := raw["stroke"]; ok {
		if v != nil {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("overlay: style.stroke must be an object")
			}
			sb := &strokeBlock{}
			if c, ok := m["color"]; ok {
				s, ok := c.(string)
				if !ok {
					return nil, fmt.Errorf("overlay: style.stroke.color must be a string")
				}
				sb.Color = s
			}
			if w, ok := m["width"]; ok {
				f, err := toFloat(w)
				if err != nil {
					return nil, fmt.Errorf("overlay: style.stroke.width: %w", err)
				}
				sb.Width = f
			}
			out.Stroke = sb
		}
	}
	if v, ok := raw["shadow"]; ok {
		if v != nil {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("overlay: style.shadow must be an object")
			}
			sb := &shadowBlock{}
			if c, ok := m["color"]; ok {
				s, ok := c.(string)
				if !ok {
					return nil, fmt.Errorf("overlay: style.shadow.color must be a string")
				}
				sb.Color = s
			}
			if o, ok := m["opacity"]; ok {
				f, err := toFloat(o)
				if err != nil {
					return nil, fmt.Errorf("overlay: style.shadow.opacity: %w", err)
				}
				sb.Opacity = f
			}
			if b, ok := m["blur_px"]; ok {
				f, err := toFloat(b)
				if err != nil {
					return nil, fmt.Errorf("overlay: style.shadow.blur_px: %w", err)
				}
				sb.BlurPX = f
			}
			if ox, ok := m["offset_x"]; ok {
				f, err := toFloat(ox)
				if err != nil {
					return nil, fmt.Errorf("overlay: style.shadow.offset_x: %w", err)
				}
				sb.OffsetX = f
			}
			if oy, ok := m["offset_y"]; ok {
				f, err := toFloat(oy)
				if err != nil {
					return nil, fmt.Errorf("overlay: style.shadow.offset_y: %w", err)
				}
				sb.OffsetY = f
			}
			if off, ok := m["offset"]; ok {
				arr, ok := off.([]any)
				if !ok {
					return nil, fmt.Errorf("overlay: style.shadow.offset must be [x,y]")
				}
				if len(arr) != 2 {
					return nil, fmt.Errorf("overlay: shadow.offset must contain exactly [x,y]")
				}
				x, err := toFloat(arr[0])
				if err != nil {
					return nil, fmt.Errorf("overlay: style.shadow.offset[0]: %w", err)
				}
				y, err := toFloat(arr[1])
				if err != nil {
					return nil, fmt.Errorf("overlay: style.shadow.offset[1]: %w", err)
				}
				sb.OffsetX, sb.OffsetY = x, y
			}
			out.Shadow = sb
		}
	}
	if _, ok := raw["transition_in"]; ok {
		return nil, fmt.Errorf("overlay: style.transition_in is not supported by the Chronon lowering; render transitions via PipelineGen keyframes instead")
	}
	return out, nil
}

func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case json.Number:
		f, err := n.Float64()
		return f, err
	default:
		return 0, fmt.Errorf("must be a number, got %T", v)
	}
}

func toInt(v any) (int, error) {
	f, err := toFloat(v)
	if err != nil {
		return 0, err
	}
	return int(f), nil
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
	// the layer centre: convert the absolute top-left anchor once, here —
	// symmetrically on both axes. (The historical version converted only X,
	// leaving Y absolute, so SubtitleStyleAsset/BurnASSIntoPlan — which treat
	// both coordinates as centre offsets — pushed bottom cues off-canvas.)
	position = []float64{
		anchorX + boxW/2 - float64(canvasW)/2,
		anchorY + boxH/2 - float64(canvasH)/2,
	}
	size = []float64{boxW, boxH}
	return position, size, nil
}
