// semantic_compile.go owns the single semantic overlay-plan.v1 → concrete
// render-plan.v2 lowering. It is a mechanical compiler: every style,
// geometry and motion decision comes from the plan or from RenderingGen's
// official preset catalog, and anything untyped or unresolved is rejected
// fail-closed instead of silently defaulted.
//
// The item pipeline is a single resolve → compile pass:
//
//	semanticItem ──resolve (registry.go)──▶ resolvedItem ──compile per kind─▶ []Layer
//	                                                └──▶ Stats
//
// The kind is the semantic SSOT: PipelineGen decides WHAT the item is, the
// template registry validates it, and each kind has exactly one compiler below.
package overlay

import (
	"encoding/json"
	"fmt"
	"strings"
)

// resolvedItem is RenderingGen's canonical internal representation of one
// overlay item. It is produced once by resolveSemanticItems and consumed by
// the per-kind compilers and the ledger — the raw JSON is never re-read.
type resolvedItem struct {
	Item   semanticItem
	Spec   TemplateSpec
	Kind   ItemKind
	Params map[string]any
	Start  int64
	End    int64
	// PresetID is the validated official preset for the (text/image) layer.
	PresetID string
	// Preset and ImagePreset are the resolved official definitions.
	Preset      PresetDefinition
	ImagePreset PresetDefinition
}

