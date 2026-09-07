package overlay

import (
	"fmt"
	"strconv"
	"strings"
)

// FastEntityOverlay defines the high-speed overlay contract for entities and phrases.
// Supported types: "text" | "image"
// Supported animations: "fade" | "slide" | "scale" | "static"
type FastEntityOverlay struct {
	Type       string  `json:"type"`                // "text" | "image"
	PresetID   string  `json:"preset_id,omitempty"` // official catalog preset; used by certification/fast adapters
	StartFrame int64   `json:"start_frame"`         // inclusive start frame
	EndFrame   int64   `json:"end_frame"`           // exclusive end frame
	Position   string  `json:"position,omitempty"`  // anchor / preset position ("lower_third", "center", "safe_area", "image_left", "image_right")
	Size       float64 `json:"size,omitempty"`      // font size for text, or box size for image
	Opacity    float64 `json:"opacity,omitempty"`   // alpha [0.0, 1.0] (default 1.0)
	// OpacityExplicit distinguishes an intentional zero from the legacy
	// zero-value meaning "use the default". It is not serialized into plans.
	OpacityExplicit *float64  `json:"-"`
	Animation       string    `json:"animation,omitempty"` // "fade" | "slide" | "scale" | "static"
	Asset           string    `json:"asset,omitempty"`     // image file path
	Text            string    `json:"text,omitempty"`      // phrase string
	Font            string    `json:"font,omitempty"`      // font path / font family
	Color           []float64 `json:"color,omitempty"`     // RGBA [r, g, b, a] in [0, 1]
	Translate       []float64 `json:"translate,omitempty"` // [dx, dy] offset
	Scale           float64   `json:"scale,omitempty"`     // scale multiplier (default 1.0)
}

// NormalizeEntityOverlays canonicalizes the legacy fast-path input before it
// enters the shared renderer contract. In particular, animation aliases are
// reduced to the names understood by the RenderingGen motion registry.
func NormalizeEntityOverlays(overlays []FastEntityOverlay) ([]FastEntityOverlay, error) {
	out := make([]FastEntityOverlay, len(overlays))
	copy(out, overlays)
	for i := range out {
		out[i].Type = strings.ToLower(strings.TrimSpace(out[i].Type))
		if out[i].Type != "text" && out[i].Type != "image" {
			return nil, fmt.Errorf("entity_contract: unsupported overlay type %q", out[i].Type)
		}
		out[i].Animation = strings.ToLower(strings.TrimSpace(out[i].Animation))
		if out[i].Animation == "none" || out[i].Animation == "" {
			out[i].Animation = "static"
		}
		if out[i].EndFrame <= out[i].StartFrame {
			return nil, fmt.Errorf("entity_contract: overlay %d has non-positive duration", i)
		}
	}
	return out, nil
}

// Default video contract parameters matching refactored pipeline standards.
const (
	DefaultFPSNum = 24
	DefaultFPSDen = 1
	DefaultWidth  = 1920
	DefaultHeight = 1080
)

// CompileFastEntityOverlays is the transitional adapter for FastEntityOverlay.
// It is kept outside the semantic compiler while existing fast-path producers
// migrate to the common semantic input contract.
// and a sequence of fast entity overlays. It emits only generic layer data and
// tracks; animation names are retained solely as debug metadata.
func fastEntityAnimation(name string, duration int64) (*LayerAnimation, error) {
	enter := int(duration)
	if enter < 1 {
		enter = 1
	}
	if name == "slide" || name == "slide_left" || name == "slide_up" {
		name = "reveal_from_bottom"
	}
	if name == "scale" || name == "scale_in" || name == "pop" {
		name = "scale_drop"
	}
	tracks, err := resolveMotion(MotionDefinition{Name: name, Unit: "layer", Enter: enter})
	if err != nil {
		return nil, err
	}
	return &LayerAnimation{Tracks: tracks}, nil
}

func parseBackgroundColor(spec string) []float64 {
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(spec, "color:"), "COLOR:"))
	raw = strings.TrimPrefix(raw, "#")
	if len(raw) == 6 {
		if value, err := strconv.ParseUint(raw, 16, 32); err == nil {
			return []float64{float64((value>>16)&0xff) / 255, float64((value>>8)&0xff) / 255, float64(value&0xff) / 255, 1}
		}
	}
	// Pale Olive Classic, also the safe default for malformed color specs.
	return []float64{238.0 / 255, 241.0 / 255, 231.0 / 255, 1}
}
