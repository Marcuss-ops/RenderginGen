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
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// resolvedItem is RenderingGen's canonical internal representation of one
// overlay item. It is produced once by resolveSemanticItems and consumed by
// the per-kind compilers and the ledger — the raw JSON is never re-read.
type resolvedItem struct {
	Item         semanticItem
	Spec         TemplateSpec
	Kind         ItemKind
	Params       map[string]any
	RuntimeStyle map[string]any
	Start        int64
	End          int64
	// PresetID is the validated official preset for the (text/image) layer.
	PresetID string
	// Preset and ImagePreset are the resolved official definitions.
	Preset      PresetDefinition
	ImagePreset PresetDefinition
}

func compileSemantic(raw []byte) (*Plan, []Asset, Stats, []string, error) {
	var src semanticPlan
	// Strict decode: the published contract declares additionalProperties:false
	// at every level, and the field set is pinned to the schema by
	// contract_schema_parity_test.go. A key outside that set is a producer bug
	// (usually a rename) that must fail loudly here instead of being dropped —
	// the historical permissive decode silently discarded project_id,
	// renderer_version and fingerprint on every job.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&src); err != nil {
		return nil, nil, Stats{}, nil, fmt.Errorf("overlay: decode semantic plan: %w", err)
	}
	if src.PlanID == "" || src.VideoID == "" || src.Width <= 0 || src.Height <= 0 || src.FPSNum <= 0 || src.FPSDen <= 0 {
		return nil, nil, Stats{}, nil, fmt.Errorf("overlay: semantic plan requires plan_id, video_id and positive canvas/fps")
	}
	// A plan must have at least one renderable primitive: a source clip, a
	// background, or an overlay item. An empty plan with nothing to render is
	// always rejected fail-closed.
	if src.Source == nil && src.Background == nil && len(src.Items) == 0 {
		return nil, nil, Stats{}, nil, fmt.Errorf("overlay: semantic plan has no renderable primitives (source, background or items required)")
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
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: unsupported background kind %q", bg.Kind)
		}
		if layerKind == "color" {
			if len(bg.Color) != 4 {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: background color requires RGBA[4]")
			}
		} else if len(bg.AssetRefs) == 0 {
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: %s background requires asset_refs", kind)
		}
		for _, ref := range bg.AssetRefs {
			if _, err := registry.Register(ref); err != nil {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: background asset: %w", err)
			}
		}
		layer := Layer{ID: "background", Type: layerKind, BoxWidth: src.Width, BoxHeight: src.Height,
			Size: []float64{float64(src.Width), float64(src.Height)},
			Fit:  bg.Fit, StartFrame: 0}
		if layerKind != "color" {
			// One gate for the background fit: validated against the closed set
			// the render contract honours, defaulted to the deterministic
			// crop-to-fill, never silently downgraded.
			fit, err := resolveBackgroundFit(bg.Fit)
			if err != nil {
				return nil, nil, Stats{}, nil, err
			}
			layer.Fit = fit
		}
		if bg.Opacity != nil {
			// Declared opacity (including an explicit 0) is carried verbatim as
			// a pointer so the wire cannot lose the "invisible" contract.
			opacity := *bg.Opacity
			layer.Opacity = &opacity
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
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: item %q asset: %w", item.ID, err)
			}
		}
	}

	// Resolve every item ONCE through the template registry: kind validation,
	// preset resolution and timing. Both the per-kind compilers and the ledger
	// read this resolution, so they can never disagree.
	resolved, err := resolveSemanticItems(&src)
	if err != nil {
		return nil, nil, Stats{}, nil, err
	}
	// Templates that resolved to no registry row are reported, not swallowed:
	// they still compile as preset-less primitives, but the caller can see the
	// fall-through (see CompileResult.UnknownTemplates).
	unknown := unknownTemplates(resolved)

	// Source clip — lowers to a full-canvas video layer. When foreground_scale
	// is set the source is scaled and centered on the canvas.
	var sourceLayerIndex = -1
	if src.Source != nil && src.Source.AssetID != "" {
		path := src.Source.Path
		ref := SemanticAssetRef{ID: src.Source.AssetID, SHA256: src.Source.SHA256}
		if path == "" {
			registered, err := registry.Register(ref)
			if err != nil {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: source asset: %w", err)
			}
			path = registered
		} else {
			registered, err := registry.RegisterAtPath(ref, path)
			if err != nil {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: source asset: %w", err)
			}
			path = registered
		}
		// FullGraph video layers must carry the same explicit fit contract as
		// background video layers. DirectYUV does not need it, which hid this
		// omission until an overlay/background selected the compositor: Chronon
		// then rendered the overlay over a black canvas instead of the source.
		srcLayer := Layer{ID: "source", Type: "video", Source: path,
			Size: []float64{float64(src.Width), float64(src.Height)}, Fit: FitCover, StartFrame: 0}
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
		// Card treatment of the clip (border frame + drop shadow). It is
		// lowered onto the source layer itself; the renderer paints the frame
		// behind the media box. A declaration the worker cannot honour is a
		// compile error (see applyFrameTreatment) and never a silent drop.
		if err := applyFrameTreatment(&srcLayer, src.SourceFrame); err != nil {
			return nil, nil, Stats{}, nil, err
		}
		sourceLayerIndex = len(plan.Layers)
		plan.Layers = append(plan.Layers, srcLayer)
	} else if src.SourceFrame != nil {
		// A frame with no source clip to attach to would be dropped silently.
		return nil, nil, Stats{}, nil, fmt.Errorf("overlay: source_frame requires a source clip")
	}

	// Sidecar subtitles remain a published companion asset. Chronon's current
	// render-plan schema has no `subtitle` layer type, so do not emit one.
	if sub := src.Subtitles; sub != nil {
		if len(sub.AssetRefs) == 0 {
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: subtitles require at least one asset_ref")
		}
		if _, err := registry.Register(sub.AssetRefs[0]); err != nil {
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: subtitle asset: %w", err)
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
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: watermark font asset: %w", err)
			}
			font = registered
		}
		if font == "" && wm.Text != "" {
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: text watermark requires font_ref")
		}
		wmStyle, err := parseStyleBlock(wm.Style)
		if err != nil {
			return nil, nil, Stats{}, nil, err
		}
		style, err := watermarkLayerStyle(wmStyle, font)
		if err != nil {
			return nil, nil, Stats{}, nil, err
		}
		margin, err := watermarkMargin(wm.MarginPX)
		if err != nil {
			return nil, nil, Stats{}, nil, err
		}
		// Position AND size come back from the one geometry resolver: the box
		// the position was computed against is the box the layer declares.
		position, size, err := resolveWatermarkGeometry(wm.Position, src.Width, src.Height, margin, wmStyle)
		if err != nil {
			return nil, nil, Stats{}, nil, err
		}
		wmLayer := Layer{ID: "watermark", StartFrame: 0, DurationFrames: plan.Canvas.DurationFrames,
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
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: watermark asset: %w", err)
			}
			wmLayer.Type = "image"
			wmLayer.Asset = registered
			wmLayer.Fit = FitContain
			if wm.Text != "" {
				wmLayer.Text = wm.Text
			}
		} else {
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: watermark requires text or asset_refs")
		}
		// resolveWatermarkGeometry returns the box's absolute canvas top-left;
		// the plan's position field needs the per-type form, so the conversion
		// happens exactly once, after the layer type is known. A text watermark
		// takes the absolute centre, an image watermark the canvas-centre offset.
		wmLayer.Position = canvasBoxPosition(wmLayer.Type,
			position[0], position[1], size[0], size[1], src.Width, src.Height)
		plan.Layers = append(plan.Layers, wmLayer)
	}

	// Item overlay layers — one compile per resolved kind, counters from the
	// same pass.
	var stats Stats
	var cameraMove *SemanticMapCameraMove
	var cameraStartFrame, cameraEndFrame int64
	var cameraMapItemID string
	for _, ri := range resolved {
		layers, err := compileItem(ri, &src, registry)
		if err != nil {
			return nil, nil, Stats{}, nil, err
		}
		if ri.Item.Map != nil && ri.Item.Map.CameraMove != nil {
			if cameraMove != nil {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: only one native camera fly-to map is allowed per plan")
			}
			move := *ri.Item.Map.CameraMove
			cameraMove = &move
			cameraMapItemID = ri.Item.ID
			cameraStartFrame, cameraEndFrame = ri.Start, ri.End
		}
		plan.Layers = append(plan.Layers, layers...)
		stats.addResolved(ri)
		if ri.End > plan.Canvas.DurationFrames {
			plan.Canvas.DurationFrames = ri.End
		}
	}

	if cameraMove != nil {
		if err := compileMapCamera(&plan, *cameraMove, cameraStartFrame, cameraEndFrame, src.FPSNum, src.FPSDen); err != nil {
			return nil, nil, Stats{}, nil, err
		}
		for i := range plan.Layers {
			if strings.HasPrefix(plan.Layers[i].ID, cameraMapItemID+":map_") {
				plan.Layers[i].StartFrame = cameraStartFrame
				plan.Layers[i].DurationFrames = cameraEndFrame - cameraStartFrame
			}
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
		return nil, nil, Stats{}, nil, fmt.Errorf("overlay: semantic plan duration is zero — provide duration_ms or at least one item with end_ms > 0")
	}

	for _, l := range plan.Layers {
		if l.Type == "shape" || l.Shape != nil || plan.Camera != nil || l.ScreenSpace || len(l.Effects) > 0 ||
			len(l.EffectParamTracks) > 0 || len(l.Masks) > 0 || layerAnimationNeedsV3(l.Animation) {
			plan.Schema = "chronon.render-plan.v3"
			plan.Version = 3
			break
		}
	}

	// Stable asset order (the registry sorts) keeps prepared-plan
	// fingerprints reproducible. The plan stays typed; the caller marshals it
	// exactly once at the Chronon boundary.
	return &plan, registry.Assets(), stats, unknown, nil
}