func compileSemantic(raw []byte) (*Plan, []Asset, Stats, error) {
	var src semanticPlan
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, nil, Stats{}, fmt.Errorf("overlay: decode semantic plan: %w", err)
	}
	if src.PlanID == "" || src.VideoID == "" || src.Width <= 0 || src.Height <= 0 || src.FPSNum <= 0 || src.FPSDen <= 0 {
		return nil, nil, Stats{}, fmt.Errorf("overlay: semantic plan requires plan_id, video_id and positive canvas/fps")
	}
	// A plan must have at least one renderable primitive: a source clip, a
	// background, or an overlay item. An empty plan with nothing to render is
	// always rejected fail-closed.
	if src.Source == nil && src.Background == nil && len(src.Items) == 0 {
		return nil, nil, Stats{}, fmt.Errorf("overlay: semantic plan has no renderable primitives (source, background or items required)")
	}
	// The Plan constructor is the single owner of the schema/version and the
	// output defaults, so the compiler cannot drift from it.
	plan := *newPlan(src.PlanID, src.Width, src.Height, src.FPSNum, src.FPSDen, 0)
	plan.Output.ProfileID = src.OutputProfileID

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
			return nil, nil, Stats{}, fmt.Errorf("overlay: unsupported background kind %q", bg.Kind)
		}
		if layerKind == "color" {
			if len(bg.Color) != 4 {
				return nil, nil, Stats{}, fmt.Errorf("overlay: background color requires RGBA[4]")
			}
		} else if len(bg.AssetRefs) == 0 {
			return nil, nil, Stats{}, fmt.Errorf("overlay: %s background requires asset_refs", kind)
		}
		for _, ref := range bg.AssetRefs {
			if _, err := registry.Register(ref); err != nil {
				return nil, nil, Stats{}, fmt.Errorf("overlay: background asset: %w", err)
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
				return nil, nil, Stats{}, fmt.Errorf("overlay: item %q asset: %w", item.ID, err)
			}
		}
	}

	// Resolve every item ONCE through the template registry: kind validation,
	// preset resolution and timing. Both the per-kind compilers and the ledger
	// read this resolution, so they can never disagree.
	resolved, err := resolveSemanticItems(&src)
	if err != nil {
		return nil, nil, Stats{}, err
	}

	// Source clip — lowers to a full-canvas video layer. When foreground_scale
	// is set the source is scaled and centered on the canvas.
	var sourceLayerIndex = -1
	if src.Source != nil && src.Source.AssetID != "" {
		path := src.Source.Path
		if path == "" {
			registered, err := registry.Register(semanticAssetRef{ID: src.Source.AssetID, SHA256: src.Source.SHA256})
			if err != nil {
				return nil, nil, Stats{}, fmt.Errorf("overlay: source asset: %w", err)
			}
			path = registered
		}
		// FullGraph video layers must carry the same explicit fit contract as
		// background video layers. DirectYUV does not need it, which hid this
		// omission until an overlay/background selected the compositor: Chronon
		// then rendered the overlay over a black canvas instead of the source.
		srcLayer := Layer{ID: "source", Type: "video", Source: path,
			Size: []float64{float64(src.Width), float64(src.Height)}, Fit: "cover", StartFrame: 0}
		// Foreground scale: keep the sampled video surface at canvas size and
		// express the centred transform in Chronon's modular coordinate space.
		// ForegroundScale == 0 or 100 means full-canvas (no scaling).
		if src.ForegroundScale > 0 && src.ForegroundScale < 100 {
			// The modular resolver adds the canvas half-size to unpinned 2D
			// layers. Cancelling that implicit shift here leaves the transform
			// centred in TransformNode's pixel-space contract. Position [0,0]
			// would apply the implicit centre a second time and place the video
			// in the lower-right quadrant.
			srcLayer.Position = []float64{-float64(src.Width) * 0.5, -float64(src.Height) * 0.5}
			srcLayer.Scale = []float64{float64(src.ForegroundScale) / 100, float64(src.ForegroundScale) / 100}
		}
		sourceLayerIndex = len(plan.Layers)
		plan.Layers = append(plan.Layers, srcLayer)
	}

	// Sidecar subtitles remain a published companion asset. Chronon's current
	// render-plan schema has no `subtitle` layer type, so do not emit one.
	if sub := src.Subtitles; sub != nil {
		if len(sub.AssetRefs) == 0 {
			return nil, nil, Stats{}, fmt.Errorf("overlay: subtitles require at least one asset_ref")
		}
		if _, err := registry.Register(sub.AssetRefs[0]); err != nil {
			return nil, nil, Stats{}, fmt.Errorf("overlay: subtitle asset: %w", err)
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
				return nil, nil, Stats{}, fmt.Errorf("overlay: watermark font asset: %w", err)
			}
			font = registered
		}
		if font == "" && wm.Text != "" {
			return nil, nil, Stats{}, fmt.Errorf("overlay: text watermark requires font_ref")
		}
		wmStyle, err := parseStyleBlock(wm.Style)
		if err != nil {
			return nil, nil, Stats{}, err
		}
		style, err := watermarkLayerStyle(wmStyle, font)
		if err != nil {
			return nil, nil, Stats{}, err
		}
		margin, err := watermarkMargin(wm.MarginPX)
		if err != nil {
			return nil, nil, Stats{}, err
		}
		position, err := resolveWatermarkPosition(wm.Position, src.Width, src.Height, margin, wmStyle)
		if err != nil {
			return nil, nil, Stats{}, err
		}
		boxW := float64(src.Width) / 6
		boxH := 80.0
		if wmStyle.WidthPX > 0 {
			boxW = float64(wmStyle.WidthPX)
		}
		if wmStyle.HeightPX > 0 {
			boxH = float64(wmStyle.HeightPX)
		}
		wmLayer := Layer{ID: "watermark", StartFrame: 0, DurationFrames: plan.Canvas.DurationFrames,
			Style: style, Position: position, Size: []float64{boxW, boxH}}
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
				return nil, nil, Stats{}, fmt.Errorf("overlay: watermark asset: %w", err)
			}
			wmLayer.Type = "image"
			wmLayer.Asset = registered
			wmLayer.Fit = "contain"
			if wm.Text != "" {
				wmLayer.Text = wm.Text
			}
		} else {
			return nil, nil, Stats{}, fmt.Errorf("overlay: watermark requires text or asset_refs")
		}
		plan.Layers = append(plan.Layers, wmLayer)
	}

	// Item overlay layers — one compile per resolved kind, counters from the
	// same pass.
	var stats Stats
	for _, ri := range resolved {
		layers, err := compileItem(ri, &src, registry)
		if err != nil {
			return nil, nil, Stats{}, err
		}
		plan.Layers = append(plan.Layers, layers...)
		stats.addResolved(ri)
		if ri.End > plan.Canvas.DurationFrames {
			plan.Canvas.DurationFrames = ri.End
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
		return nil, nil, Stats{}, fmt.Errorf("overlay: semantic plan duration is zero — provide duration_ms or at least one item with end_ms > 0")
	}
	// Stable asset order (the registry sorts) keeps prepared-plan
	// fingerprints reproducible. The plan stays typed; the caller marshals it
	// exactly once at the Chronon boundary.
	return &plan, registry.Assets(), stats, nil
}

