// semantic_compile.go owns the single semantic overlay-plan.v1 → concrete
// render-plan.v2 lowering. It is a mechanical compiler: every style,
// geometry and motion decision comes from the plan or from RenderingGen's
// official preset catalog, and anything untyped or unresolved is rejected
// fail-closed instead of silently defaulted.
package overlay

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

func compileSemantic(raw []byte) (*Plan, []Asset, error) {
	var src semanticPlan
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, nil, fmt.Errorf("overlay: decode semantic plan: %w", err)
	}
	if src.PlanID == "" || src.VideoID == "" || src.Width <= 0 || src.Height <= 0 || src.FPSNum <= 0 || src.FPSDen <= 0 {
		return nil, nil, fmt.Errorf("overlay: semantic plan requires plan_id, video_id and positive canvas/fps")
	}
	// A plan must have at least one renderable primitive: a source clip, a
	// background, or an overlay item. An empty plan with nothing to render is
	// always rejected fail-closed.
	if src.Source == nil && src.Background == nil && len(src.Items) == 0 {
		return nil, nil, fmt.Errorf("overlay: semantic plan has no renderable primitives (source, background or items required)")
	}
	plan := Plan{Schema: "chronon.render-plan.v2", Version: 2, JobID: src.PlanID,
		Canvas: Canvas{Width: src.Width, Height: src.Height, FPSNum: src.FPSNum, FPSDen: src.FPSDen},
		Output: Output{Path: "result.mp4", Format: "mp4", Codec: "h264", ProfileID: src.OutputProfileID}}

	// Seed canvas duration from the explicit duration_ms when provided. Items
	// can extend it but cannot shrink it. For clip renders with items:[] this
	// is the only source of duration; the compiler rejects zero duration at the
	// end of the function.
	if src.DurationMS > 0 {
		_, endFrame := msFrames(0, src.DurationMS, int64(src.FPSNum), int64(src.FPSDen))
		plan.Canvas.DurationFrames = endFrame
	}

	registry := newAssetRegistry()
	if src.Audio != nil {
		plan.Output.Audio = &Audio{
			Mode: src.Audio.Mode, Codec: src.Audio.Codec,
			SampleRate: src.Audio.SampleRate, Channels: src.Audio.Channels,
		}
	}
	if bg := src.Background; bg != nil {
		kind := strings.ToLower(strings.TrimSpace(bg.Kind))
		// blur_cover is a clip-render-specific fit hint; store it as "video"
		// type in the layer with the fit preserved.
		layerKind := kind
		if kind == "blur_cover" {
			layerKind = "video"
		}
		if layerKind != "color" && layerKind != "image" && layerKind != "video" {
			return nil, nil, fmt.Errorf("overlay: unsupported background kind %q", bg.Kind)
		}
		if layerKind == "color" {
			if len(bg.Color) != 4 {
				return nil, nil, fmt.Errorf("overlay: background color requires RGBA[4]")
			}
		} else if len(bg.AssetRefs) == 0 {
			return nil, nil, fmt.Errorf("overlay: %s background requires asset_refs", kind)
		}
		for _, ref := range bg.AssetRefs {
			if _, err := registry.Register(ref); err != nil {
				return nil, nil, fmt.Errorf("overlay: background asset: %w", err)
			}
		}
		layer := Layer{ID: "background", Type: layerKind, BoxWidth: src.Width, BoxHeight: src.Height,
			Size: []float64{float64(src.Width), float64(src.Height)},
			Fit:  bg.Fit, StartFrame: 0}
		if layerKind != "color" && layer.Fit == "" {
			layer.Fit = "cover"
		}
		if bg.Opacity != nil {
			layer.Opacity = *bg.Opacity
		}
		if layerKind == "color" {
			layer.Color = append([]float64(nil), bg.Color...)
		} else {
			ref := bg.AssetRefs[0]
			if layerKind == "video" {
				layer.Source = registry.Path(ref.ID)
			} else {
				layer.Asset = registry.Path(ref.ID)
			}
			layer.Loop = bg.Loop
		}
		plan.Layers = append(plan.Layers, layer)
	}
	// Pre-register every item asset so collisions fail before any layer is
	// emitted (the registry is the single owner of the id → path mapping).
	for _, item := range src.Items {
		for _, ref := range item.Assets {
			if _, err := registry.Register(ref); err != nil {
				return nil, nil, fmt.Errorf("overlay: item %q asset: %w", item.ID, err)
			}
		}
	}

	// Source clip — lowers to a full-canvas video layer. When foreground_scale
	// is set the source is scaled and centered on the canvas.
	var sourceLayerIndex = -1
	if src.Source != nil && src.Source.AssetID != "" {
		path := src.Source.Path
		if path == "" {
			registered, err := registry.Register(semanticAssetRef{ID: src.Source.AssetID, SHA256: src.Source.SHA256})
			if err != nil {
				return nil, nil, fmt.Errorf("overlay: source asset: %w", err)
			}
			path = registered
		}
		srcLayer := Layer{ID: "source", Type: "video", Source: path, StartFrame: 0}
		// Foreground scale: compute scaled geometry and center on canvas.
		// ForegroundScale == 0 or 100 means full-canvas (no scaling).
		if src.ForegroundScale > 0 && src.ForegroundScale < 100 {
			scaledW := int(math.Round(float64(src.Width) * float64(src.ForegroundScale) / 100))
			scaledH := int(math.Round(float64(src.Height) * float64(src.ForegroundScale) / 100))
			offsetX := float64(src.Width-scaledW) / 2
			offsetY := float64(src.Height-scaledH) / 2
			srcLayer.Size = []float64{float64(scaledW), float64(scaledH)}
			srcLayer.Position = []float64{offsetX, offsetY}
			srcLayer.Scale = []float64{float64(src.ForegroundScale) / 100, float64(src.ForegroundScale) / 100}
		}
		sourceLayerIndex = len(plan.Layers)
		plan.Layers = append(plan.Layers, srcLayer)
	}

	// Sidecar subtitles remain a published companion asset. Chronon's current
	// render-plan schema has no `subtitle` layer type, so do not emit one.
	if sub := src.Subtitles; sub != nil {
		if len(sub.AssetRefs) == 0 {
			return nil, nil, fmt.Errorf("overlay: subtitles require at least one asset_ref")
		}
		if _, err := registry.Register(sub.AssetRefs[0]); err != nil {
			return nil, nil, fmt.Errorf("overlay: subtitle asset: %w", err)
		}
		// Sidecar subtitles remain a published companion asset: the manifest
		// entry (above) is what gets published; Chronon has no subtitle layer.
	}

	// Watermark — lowers to a text or image layer at the requested position.
	// Geometry and style come ONLY from the plan's typed blocks: font size,
	// color, shadow (style), position + margin_px (layout). Unknown or missing
	// values are compile errors, never silent fallbacks.
	if wm := src.Watermark; wm != nil {
		font := ""
		if wm.FontRef != nil {
			registered, err := registry.Register(*wm.FontRef)
			if err != nil {
				return nil, nil, fmt.Errorf("overlay: watermark font asset: %w", err)
			}
			font = registered
		}
		if font == "" {
			return nil, nil, fmt.Errorf("overlay: text watermark requires font_ref")
		}
		wmStyle, err := parseStyleBlock(wm.Style)
		if err != nil {
			return nil, nil, err
		}
		style, err := watermarkLayerStyle(wmStyle, font)
		if err != nil {
			return nil, nil, err
		}
		margin, err := watermarkMargin(wm.MarginPX)
		if err != nil {
			return nil, nil, err
		}
		position, err := resolveWatermarkPosition(wm.Position, src.Width, src.Height, margin, wmStyle)
		if err != nil {
			return nil, nil, err
		}
		wmLayer := Layer{ID: "watermark", StartFrame: 0, DurationFrames: plan.Canvas.DurationFrames,
			Style: style, Position: position}
		if wm.Opacity != nil {
			wmLayer.Opacity = *wm.Opacity
		}
		if wm.Text != "" && len(wm.AssetRefs) == 0 {
			// Text-only watermark.
			wmLayer.Type = "text"
			wmLayer.Text = wm.Text
		} else if len(wm.AssetRefs) > 0 {
			registered, err := registry.Register(wm.AssetRefs[0])
			if err != nil {
				return nil, nil, fmt.Errorf("overlay: watermark asset: %w", err)
			}
			wmLayer.Type = "image"
			wmLayer.Asset = registered
			if wm.Text != "" {
				wmLayer.Text = wm.Text
			}
		} else {
			return nil, nil, fmt.Errorf("overlay: watermark requires text or asset_refs")
		}
		plan.Layers = append(plan.Layers, wmLayer)
	}

	// Item overlay layers.
	for _, item := range src.Items {
		start, end := msFrames(item.StartMS, item.EndMS, int64(src.FPSNum), int64(src.FPSDen))
		if item.ID == "" || item.Template == "" || item.StartMS < 0 || item.EndMS <= item.StartMS {
			return nil, nil, fmt.Errorf("overlay: invalid semantic item %q", item.ID)
		}
		preset, err := presetFor(item)
		if err != nil {
			return nil, nil, err
		}
		definition := OfficialPresetDefinition{}
		if preset != "" {
			family := string(PresetText)
			if isImageTemplate(item.Template) {
				family = string(PresetImage)
			}
			definition, err = resolveOfficialPreset(preset, family)
			if err != nil {
				return nil, nil, err
			}
		}
		params := item.Params
		if params == nil {
			params = map[string]any{}
		}
		for k, v := range item.MotionParams {
			params[k] = v
		}
		if isImageTemplate(item.Template) && len(item.Assets) == 0 {
			return nil, nil, fmt.Errorf("overlay: image template %q item %q requires asset_refs", item.Template, item.ID)
		}
		if isEntityTemplate(item.Template) && len(item.Assets) > 0 {
			// Entity cards are a two-layer composition. Give the image its own
			// stable layer ID; reusing the card ID would make Chronon treat the
			// later text layer as the same layer and silently hide the image.
			imageItem := item
			imageItem.ID = item.ID + "-image"
			img := imageLayer(imageItem, start, end, registry.Path(item.Assets[0].ID), params)
			if item.ImagePresetID != "" {
				imageDefinition, imageErr := resolveOfficialPreset(item.ImagePresetID, string(PresetImage))
				if imageErr != nil {
					return nil, nil, imageErr
				}
				applyPresetDefinition(&img, imageDefinition)
				imgAnimation, imgAnimErr := animationForDefinition(imageDefinition)
				if imgAnimErr != nil {
					return nil, nil, imgAnimErr
				}
				img.Animation = imgAnimation
				img.Position = resolveImageLayout(imageDefinition.Layout, img.BoxWidth, img.BoxHeight, src.Width, src.Height)
			}
			plan.Layers = append(plan.Layers, img)
		}
		text := item.Text
		if text == "" {
			text = entityRefText(item)
		}
		layer := Layer{ID: item.ID, Type: "text", Text: text, StartFrame: start, DurationFrames: end - start}
		if isImageTemplate(item.Template) {
			layer = imageLayer(item, start, end, registry.Path(item.Assets[0].ID), params)
		}
		if preset != "" {
			applyPresetDefinition(&layer, definition)
		}
		// Text placement is expressed as a layer top-left plus a local text
		// box.  Keep that contract explicit in v2: materialize_text uses the
		// serialized box size, while the layer position is applied exactly
		// once by Chronon.  The old v1 suite omitted this field, causing
		// Chronon to use a canvas-sized local frame and add the layout offset
		// a second time (centered text landed around x=1469 on a 1920 canvas).
		if layer.Type == "text" {
			if layer.BoxWidth <= 0 {
				layer.BoxWidth = src.Width
			}
			if layer.BoxHeight <= 0 {
				layer.BoxHeight = 120
			}
			layer.Size = []float64{float64(layer.BoxWidth), float64(layer.BoxHeight)}
		}
		if item.MotionID != "" {
			animation, err := animationForMotion(item.MotionID, item.MotionParams, item.Text, end-start)
			if err != nil {
				return nil, nil, err
			}
			if len(animation.Tracks) > 0 {
				layer.Animation = animation
			}
			layer.TextAnimators = animation.TextAnimators
		} else if preset != "" {
			if layer.Type == "text" {
				// Official text presets lower their motion through the shared
				// animationForPreset path so word/glyph selectors
				// (word_reveal, character_cascade, ...) are transported as
				// text animators instead of being silently compiled away to an
				// empty layer animation. Chronon requires animation objects to
				// carry tracks, so an animator-only motion keeps the animation
				// field absent and rides the layer's text_animators contract.
				presetAnimation, presetAnimErr := animationForPreset(definition, text, end-start)
				if presetAnimErr != nil {
					return nil, nil, presetAnimErr
				}
				if presetAnimation != nil {
					if len(presetAnimation.Tracks) == 0 {
						layer.TextAnimators = presetAnimation.TextAnimators
					} else {
						layer.Animation = presetAnimation
					}
				}
			} else {
				presetAnimation, presetAnimErr := animationForDefinition(definition)
				if presetAnimErr != nil {
					return nil, nil, presetAnimErr
				}
				layer.Animation = presetAnimation
			}
		}
		if layer.Style != nil && layer.Position == nil {
			posX, hasPosX := params["position_x"].(float64)
			posY, hasPosY := params["position_y"].(float64)
			if hasPosX && hasPosY {
				layer.Position = []float64{posX, posY}
			} else {
				layer.Position = resolveTextLayout(definition.Layout, layer.BoxWidth, layer.BoxHeight, src.Width, src.Height)
				if hasPosX {
					layer.Position[0] = posX
				}
				if hasPosY {
					layer.Position[1] = posY
				}
			}
		}
		if layer.Type == "image" && layer.Position == nil && preset != "" {
			layer.Position = resolveImageLayout(definition.Layout, layer.BoxWidth, layer.BoxHeight, src.Width, src.Height)
		}
		plan.Layers = append(plan.Layers, layer)
		if end > plan.Canvas.DurationFrames {
			plan.Canvas.DurationFrames = end
		}
	}

	// Patch background duration to match the final canvas duration.
	if len(plan.Layers) > 0 && plan.Layers[0].ID == "background" {
		plan.Layers[0].DurationFrames = plan.Canvas.DurationFrames
	}
	// Patch source layer duration — it spans the full clip.
	if sourceLayerIndex >= 0 {
		plan.Layers[sourceLayerIndex].DurationFrames = plan.Canvas.DurationFrames
	}
	// Patch subtitle and watermark layers — they span the full clip.
	for i := range plan.Layers {
		if plan.Layers[i].DurationFrames == 0 {
			switch plan.Layers[i].ID {
			case "subtitles", "watermark":
				plan.Layers[i].DurationFrames = plan.Canvas.DurationFrames
			}
		}
	}

	if plan.Canvas.DurationFrames <= 0 {
		return nil, nil, fmt.Errorf("overlay: semantic plan duration is zero — provide duration_ms or at least one item with end_ms > 0")
	}
	// Stable asset order (the registry sorts) keeps prepared-plan
	// fingerprints reproducible. The plan stays typed; the caller marshals it
	// exactly once at the Chronon boundary.
	return &plan, registry.Assets(), nil
}

// resolveWatermarkPosition lives in visual_style_resolver.go — the single
// owner of watermark/subtitle geometry resolution.