// resolveBackgroundFit validates the requested background fit.
//
// The vocabulary itself lives once in design_tokens.go (fitVocabulary), so the
// background and the item/video lowering cannot accept different spellings: a
// producer could previously ask for a treatment that one site accepted and the
// other silently rewrote — the historical "blur_cover" background hint did
// exactly that. Empty means the deterministic crop-to-fill default ("cover"),
// which fills the canvas at any source aspect ratio.
func resolveBackgroundFit(requested string) (string, error) {
	fit := strings.ToLower(strings.TrimSpace(requested))
	if fit == "" {
		return FitCover, nil
	}
	if !isSupportedFit(fit) {
		return "", fmt.Errorf("overlay: unsupported background fit %q (supported: %s)", requested, supportedFits)
	}
	return fit, nil
}

// unknownTemplates returns the sorted, de-duplicated template_ids of the items
// that found no registry row. It is the only place the fall-through is
// classified, and it reads the SAME resolved spec the compiler lowered, so it
// can never disagree with what was actually emitted.
func unknownTemplates(items []resolvedItem) []string {
	seen := make(map[string]bool)
	var out []string
	for _, ri := range items {
		if ri.Spec.Registered {
			continue
		}
		id := strings.TrimSpace(ri.Item.Template)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// resolveSemanticItems validates the plan's items once and lowers each to its
// canonical resolvedItem. This is the only place an item's kind, preset and
// timing are decided.
func resolveSemanticItems(src *semanticPlan) ([]resolvedItem, error) {
	out := make([]resolvedItem, 0, len(src.Items))
	for _, item := range src.Items {
		start, end := msFrames(item.StartMS, item.EndMS, int64(src.FPSNum), int64(src.FPSDen))
		if item.ID == "" || item.StartMS < 0 || item.EndMS <= item.StartMS {
			return nil, fmt.Errorf("overlay: invalid semantic item %q", item.ID)
		}
		if end <= start {
			return nil, fmt.Errorf("overlay: item %q window [%d,%d) ms collapses to no frames at %d/%d fps",
				item.ID, item.StartMS, item.EndMS, src.FPSNum, src.FPSDen)
		}
		if err := validateTextElementContract(item); err != nil {
			return nil, err
		}
		if err := validateSemanticImageLayers(item); err != nil {
			return nil, err
		}
		spec := templateSpecFor(item.Template)
		kind, err := spec.resolveKind(item.Kind, item.ID)
		if err != nil {
			return nil, err
		}
		// The map declaration is validated for EVERY item, not only map ones: a
		// map block on a phrase item would otherwise be carried through the plan
		// and silently ignored, which is exactly the kind of contract drift the
		// worker exists to stop.
		if err := validateMapContract(item, kind, src.Width, src.Height); err != nil {
			return nil, err
		}
		// The resolved kind is authoritative for the preset family too, so an
		// unknown template paired with an explicit image kind still validates
		// against the image catalog. Registered DATE/METRIC presentation kinds
		// also stay on the text preset family while their dedicated catalog
		// motion_id controls the presentation animation.
		if isImageKind(kind) {
			spec.Family = PresetImage
		} else if spec.Kind == KindPrimitive || kind == KindNumber || kind == KindMetricStat || kind == KindTimelineDate {
			spec.Family = PresetText
		}
		params := make(map[string]any, len(item.Params)+len(item.Style))
		for k, v := range item.Params {
			params[k] = v
		}
		for k, v := range item.Style {
			params[k] = v
		}
		runtimeStyle := make(map[string]any, len(item.Params)+len(item.Style))
		for k, v := range item.Params {
			runtimeStyle[k] = v
		}
		for k, v := range item.Style {
			runtimeStyle[k] = v
		}
		ri := resolvedItem{Item: item, Spec: spec, Kind: kind, Params: params, RuntimeStyle: runtimeStyle, Start: start, End: end}

		if isImageKind(kind) && len(item.Assets) == 0 && len(item.ImageLayers) == 0 {
			return nil, fmt.Errorf("overlay: image template %q item %q requires asset_refs", item.Template, item.ID)
		}
		if isVideoKind(kind) && len(item.Assets) == 0 {
			return nil, fmt.Errorf("overlay: video overlay template %q item %q requires asset_refs", item.Template, item.ID)
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
		if err := validateTextRuntimeOverrides(runtimeStyle, item.ID, kind, preset, len(item.Assets) > 0); err != nil {
			return nil, err
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
// one compatibility path for legacy entity_card items that still carry an
// image asset.
func compileItem(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	switch {
	case isEntityKind(ri.Kind):
		if len(ri.Item.ImageLayers) > 0 {
			return compileImageLayers(ri, src, registry)
		}
		if len(ri.Item.Assets) == 0 {
			layer, err := compileTextLayer(ri, src, ri.Item.ID)
			if err != nil {
				return nil, err
			}
			return []Layer{layer}, nil
		}
		return compileEntityCard(ri, src, registry)
	case isVideoKind(ri.Kind):
		layer, err := compileVideoOverlayLayer(ri, src, registry)
		if err != nil {
			return nil, err
		}
		return []Layer{layer}, nil
	case isImageKind(ri.Kind):
		return compileImageLayers(ri, src, registry)
	case isShapeKind(ri.Kind):
		layer, err := compileShapeLayer(ri, src)
		if err != nil {
			return nil, err
		}
		return []Layer{layer}, nil
	case isMapKind(ri.Kind):
		return compileMapLayers(ri, src, registry)
	default:
		layer, err := compileTextLayer(ri, src, ri.Item.ID)
		if err != nil {
			return nil, err
		}
		return []Layer{layer}, nil
	}
}

// compileEntityCard is the compatibility path for legacy entity_card payloads
// that still carry an asset. Portrait entities are image-only: the image preset
// owns the geometry and motion, while the name stays provenance metadata and
// is not rendered as a second lower-third layer.
func compileEntityCard(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	img := imageLayer(ri, registry.Path(ri.Item.Assets[0].ID))
	if ri.ImagePreset.ID != "" {
		applyPresetDefinition(&img, ri.ImagePreset)
		var imgAnimation *LayerAnimation
		var err error
		if ri.Item.MotionID != "" {
			imgAnimation, err = imageMotionAnimation(ri.Item.MotionID, ri.Item.MotionParams, ri.End-ri.Start, ri.ImagePreset.Motion.Exit)
		} else {
			imgAnimation, err = animationForPreset(ri.ImagePreset, "", ri.End-ri.Start)
		}
		if err != nil {
			return nil, err
		}
		applyMotionRouting(&img, imgAnimation)
		// Entity portraits are centered visual subjects.  The image presets
		// still own the box size, but their legacy left/right/bottom anchors
		// must not move the portrait off-canvas. Keep the resolved size
		// untouched and use contain so the source is fully visible without crop.
		img.Position = []float64{0, 0}
		img.Fit = FitContain
	}
	if err := applyFrameTreatment(&img, ri.Item.Frame); err != nil {
		return nil, fmt.Errorf("overlay: entity image item %q: %w", ri.Item.ID, err)
	}
	if ri.Item.MotionID != "" && ri.ImagePreset.ID == "" {
		return nil, fmt.Errorf("overlay: entity image item %q with motion_id requires image_preset_id", ri.Item.ID)
	}
	premium, err := premiumImageDefinition(ri.Item.MotionID)
	if err != nil {
		return nil, fmt.Errorf("overlay: entity image motion %q: %w", ri.Item.MotionID, err)
	}
	layers, err := compilePremiumImageLayers(ri, src, img, premium)
	if err != nil {
		return nil, err
	}
	if caption := strings.TrimSpace(ri.Item.EntityCaption); caption != "" {
		imageIndex := premiumImageLayerIndex(layers)
		captionLayer, err := compileEntityCaptionLayer(ri, src, ri.Item, caption, &layers[imageIndex], "entity")
		if err != nil {
			return nil, err
		}
		if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
			premiumLayerActive(&captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
		}
		layers = append(layers, captionLayer)
	}
	if premium != nil && premium.ImageRecipe.RequireCaption && strings.TrimSpace(ri.Item.EntityCaption) == "" {
		return nil, fmt.Errorf("overlay: motion %q requires entity_caption", premium.ID)
	}
	return layers, nil
}

// compileVideoOverlayLayer lowers a rendered video overlay to a TIMED video
// layer inside the same render pass. It is the single-pass replacement for
// compositing a pre-rendered segment with a post-render transcoder:
//
//   - Source is the overlay segment's content-addressed path (video layers
//     carry `source`, exactly like the source clip and the background);
//   - StartFrame/DurationFrames come from the item's start_ms/end_ms, and
//     Chronon's VideoNode samples the segment at (frame - layer_start), so
//     the segment's first frame lands on the declared start frame and the
//     layer is composited only inside its window;
//   - the layer is appended AFTER the source layer, so its z-order places the
//     overlay over the clip.
//
// Geometry is full-canvas "cover" by default (the producer renders the
// segment at the output contract), overridable via params.fit. Fail-closed:
// a video overlay without an asset is rejected during resolution.
func compileVideoOverlayLayer(ri resolvedItem, src *semanticPlan, registry *assetRegistry) (Layer, error) {
	if len(ri.Item.Assets) == 0 {
		return Layer{}, fmt.Errorf("overlay: video overlay template %q item %q requires asset_refs", ri.Item.Template, ri.Item.ID)
	}
	fit := stringParam(ri.Params, "fit", FitCover)
	if !isSupportedFit(fit) {
		// The same closed vocabulary as the background; only the message names
		// the item, because this is the only place a per-item fit is validated.
		return Layer{}, fmt.Errorf("overlay: video overlay item %q has unsupported fit %q (supported: %s)", ri.Item.ID, fit, supportedFits)
	}
	layer := Layer{
		ID:             overlayLayerID(ri.Item.ID),
		Type:           "video",
		Source:         registry.Path(ri.Item.Assets[0].ID),
		Size:           []float64{float64(src.Width), float64(src.Height)},
		Fit:            fit,
		StartFrame:     ri.Start,
		DurationFrames: ri.End - ri.Start,
	}
	if opacity, ok := ri.Params["opacity"].(float64); ok {
		if opacity < 0 || opacity > 1 {
			return Layer{}, fmt.Errorf("overlay: video overlay item %q has opacity %v outside [0,1]", ri.Item.ID, opacity)
		}
		layer.Opacity = &opacity
	}
	// scale_percent insets a full-canvas overlay segment so its declared frame
	// is actually visible. It uses the same centred transform contract as the
	// source clip's foreground_scale_percent (which is the only place the
	// modular resolver's implicit canvas-centre shift is cancelled).
	if raw, exists := ri.Params["scale_percent"]; exists {
		percent, ok := numericParam(raw)
		if !ok || percent <= 0 || percent > 100 {
			return Layer{}, fmt.Errorf("overlay: video overlay item %q has scale_percent outside (0,100]", ri.Item.ID)
		}
		if percent < 100 {
			layer.Position = []float64{-float64(src.Width) * 0.5, -float64(src.Height) * 0.5}
			layer.Scale = []float64{percent / 100, percent / 100}
		}
	}
	if err := applyFrameTreatment(&layer, ri.Item.Frame); err != nil {
		return Layer{}, fmt.Errorf("overlay: item %q: %w", ri.Item.ID, err)
	}
	return layer, nil
}

// applyFrameTreatment lowers a card declaration (plan-level source_frame or an
// item's frame) onto a media layer. It is the SINGLE owner of the translation:
//
//   - the border becomes the renderer-owned frame behind the media box, i.e.
//     style.background with the border colour, the border thickness as padding
//     and radius_px as the frame's outer corner radius;
//   - the media box itself takes the concentric inner radius
//     (radius_px - width_px), so the clip's corners nest inside the frame;
//   - the shadow becomes style.shadow, which the renderer attaches to the
//     frame when a frame exists and to the media layer otherwise.
//
// Every value comes from the declaration: the worker invents no colour, blur or
// geometry. A declaration with neither border nor shadow, an out-of-range
// value or a malformed colour is rejected here (fail-closed).
func applyFrameTreatment(layer *Layer, frame *semanticSourceFrame) error {
	if frame == nil {
		return nil
	}
	if frame.Border == nil && frame.Shadow == nil {
		return fmt.Errorf("frame requires border or shadow")
	}
	if frame.Border != nil {
		border := frame.Border
		if math.IsNaN(border.WidthPX) || math.IsInf(border.WidthPX, 0) || border.WidthPX < 0 || border.WidthPX > maxFrameBorderWidth {
			return fmt.Errorf("frame border width_px must be within [0,%g]", maxFrameBorderWidth)
		}
		if math.IsNaN(border.RadiusPX) || math.IsInf(border.RadiusPX, 0) || border.RadiusPX < 0 || border.RadiusPX > maxFrameRadius {
			return fmt.Errorf("frame border radius_px must be within [0,%g]", maxFrameRadius)
		}
		if !isHexColor(border.Color) {
			return fmt.Errorf("frame border color %q must be #RRGGBB", border.Color)
		}
		if border.WidthPX > 0 {
			style := ensureLayerStyle(layer)
			style.Background = &LayerBackground{
				Color:   border.Color,
				Radius:  border.RadiusPX,
				Padding: []float64{border.WidthPX, border.WidthPX},
			}
		}
		// Concentric inner radius: the clip's corners nest inside the frame's.
		if inner := border.RadiusPX - border.WidthPX; inner > 0 {
			layer.Radius = inner
		}
	}
	if frame.Shadow != nil {
		shadow := frame.Shadow
		if !isHexColor(shadow.Color) {
			return fmt.Errorf("frame shadow color %q must be #RRGGBB", shadow.Color)
		}
		if math.IsNaN(shadow.Opacity) || shadow.Opacity < 0 || shadow.Opacity > 1 {
			return fmt.Errorf("frame shadow opacity must be within [0,1]")
		}
		if math.IsNaN(shadow.BlurPX) || shadow.BlurPX < 0 || shadow.BlurPX > maxFrameShadowBlur {
			return fmt.Errorf("frame shadow blur_px must be within [0,%g]", maxFrameShadowBlur)
		}
		if math.IsNaN(shadow.OffsetXP) || math.Abs(shadow.OffsetXP) > maxFrameShadowOffset ||
			math.IsNaN(shadow.OffsetYP) || math.Abs(shadow.OffsetYP) > maxFrameShadowOffset {
			return fmt.Errorf("frame shadow offsets must be within ±%g", maxFrameShadowOffset)
		}
		style := ensureLayerStyle(layer)
		style.Shadow = &LayerShadow{
			Color:   shadow.Color,
			Opacity: shadow.Opacity,
			Blur:    shadow.BlurPX,
			Offset:  []float64{shadow.OffsetXP, shadow.OffsetYP},
		}
	}
	return nil
}

// Bounds of the frame contract. They mirror the published schema, so a value
// the contract admits is never rejected here and vice versa.
const (
	maxFrameBorderWidth  float64 = 512
	maxFrameRadius       float64 = 512
	maxFrameShadowBlur   float64 = 256
	maxFrameShadowOffset float64 = 256
)

func ensureLayerStyle(layer *Layer) *LayerStyle {
	if layer.Style == nil {
		layer.Style = &LayerStyle{}
	}
	return layer.Style
}

// isHexColor accepts exactly the #RRGGBB form the render plan's colour
// properties declare (the engine parses no other spelling).
func isHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, digit := range value[1:] {
		switch {
		case digit >= '0' && digit <= '9':
		case digit >= 'a' && digit <= 'f':
		case digit >= 'A' && digit <= 'F':
		default:
			return false
		}
	}
	return true
}

// compileImageLayers lowers an image kind (IMAGE_OVERLAY/PRODUCT/LOGO/…) to one
// or more Chronon image layers. Composite children share one semantic item and
// queue render while retaining independent timing and motion.
func validateSemanticImageLayers(item semanticItem) error {
	if len(item.ImageLayers) == 0 {
		return nil
	}
	kind := ItemKind(strings.ToLower(strings.TrimSpace(item.Kind)))
	if kind == "" {
		kind = templateSpecFor(item.Template).Kind
	}
	if behaviorOf(kind) != behaviorImage || len(item.ImageLayers) < 2 || len(item.Assets) < 2 {
		return fmt.Errorf("overlay: item %q image_layers require an image item with at least two layers and assets", item.ID)
	}
	assets := make(map[string]struct{}, len(item.Assets))
	for _, ref := range item.Assets {
		assets[ref.ID] = struct{}{}
	}
	layerIDs := make(map[string]struct{}, len(item.ImageLayers))
	parentDuration := item.EndMS - item.StartMS
	for index, layer := range item.ImageLayers {
		if strings.TrimSpace(layer.ID) == "" || strings.TrimSpace(layer.AssetID) == "" {
			return fmt.Errorf("overlay: item %q image_layers[%d] requires id and asset_id", item.ID, index)
		}
		if _, ok := assets[layer.AssetID]; !ok {
			return fmt.Errorf("overlay: item %q image layer %q references undeclared asset %q", item.ID, layer.ID, layer.AssetID)
		}
		if _, exists := layerIDs[layer.ID]; exists {
			return fmt.Errorf("overlay: item %q has duplicate image layer id %q", item.ID, layer.ID)
		}
		layerIDs[layer.ID] = struct{}{}
		if layer.StartMS < 0 || layer.EndMS <= layer.StartMS || layer.EndMS > parentDuration {
			return fmt.Errorf("overlay: item %q image layer %q has invalid relative timing [%d,%d) for parent duration %dms", item.ID, layer.ID, layer.StartMS, layer.EndMS, parentDuration)
		}
		if strings.TrimSpace(layer.PresetID) == "" {
			return fmt.Errorf("overlay: item %q image layer %q requires preset_id", item.ID, layer.ID)
		}
	}
	return nil
}

func compileImageLayers(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	premium, err := premiumImageDefinition(ri.Item.MotionID)
	if err != nil {
		return nil, fmt.Errorf("overlay: image motion %q: %w", ri.Item.MotionID, err)
	}
	visualAccents, err := visualAccentsDefinition(ri.Item.MotionID)
	if err != nil {
		return nil, fmt.Errorf("overlay: visual accents motion %q: %w", ri.Item.MotionID, err)
	}
	if visualAccents != nil && visualAccents.ImageRecipe != nil && visualAccents.ImageRecipe.Stack {
		return nil, fmt.Errorf("overlay: motion %q requires multiple image_layers", visualAccents.ID)
	}
	if len(ri.Item.ImageLayers) == 0 {
		if len(ri.Item.Assets) == 0 {
			return nil, fmt.Errorf("overlay: image template %q item %q requires asset_refs", ri.Item.Template, ri.Item.ID)
		}
		if premium != nil && premium.ImageRecipe.Stack {
			return nil, fmt.Errorf("overlay: motion %q requires at least two image_layers and motion_params.active_layer_id", premium.ID)
		}
		layer, err := compileSingleImageLayer(ri, src, registry.Path(ri.Item.Assets[0].ID), ri.Preset, ri.Item.MotionID, ri.Item.MotionParams, ri.Start, ri.End, ri.Item.ID)
		if err != nil {
			return nil, err
		}
		if visualAccents != nil {
			layers, err := compileVisualAccentsImageLayers(ri, src, layer, visualAccents)
			if err != nil {
				return nil, err
			}
			return layers, nil
		}
		layers, err := compilePremiumImageLayers(ri, src, layer, premium)
		if err != nil {
			return nil, err
		}
		if caption := strings.TrimSpace(ri.Item.EntityCaption); caption != "" {
			imageIndex := premiumImageLayerIndex(layers)
			captionLayer, err := compileEntityCaptionLayer(ri, src, ri.Item, caption, &layers[imageIndex], "entity")
			if err != nil {
				return nil, err
			}
			if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
				premiumLayerActive(&captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
			}
			return append(layers, captionLayer), nil
		}
		if premium != nil && premium.ImageRecipe.RequireCaption {
			return nil, fmt.Errorf("overlay: motion %q requires entity_caption", premium.ID)
		}
		return layers, nil
	}
	if premium != nil && premium.ImageRecipe.Stack {
		return compilePremiumImageStack(ri, src, registry, premium)
	}
	if premium != nil && premium.ImageRecipe.RequireCaption {
		for _, child := range ri.Item.ImageLayers {
			if strings.TrimSpace(child.Caption) == "" {
				return nil, fmt.Errorf("overlay: motion %q requires a caption on every image layer", premium.ID)
			}
		}
	}
	assets := make(map[string]string, len(ri.Item.Assets))
	for _, ref := range ri.Item.Assets {
		assets[ref.ID] = registry.Path(ref.ID)
	}
	layers := make([]Layer, 0, len(ri.Item.ImageLayers)*2)
	for _, child := range ri.Item.ImageLayers {
		assetPath, ok := assets[child.AssetID]
		if !ok {
			return nil, fmt.Errorf("overlay: composite image item %q layer %q references undeclared asset %q", ri.Item.ID, child.ID, child.AssetID)
		}
		preset, err := resolveOfficialPreset(child.PresetID, string(PresetImage))
		if err != nil {
			return nil, fmt.Errorf("overlay: composite image item %q layer %q: %w", ri.Item.ID, child.ID, err)
		}
		startOffset, endOffset := msFrames(child.StartMS, child.EndMS, int64(src.FPSNum), int64(src.FPSDen))
		start, end := ri.Start+startOffset, ri.Start+endOffset
		params := child.Params
		if params == nil {
			params = map[string]any{}
		}
		childItem := ri.Item
		childItem.ID = ri.Item.ID + ":" + child.ID
		childItem.PresetID = child.PresetID
		childItem.MotionID = child.MotionID
		childItem.MotionParams = child.MotionParams
		childItem.EntityCaption = ""
		childItem.CaptionMotionID = ""
		childItem.StartMS, childItem.EndMS = child.StartMS, child.EndMS
		childItem.Params = params
		childResolved := ri
		childResolved.Item = childItem
		childResolved.Params = params
		childResolved.Start, childResolved.End = start, end
		layer, err := compileSingleImageLayer(childResolved, src, assetPath, preset, child.MotionID, child.MotionParams, start, end, ri.Item.ID+":"+child.ID)
		if err != nil {
			return nil, err
		}
		// Keep camera-backed 2.5D tracks as authored. Image geometry and
		// motion are independent contract inputs; Enable3D is derived by the
		// shared motion-routing helper above.
		decorated, err := compilePremiumImageLayers(childResolved, src, layer, premium)
		if err != nil {
			return nil, err
		}
		if caption := strings.TrimSpace(child.Caption); caption != "" {
			imageIndex := premiumImageLayerIndex(decorated)
			captionLayer, err := compileEntityCaptionLayer(ri, src, childItem, caption, &decorated[imageIndex], child.ID)
			if err != nil {
				return nil, err
			}
			if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
				premiumLayerActive(&captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
			}
			layers = append(layers, decorated...)
			layers = append(layers, captionLayer)
		} else {
			layers = append(layers, decorated...)
		}
	}
	return layers, nil
}

func compileEntityCaptionLayer(parent resolvedItem, src *semanticPlan, child semanticItem, caption string, image *Layer, childID string) (Layer, error) {
	if len(image.Size) < 2 || image.Size[0] <= 0 || image.Size[1] <= 0 {
		return Layer{}, fmt.Errorf("overlay: item %q image caption has no positive image geometry", parent.Item.ID)
	}
	// The resolver owns the geometry: image bottom_center + margin anchor,
	// safe-area clamping and long-name font fitting all happen there. The
	// lowering below only writes the resolver's answer into the layer.
	imageBounds := EntityCardImageBoundsFromCenter(src.Width, src.Height, image.Position, image.Size[0], image.Size[1])
	layout, err := ResolveEntityCardLayout(src.Width, src.Height, imageBounds, caption)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: item %q entity caption layout: %w", parent.Item.ID, err)
	}
	// Vertical fallback: the resolver may shift the image up so the pair
	// image + margin + caption fits inside the safe area. Image layers are
	// positioned relative to the canvas center, so an upward canvas shift of
	// `shift` px converts to exactly minus `shift`; the caption's geometry
	// below is canvas-absolute.
	if shift := imageBounds.Y - layout.ImageBounds.Y; shift != 0 {
		if len(image.Position) < 2 {
			image.Position = []float64{0, 0}
		}
		image.Position[1] -= shift
	}
	captionBounds := layout.CaptionBounds
	width := int(captionBounds.Width)
	if width < 1 {
		width = 1
	}
	captionItem := parent.Item
	captionItem.ID = parent.Item.ID + ":" + childID + ":caption"
	captionItem.Kind = string(KindEntityCard)
	captionItem.Template = "PERSON_DEFAULT"
	captionItem.PresetID = PhraseDefaultPresetID
	captionItem.Text = caption
	// The caption is a first-class animated layer: resolve the requested
	// motion (or the shared default) and refuse non-text motions here, before
	// compileTextLayer lowers MotionID — an image motion on a caption would
	// otherwise lower camera-backed tracks a text layer cannot honor.
	captionMotion := EntityCaptionMotionID(child.CaptionMotionID)
	if !entityCaptionMotionAllowed(captionMotion) {
		captionMotion = ""
	}
	captionItem.MotionID = captionMotion
	captionItem.MotionParams = nil
	captionItem.Assets = nil
	captionItem.ImageLayers = nil
	positionX := captionBounds.CenterX
	positionY := captionBounds.CenterY
	captionItem.Params = map[string]any{
		"position_x":   positionX,
		"position_y":   positionY,
		"font_size_px": captionBounds.FontSize,
	}
	captionItem.Style = nil
	captionResolved := resolvedItem{
		Item: captionItem, Spec: parent.Spec, Kind: KindEntityCard,
		Params: captionItem.Params,
		Start:  image.StartFrame, End: image.StartFrame + image.DurationFrames,
	}
	captionResolved.Preset = phraseDefaultPreset()
	captionLayer, err := compileTextLayer(captionResolved, src, captionItem.ID)
	if err != nil {
		return Layer{}, fmt.Errorf("overlay: entity image caption %q: %w", captionItem.ID, err)
	}
	captionLayer.BoxWidth = width
	captionLayer.BoxHeight = int(captionBounds.Height)
	captionLayer.Size = []float64{captionBounds.Width, captionBounds.Height}
	captionLayer.Position = []float64{captionBounds.CenterX, captionBounds.CenterY}
	captionLayer.Style.Fill = "#FFFFFF"
	captionLayer.Style.FontSize = captionBounds.FontSize
	captionLayer.Style.MinFontSize = captionBounds.FontSize
	captionLayer.Style.MaxFontSize = captionBounds.FontSize
	captionLayer.Style.Background = &LayerBackground{
		Color: "#111827", Opacity: floatPointer(0.82), Radius: 18,
		Padding: []float64{16, 8},
	}
	return captionLayer, nil
}

func compileSingleImageLayer(ri resolvedItem, src *semanticPlan, assetPath string, preset PresetDefinition, motionID string, motionParams map[string]any, start, end int64, layerID string) (Layer, error) {
	layer := imageLayer(ri, assetPath)
	layer.ID = imageLayerID(layerID)
	layer.StartFrame, layer.DurationFrames = start, end-start
	applyPresetDefinition(&layer, preset)
	if motionID != "" {
		animation, err := imageMotionAnimation(motionID, motionParams, end-start, preset.Motion.Exit)
		if err != nil {
			return Layer{}, err
		}
		// Explicit image motion_id is authoritative for both entity and generic
		// images; 2.5D recipes are no longer silently discarded for portraits.
		applyMotionRouting(&layer, animation)
	} else if preset.ID != "" {
		animation, err := animationForPreset(preset, "", end-start)
		if err != nil {
			return Layer{}, err
		}
		applyMotionRouting(&layer, animation)
	}
	if x, ok := numericParam(ri.Params["position_x"]); ok {
		y, _ := numericParam(ri.Params["position_y"])
		layer.Position = []float64{x, y}
	} else if layer.Position == nil {
		if ri.Kind == KindEntityImage {
			layer.Position = []float64{0, 0}
			layer.Fit = FitContain
		} else if position, ok := ri.Params["position"].(string); ok && strings.EqualFold(strings.TrimSpace(position), "center") {
			layer.Position = []float64{0, 0}
		} else {
			layer.Position = resolveImageLayout(preset.Layout, layer.BoxWidth, layer.BoxHeight, src.Width, src.Height)
		}
	}
	if y, ok := numericParam(ri.Params["position_y"]); ok {
		if layer.Position == nil {
			layer.Position = []float64{0, 0}
		}
		layer.Position[1] = y
	}
	if ri.Kind == KindEntityImage {
		layer.Fit = FitContain
		layer.EntityImage = true
	}
	if err := applyFrameTreatment(&layer, ri.Item.Frame); err != nil {
		return Layer{}, fmt.Errorf("overlay: image item %q: %w", ri.Item.ID, err)
	}
	return layer, nil
}

func numericParam(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

// FitEntityImageLayerToAsset matches an entity image's bounded contain box to
// the source aspect ratio. Call after the processor materializes the asset at
// assetPath; compilation alone only knows its logical path.
func FitEntityImageLayerToAsset(layer *Layer, assetPath string) error {
	if layer == nil || strings.TrimSpace(assetPath) == "" || layer.BoxWidth <= 0 || layer.BoxHeight <= 0 {
		return fmt.Errorf("missing image geometry")
	}
	f, err := os.Open(assetPath)
	if err != nil {
		return err
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return err
	}
	if config.Width <= 0 || config.Height <= 0 {
		return fmt.Errorf("invalid source dimensions %dx%d", config.Width, config.Height)
	}
	maxW, maxH := layer.BoxWidth, layer.BoxHeight
	if int64(config.Width)*int64(maxH) > int64(config.Height)*int64(maxW) {
		layer.BoxWidth = maxW
		layer.BoxHeight = max(1, int(float64(maxW)*float64(config.Height)/float64(config.Width)+0.5))
	} else {
		layer.BoxHeight = maxH
		layer.BoxWidth = max(1, int(float64(maxH)*float64(config.Width)/float64(config.Height)+0.5))
	}
	layer.Size = []float64{float64(layer.BoxWidth), float64(layer.BoxHeight)}
	// The box now has the source's aspect ratio, so cover fills the complete
	// surface without cropping meaningful image content or leaving a dark
	// contain matte around the texture.
	layer.Fit = FitCover
	return nil
}

// phraseWrapCols is the hard line-length budget for important phrases:
// ogni 25 caratteri a capo correttamente, word-aware, mai tagliando una parola
// a metà. Una parola isolata più lunga di 25 resta intera sulla propria riga.
const phraseWrapCols = 25

// phraseLineHeight is the estimated raster height of one wrapped line at the
// phrase preset's authored font size (112) with its glow/shadow. It is used
// only to grow the text box when wrapping produces more lines than the
// preset's 260px can hold — the renderer still owns the final line layout.
const phraseLineHeight = 132

func wrapPhraseAt25(text string) (string, int) {
	if utf8.RuneCountInString(strings.TrimSpace(text)) <= phraseWrapCols {
		return strings.TrimSpace(text), 1
	}
	// Respect existing hard breaks: each paragraph is wrapped independently
	// so an authored manuale \n is never collapsed.
	paragraphs := strings.Split(text, "\n")
	var out []string
	for _, para := range paragraphs {
		trimmed := strings.TrimSpace(para)
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(trimmed)
		if len(words) == 0 {
			continue
		}
		var cur strings.Builder
		curLen := 0
		for idx, word := range words {
			wLen := utf8.RuneCountInString(word)
			if curLen == 0 {
				cur.WriteString(word)
				curLen = wLen
			} else if curLen+1+wLen <= phraseWrapCols {
				cur.WriteString(" ")
				cur.WriteString(word)
				curLen += 1 + wLen
			} else {
				out = append(out, cur.String())
				cur.Reset()
				cur.WriteString(word)
				curLen = wLen
			}
			if idx == len(words)-1 {
				out = append(out, cur.String())
			}
		}
	}
	if len(out) == 0 {
		return strings.TrimSpace(text), 1
	}
	lineCount := len(out)
	// Drop a leading/trailing empty produced by borderline splits.
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
		lineCount--
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
		lineCount--
	}
	if lineCount <= 0 {
		return strings.TrimSpace(text), 1
	}
	return strings.Join(out, "\n"), lineCount
}

// compileTextLayer lowers a text kind to a single text layer. Text is
// mandatory: PipelineGen owns the displayed text and RenderingGen never
// invents one (there is no entity_ref fallback).
func compileTextLayer(ri resolvedItem, src *semanticPlan, layerID string) (Layer, error) {
	rawText := ri.Item.Text
	if strings.TrimSpace(rawText) == "" {
		return Layer{}, fmt.Errorf("overlay: item %q requires text (PipelineGen owns the displayed text)", ri.Item.ID)
	}
	// Ogni 25 caratteri a capo correttamente: word-aware wrap that never cuts
	// a word. Applied at compile so Chronon's word wrap and the motion's
	// glyph stagger both operate on the finalized line breaks, and every line
	// remains horizontally Center-aligned by materialize_text.
	text, wrappedLines := wrapPhraseAt25(rawText)
	// Preserve the phrase kind's semantic flag for the motion floor even when
	// the raw kind was routed through the text branch.
	layer := Layer{ID: layerID, Type: "text", Text: text, StartFrame: ri.Start, DurationFrames: ri.End - ri.Start}
	if ri.Preset.ID != "" {
		applyPresetDefinition(&layer, ri.Preset)
		if layer.Style != nil {
			layer.Style.Font = OfficialFontPathForLanguage(src.Language)
		}
	}
	if err := applyTextRuntimeOverrides(&layer, ri.RuntimeStyle); err != nil {
		return Layer{}, fmt.Errorf("overlay: item %q: %w", ri.Item.ID, err)
	}
	// Text placement is expressed as a layer top-left plus a local text box.
	// materialize_text uses the serialized box size, while the layer position
	// is applied exactly once by Chronon.
	if width, ok := numericParam(ri.Params["width"]); ok && width > 0 {
		layer.BoxWidth = int(width)
	}
	if height, ok := numericParam(ri.Params["height"]); ok && height > 0 {
		layer.BoxHeight = int(height)
	}
	if layer.BoxWidth <= 0 {
		layer.BoxWidth = src.Width
		if ri.Preset.Family == PresetText && ri.Preset.Layout.BoxWidth > 0 && ri.Preset.Layout.BoxWidth < layer.BoxWidth {
			layer.BoxWidth = ri.Preset.Layout.BoxWidth
		}
	}
	if layer.BoxHeight <= 0 {
		layer.BoxHeight = DefaultTextBoxHeight
		if ri.Preset.Family == PresetText && ri.Preset.Layout.BoxHeight > 0 {
			layer.BoxHeight = ri.Preset.Layout.BoxHeight
		}
	}
	// Grow the text box when the 25-char wrap produced more lines than the
	// preset's 260px (≈3.3 lines) can hold — the renderer owns the final
	// line layout but the box must be tall enough to not clip centred lines.
	if wrappedLines > 1 {
		needed := wrappedLines*phraseLineHeight + 16 // glow/shadow padding
		if needed > layer.BoxHeight {
			if needed > src.Height {
				needed = src.Height
			}
			layer.BoxHeight = needed
		}
	}
	layer.Size = []float64{float64(layer.BoxWidth), float64(layer.BoxHeight)}

	var textAnimation *LayerAnimation
	if ri.Item.MotionID != "" {
		// Both halves of the selected motion travel, never one of them: the
		// layer tracks and the per-unit text animators are produced by the same
		// lowering pass (see lowerMotion). The preset's exit window is the
		// fallback for a motion that declares none. The wrapped text is used
		// as the motion context so the stagger aligns with the final lines.
		animation, err := animationForMotion(ri.Item.MotionID, ri.Item.MotionParams, text, ri.End-ri.Start, ri.Preset.Motion.Exit, ri.Kind == KindImportantPhrase)
		if err != nil {
			return Layer{}, err
		}
		textAnimation = animation
	} else if ri.Preset.ID != "" {
		// Official text presets lower their motion through the shared
		// animationForPreset path so word/glyph selectors (word_reveal,
		// character_cascade, ...) are transported as text animators instead of
		// being silently compiled away to an empty layer animation. Chronon
		// requires animation objects to carry tracks, so an animator-only
		// motion keeps the animation field absent and rides the layer's
		// text_animators contract.
		presetAnimation, err := animationForPreset(ri.Preset, text, ri.End-ri.Start, ri.Kind == KindImportantPhrase)
		if err != nil {
			return Layer{}, err
		}
		textAnimation = presetAnimation
	}
	if ri.Kind == KindImportantPhrase {
		textAnimation = withPhraseEntryExit(textAnimation, ri.End-ri.Start)
	}
	applyMotionRouting(&layer, textAnimation)
	if layer.Style != nil && layer.Position == nil {
		// position_x/position_y are absolute canvas coordinates of the text
		// centre — the same form the engine reads for text layers.
		posX, hasPosX := ri.Params["position_x"].(float64)
		posY, hasPosY := ri.Params["position_y"].(float64)
		if hasPosX && hasPosY {
			layer.Position = []float64{posX, posY}
		} else {
			layer.Position = resolveTextLayout(src.Width, src.Height)
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

// resolveWatermarkGeometry lives in visual_style_resolver.go — the single
// owner of watermark/subtitle geometry resolution (position AND size).