// resolveSemanticItems validates the plan's items once and lowers each to its
// canonical resolvedItem. This is the only place an item's kind, preset and
// timing are decided.
func resolveSemanticItems(src *semanticPlan) ([]resolvedItem, error) {
	out := make([]resolvedItem, 0, len(src.Items))
	for _, item := range src.Items {
		start, end := msFrames(item.StartMS, item.EndMS, int64(src.FPSNum), int64(src.FPSDen))
		if item.ID == "" || item.Template == "" || item.StartMS < 0 || item.EndMS <= item.StartMS {
			return nil, fmt.Errorf("overlay: invalid semantic item %q", item.ID)
		}
		spec := templateSpecFor(item.Template)
		kind, err := spec.resolveKind(item.Kind, item.ID)
		if err != nil {
			return nil, err
		}
		// The resolved kind is authoritative for the preset family too, so an
		// unknown template paired with an explicit image kind still validates
		// against the image catalog.
		if isImageKind(kind) {
			spec.Family = PresetImage
		} else if spec.Kind == KindPrimitive {
			spec.Family = PresetText
		}
		params := item.Params
		if params == nil {
			params = map[string]any{}
		}
		for k, v := range item.MotionParams {
			params[k] = v
		}
		ri := resolvedItem{Item: item, Spec: spec, Kind: kind, Params: params, Start: start, End: end}

		if isImageKind(kind) && len(item.Assets) == 0 {
			return nil, fmt.Errorf("overlay: image template %q item %q requires asset_refs", item.Template, item.ID)
		}
		preset, err := presetFor(item, spec)
		if err != nil {
			return nil, err
		}
		ri.PresetID = preset
		if preset != "" {
			def, err := resolveOfficialPreset(preset, string(spec.Family))
			if err != nil {
				return nil, err
			}
			ri.Preset = def
		}
		if isEntityKind(kind) && len(item.Assets) > 0 {
			if imagePreset := strings.TrimSpace(item.ImagePresetID); imagePreset != "" {
				def, err := resolveOfficialPreset(imagePreset, string(PresetImage))
				if err != nil {
					return nil, err
				}
				ri.ImagePreset = def
			}
		}
		out = append(out, ri)
	}
	return out, nil
}

// compileItem dispatches one resolved item to its per-kind compiler. The kind
// is the only discriminator; there is exactly one compiler per kind class and
// exactly one place that decides an entity card becomes image + text.
func compileItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	switch {
	case isEntityKind(ri.Kind):
		if len(ri.Item.Assets) == 0 {
			layer, err := compileTextLayer(ri, src, ri.Item.ID)
			if err != nil {
				return nil, err
			}
			return []Layer{layer}, nil
		}
		return compileEntityCard(ri, src, registry)
	case isImageKind(ri.Kind):
		layer, err := compileImageLayer(ri, src, registry)
		if err != nil {
			return nil, err
		}
		return []Layer{layer}, nil
	default:
		layer, err := compileTextLayer(ri, src, ri.Item.ID)
		if err != nil {
			return nil, err
		}
		return []Layer{layer}, nil
	}
}

// compileEntityCard is the single owner of the "entity card + asset = image +
// text" rule. The image and text layers carry distinct ids derived from the
// item id, so Chronon can never collapse the two into one layer and hide the
// image.
func compileEntityCard(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	img := imageLayer(ri, registry.Path(ri.Item.Assets[0].ID))
	if ri.ImagePreset.ID != "" {
		applyPresetDefinition(&img, ri.ImagePreset)
		imgAnimation, err := animationForDefinition(ri.ImagePreset)
		if err != nil {
			return nil, err
		}
		img.Animation = imgAnimation
		img.Position = resolveImageLayout(ri.ImagePreset.Layout, img.BoxWidth, img.BoxHeight, src.Width, src.Height)
	}
	text, err := compileTextLayer(ri, src, textLayerID(ri.Item.ID))
	if err != nil {
		return nil, err
	}
	return []Layer{img, text}, nil
}

