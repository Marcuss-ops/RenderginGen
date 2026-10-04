package overlay

import "fmt"

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

// Both spellings the two public style contracts emit are accepted by
// parseStyleBlock, which reads the plan's style map directly: PipelineGen
// normally emits offset_x/offset_y, while older semantic fixtures and clients
// may still send offset:[x,y]. The conversion lives there and nowhere else — an
// UnmarshalJSON accepting the same alias sat here, but nothing decodes a style
// block through encoding/json, so it was dead code that read as the authority
// on the alias rule.

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
