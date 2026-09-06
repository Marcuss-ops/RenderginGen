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

func CompileFastEntityOverlays(
	jobID string,
	width, height int,
	fpsNum, fpsDen int,
	totalDurationFrames int64,
	bgVideoPath string,
	overlays []FastEntityOverlay,
) (*Plan, error) {
	var err error
	overlays, err = NormalizeEntityOverlays(overlays)
	if err != nil {
		return nil, err
	}
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}
	if fpsNum <= 0 {
		fpsNum = DefaultFPSNum
	}
	if fpsDen <= 0 {
		fpsDen = DefaultFPSDen
	}
	if totalDurationFrames <= 0 {
		return nil, fmt.Errorf("entity_contract: total duration must be positive")
	}

	plan := newPlan(jobID, width, height, fpsNum, fpsDen, totalDurationFrames)

	// A color: background is rendered as a real Chronon color layer. This is
	// used by the preset canaries so a compositor backend cannot silently turn
	// a branded background video into black pixels.
	if bgVideoPath != "" {
		if strings.HasPrefix(strings.ToLower(bgVideoPath), "color:") {
			plan.Layers = append(plan.Layers, Layer{
				ID: "bg_color", Type: "color", Color: parseBackgroundColor(bgVideoPath),
				BoxWidth: width, BoxHeight: height, Size: []float64{float64(width), float64(height)},
				StartFrame: 0, DurationFrames: totalDurationFrames, Opacity: 1.0,
			})
		} else {
			plan.Layers = append(plan.Layers, Layer{
				ID:             "bg_video",
				Type:           "video",
				Source:         bgVideoPath,
				BoxWidth:       width,
				BoxHeight:      height,
				Fit:            "cover",
				StartFrame:     0,
				DurationFrames: totalDurationFrames,
				Opacity:        1.0,
			})
		}
	}

	// Add entity overlays
	for i, ov := range overlays {
		layerID := fmt.Sprintf("entity_%d_%s", i+1, strings.ToLower(ov.Type))
		duration := ov.EndFrame - ov.StartFrame
		if duration <= 0 {
			duration = 1
		}

		opacity := ov.Opacity
		if ov.OpacityExplicit != nil {
			opacity = *ov.OpacityExplicit
		}
		if opacity < 0.0 || opacity > 1.0 {
			return nil, fmt.Errorf("entity_contract: overlay %d opacity %.3f outside [0,1]", i, opacity)
		}
		if opacity == 0.0 && ov.OpacityExplicit == nil {
			opacity = 1.0
		}

		animationName := strings.ToLower(strings.TrimSpace(ov.Animation))
		if animationName == "" || animationName == "none" {
			animationName = "static"
		}
		var layerAnim *LayerAnimation
		var presetTextAnimators []TextAnimator
		if animationName != "static" {
			anim, animErr := fastEntityAnimation(animationName, duration)
			if animErr != nil {
				return nil, animErr
			}
			layerAnim = anim
		}
		var preset OfficialPresetDefinition
		if strings.TrimSpace(ov.PresetID) != "" {
			family := strings.ToLower(strings.TrimSpace(ov.Type))
			preset, err = resolveOfficialPreset(ov.PresetID, family)
			if err != nil {
				return nil, err
			}
			layerAnim, err = animationForPreset(preset, ov.Text, duration)
			if err != nil {
				return nil, err
			}
			if layerAnim != nil && len(layerAnim.Tracks) == 0 {
				// Chronon's schema requires a non-empty `tracks` array when the
				// layer animation object is present. Text-only motion is carried
				// by the layer-level text_animators field instead.
				presetTextAnimators = layerAnim.TextAnimators
				layerAnim = nil
			}
		}

		switch strings.ToLower(strings.TrimSpace(ov.Type)) {
		case "image":
			if ov.Asset == "" {
				return nil, fmt.Errorf("entity_contract: overlay %d (image) requires asset path", i)
			}
			boxWidth, boxHeight := 260, 260
			if preset.Family == PresetImage {
				if preset.Layout.BoxWidth > 0 {
					boxWidth = preset.Layout.BoxWidth
				}
				if preset.Layout.BoxHeight > 0 {
					boxHeight = preset.Layout.BoxHeight
				}
			}
			if ov.Size > 0 {
				boxWidth = int(ov.Size)
				boxHeight = int(ov.Size)
				if strings.EqualFold(ov.Position, "center") && ov.Size == 200 {
					boxHeight = 100
				}
			}
			pos := ov.Position
			if preset.Family == PresetImage {
				resolved := resolveImageLayout(preset.Layout, boxWidth, boxHeight, width, height)
				posX, posY := resolved[0], resolved[1]
				// A caller-supplied anchor is authoritative. The catalog supplies
				// the default only when the job leaves placement unspecified. Image
				// positions are offsets from the canvas center in Chronon's plan.
				if strings.TrimSpace(pos) != "" {
					switch strings.ToLower(strings.TrimSpace(pos)) {
					case "center":
						posX, posY = 0, 0
					case "image_left", "left":
						posX = -float64(width-boxWidth) / 2.0
						posY = 0
					case "image_right", "right":
						posX = float64(width-boxWidth) / 2.0
						posY = 0
					case "bottom_left":
						posX = -float64(width-boxWidth) / 2.0
						posY = float64(height-boxHeight) / 2.0
					case "bottom_center":
						posX = 0
						posY = float64(height-boxHeight) / 2.0
					case "bottom_right":
						posX = float64(width-boxWidth) / 2.0
						posY = float64(height-boxHeight) / 2.0
					}
				}
				if len(ov.Translate) == 2 {
					posX += ov.Translate[0]
					posY += ov.Translate[1]
				}
				imgLayer := Layer{ID: layerID, Type: "image", Asset: ov.Asset,
					BoxWidth: boxWidth, BoxHeight: boxHeight,
					Size: []float64{float64(boxWidth), float64(boxHeight)}, Fit: preset.Layout.Fit,
					Position: []float64{posX, posY}, StartFrame: ov.StartFrame,
					DurationFrames: duration, Opacity: opacity, Animation: layerAnim}
				if imgLayer.Fit == "" {
					imgLayer.Fit = "contain"
				}
				plan.Layers = append(plan.Layers, imgLayer)
				continue
			}
			if pos == "" {
				pos = "center"
			}

			// Entity overlays expose only generic absolute geometry. Editorial
			// anchor resolution belongs to the common RenderingGen compiler.
			posX := float64(width-boxWidth) / 2.0
			posY := float64(height-boxHeight) / 2.0
			switch strings.ToLower(pos) {
			case "image_left", "left":
				posX = 0
			case "image_right", "right":
				posX = float64(width - boxWidth)
			}
			if len(ov.Translate) == 2 {
				posX += ov.Translate[0]
				posY += ov.Translate[1]
			}

			// Positions are top-left coordinates in the generic contract.

			imgLayer := Layer{
				ID:             layerID,
				Type:           "image",
				Asset:          ov.Asset,
				BoxWidth:       boxWidth,
				BoxHeight:      boxHeight,
				Size:           []float64{float64(boxWidth), float64(boxHeight)},
				Fit:            "contain",
				Position:       []float64{posX, posY},
				StartFrame:     ov.StartFrame,
				DurationFrames: duration,
				Opacity:        opacity,
				Animation:      layerAnim,
			}
			plan.Layers = append(plan.Layers, imgLayer)

		case "text":
			if ov.Text == "" {
				return nil, fmt.Errorf("entity_contract: overlay %d (text) requires text content", i)
			}
			fontSize := 58.0
			if ov.Size > 0 {
				fontSize = ov.Size
			}
			font := ov.Font
			if font == "" {
				return nil, fmt.Errorf("entity_contract: overlay %d (text) requires font asset", i)
			}
			color := ov.Color
			if len(color) != 4 {
				color = []float64{1.0, 1.0, 1.0, 1.0}
			}

			txtLayer := Layer{
				ID:             layerID,
				Type:           "text",
				Text:           ov.Text,
				Size:           []float64{float64(width), 120},
				Style:          &LayerStyle{Font: font, FontSize: fontSize, Fill: rgbaHex(color)},
				StartFrame:     ov.StartFrame,
				DurationFrames: duration,
				Opacity:        opacity,
				Animation:      layerAnim,
			}
			if preset.Family == PresetText {
				applyPresetDefinition(&txtLayer, preset)
				txtLayer.Size = []float64{float64(width), 120}
				txtLayer.Position = resolveTextLayout(preset.Layout, width, 120, width, height)
				txtLayer.TextAnimators = presetTextAnimators
			}
			if len(ov.Translate) == 2 {
				txtLayer.Position = []float64{ov.Translate[0], ov.Translate[1]}
			}
			plan.Layers = append(plan.Layers, txtLayer)

		default:
			return nil, fmt.Errorf("entity_contract: unsupported overlay type %q", ov.Type)
		}
	}

	return plan, nil
}