// compileImageLayer lowers an image kind (IMAGE_OVERLAY/PRODUCT/LOGO/…) to a
// single image layer.
func compileImageLayer(ri resolvedItem, src *semanticPlan, registry *assetRegistry) (Layer, error) {
	if len(ri.Item.Assets) == 0 {
		return Layer{}, fmt.Errorf("overlay: image template %q item %q requires asset_refs", ri.Item.Template, ri.Item.ID)
	}
	layer := imageLayer(ri, registry.Path(ri.Item.Assets[0].ID))
	if ri.Preset.ID != "" {
		applyPresetDefinition(&layer, ri.Preset)
	}
	if ri.Item.MotionID != "" {
		animation, err := animationForMotion(ri.Item.MotionID, ri.Item.MotionParams, ri.Item.Text, ri.End-ri.Start)
		if err != nil {
			return Layer{}, err
		}
		if len(animation.Tracks) > 0 {
			layer.Animation = animation
		}
		layer.TextAnimators = animation.TextAnimators
	} else if ri.Preset.ID != "" {
		presetAnimation, err := animationForDefinition(ri.Preset)
		if err != nil {
			return Layer{}, err
		}
		layer.Animation = presetAnimation
	}
	if layer.Position == nil && ri.Preset.ID != "" {
		// A semantic image may request the same explicit center/anchor the
		// official image presets express. Keep the preset as the owner of fit
		// and motion, while honoring this layout intent.
		if position, ok := ri.Params["position"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(position)) {
			case "center":
				layer.Position = []float64{0, 0}
			case "image_left", "left":
				layer.Position = []float64{-float64(src.Width-layer.BoxWidth) / 2, 0}
			case "image_right", "right":
				layer.Position = []float64{float64(src.Width-layer.BoxWidth) / 2, 0}
			case "bottom_right":
				layer.Position = []float64{float64(src.Width-layer.BoxWidth) / 2, float64(src.Height-layer.BoxHeight) / 2}
			default:
				layer.Position = resolveImageLayout(ri.Preset.Layout, layer.BoxWidth, layer.BoxHeight, src.Width, src.Height)
			}
		} else {
			layer.Position = resolveImageLayout(ri.Preset.Layout, layer.BoxWidth, layer.BoxHeight, src.Width, src.Height)
		}
	}
	return layer, nil
}

// compileTextLayer lowers a text kind to a single text layer. Text is
// mandatory: PipelineGen owns the displayed text and RenderingGen never
// invents one (there is no entity_ref fallback).
func compileTextLayer(ri resolvedItem, src *semanticPlan, layerID string) (Layer, error) {
	text := ri.Item.Text
	if strings.TrimSpace(text) == "" {
		return Layer{}, fmt.Errorf("overlay: item %q requires text (PipelineGen owns the displayed text)", ri.Item.ID)
	}
	layer := Layer{ID: layerID, Type: "text", Text: text, StartFrame: ri.Start, DurationFrames: ri.End - ri.Start}
	if ri.Preset.ID != "" {
		applyPresetDefinition(&layer, ri.Preset)
	}
	// Text placement is expressed as a layer top-left plus a local text box.
	// materialize_text uses the serialized box size, while the layer position
	// is applied exactly once by Chronon.
	if layer.BoxWidth <= 0 {
		layer.BoxWidth = src.Width
	}
	if layer.BoxHeight <= 0 {
		layer.BoxHeight = 120
	}
	layer.Size = []float64{float64(layer.BoxWidth), float64(layer.BoxHeight)}

	if ri.Item.MotionID != "" {
		animation, err := animationForMotion(ri.Item.MotionID, ri.Item.MotionParams, ri.Item.Text, ri.End-ri.Start)
		if err != nil {
			return Layer{}, err
		}
		if len(animation.Tracks) > 0 {
			layer.Animation = animation
		}
		layer.TextAnimators = animation.TextAnimators
	} else if ri.Preset.ID != "" {
		// Official text presets lower their motion through the shared
		// animationForPreset path so word/glyph selectors (word_reveal,
		// character_cascade, ...) are transported as text animators instead of
		// being silently compiled away to an empty layer animation. Chronon
		// requires animation objects to carry tracks, so an animator-only
		// motion keeps the animation field absent and rides the layer's
		// text_animators contract.
		presetAnimation, err := animationForPreset(ri.Preset, text, ri.End-ri.Start)
		if err != nil {
			return Layer{}, err
		}
		if presetAnimation != nil {
			if len(presetAnimation.Tracks) == 0 {
				layer.TextAnimators = presetAnimation.TextAnimators
			} else {
				layer.Animation = presetAnimation
			}
		}
	}
	if layer.Style != nil && layer.Position == nil {
		posX, hasPosX := ri.Params["position_x"].(float64)
		posY, hasPosY := ri.Params["position_y"].(float64)
		if hasPosX && hasPosY {
			layer.Position = []float64{posX, posY}
		} else {
			layer.Position = resolveTextLayout(ri.Preset.Layout, layer.BoxWidth, layer.BoxHeight, src.Width, src.Height)
			if hasPosX {
				layer.Position[0] = posX
			}
			if hasPosY {
				layer.Position[1] = posY
			}
		}
	}
	return layer, nil
}

// resolveWatermarkPosition lives in visual_style_resolver.go — the single
// owner of watermark/subtitle geometry resolution.
