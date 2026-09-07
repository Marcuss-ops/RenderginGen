package overlay

import (
	"fmt"
	"strings"
)

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
					Radius:   imagePresetRadius(preset.ID, boxWidth, boxHeight),
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
				// Chronon centers a canvas-sized text frame when position is
				// omitted. Keep center-anchored phrase presets on that canonical
				// path; explicit positions are reserved for lower-third/safe-area
				// layouts whose anchor is intentionally not centered.
				if preset.Layout.Anchor != "center" {
					txtLayer.Position = resolveTextLayout(preset.Layout, width, 120, width, height)
				}
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

func imagePresetRadius(presetID string, width, height int) float64 {
	if presetID != "modern_rounded_pop" {
		return 0
	}
	if width > height {
		width = height
	}
	return float64(width) * 0.16
}
