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
	"math"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
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
	if err := resolveAnimationPolicies(&src); err != nil {
		return nil, nil, Stats{}, nil, err
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
		// Card treatment of the clip (border frame + drop shadow + perimeter
		// stroke). The border is lowered behind the source layer; the stroke gets
		// its own transparent shape above the media box. A declaration the worker
		// cannot honour is a compile error (see applyFrameTreatment), never dropped.
		if err := applyFrameTreatment(&srcLayer, src.SourceFrame); err != nil {
			return nil, nil, Stats{}, nil, err
		}
		sourceLayerIndex = len(plan.Layers)
		plan.Layers = append(plan.Layers, srcLayer)
		if srcLayer.FrameStroke != nil {
			strokeLayer, err := compileMediaStrokeLayer(srcLayer, src.Width, src.Height)
			if err != nil {
				return nil, nil, Stats{}, nil, fmt.Errorf("overlay: source_frame: %w", err)
			}
			plan.Layers = append(plan.Layers, strokeLayer)
		}
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
	var entityCameraMotionID string
	var entityCameraMotionItemID string
	for _, ri := range resolved {
		if isEntityKind(ri.Kind) && ri.Item.EntityStyleID != "" {
			if style, ok := ResolveEntityStyle(ri.Item.EntityStyleID, src.PlanID, src.VideoID, ri.Item.ID); ok && style.CameraMotionID != "" {
				if entityCameraMotionID != "" {
					return nil, nil, Stats{}, nil, fmt.Errorf("overlay: camera motion on entity item %q conflicts with camera motion on entity item %q; only one scene camera controller is allowed per plan", ri.Item.ID, entityCameraMotionItemID)
				}
				entityCameraMotionID = style.CameraMotionID
				entityCameraMotionItemID = ri.Item.ID
			}
		}
		if isEntityKind(ri.Kind) && ri.Item.EntityStyleID != "" && cameraMove != nil {
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: entity item %q camera motion conflicts with map item %q scene camera move", ri.Item.ID, cameraMapItemID)
		}
		if ri.Item.Map != nil && ri.Item.Map.CameraMove != nil && entityCameraMotionID != "" {
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: map item %q scene camera move conflicts with entity item %q camera motion", ri.Item.ID, entityCameraMotionItemID)
		}
		if ri.Item.Map != nil && ri.Item.Map.CameraMove != nil && cameraMove != nil {
			return nil, nil, Stats{}, nil, fmt.Errorf("overlay: map item %q camera move conflicts with map item %q; only one scene camera controller is allowed per plan", ri.Item.ID, cameraMapItemID)
		}
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
		for _, layer := range layers {
			// A premium image motion can expand one item into several linked
			// layers. Attach a clip stroke to the actual image layer only.
			plan.Layers = append(plan.Layers, layer)
			if layer.FrameStroke != nil && layer.Type != "shape" {
				strokeLayer, err := compileMediaStrokeLayer(layer, src.Width, src.Height)
				if err != nil {
					return nil, nil, Stats{}, nil, fmt.Errorf("overlay: item %q: %w", ri.Item.ID, err)
				}
				plan.Layers = append(plan.Layers, strokeLayer)
			}
		}
		// Stroke shapes require render-plan.v3 even though the semantic item
		// itself is a media layer.
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
	} else if entityCameraMotionID != "" && plan.Camera == nil {
		cx := float64(src.Width) / 2
		cy := float64(src.Height) / 2
		plan.Camera = &CameraPlan{
			Type:     "perspective",
			Position: [3]float64{cx, cy, -1400.0},
			Rotation: [3]float64{0, 0, 0},
			FOVDeg:   55.0,
			Near:     1.0,
			Far:      5000.0,
			Zoom:     1.0,
		}
		var tracks []CameraTrack
		switch entityCameraMotionID {
		case "camera_dolly_push":
			tracks = []CameraTrack{
				{
					Property: "camera_position_z",
					Easing:   "out_cubic",
					Keyframes: []AnimationKeyframe{
						{Frame: 0, Value: -1700.0},
						{Frame: 65, Value: -1400.0},
						{Frame: 89, Value: -1400.0},
					},
				},
			}
		case "camera_orbit_yaw":
			tracks = []CameraTrack{
				{
					Property: "camera_rotation_y",
					Easing:   "out_cubic",
					Keyframes: []AnimationKeyframe{
						{Frame: 0, Value: 12.0},
						{Frame: 55, Value: 0.0},
						{Frame: 89, Value: 0.0},
					},
				},
				{
					Property: "camera_position_x",
					Easing:   "out_cubic",
					Keyframes: []AnimationKeyframe{
						{Frame: 0, Value: cx + 160.0},
						{Frame: 55, Value: cx},
						{Frame: 89, Value: cx},
					},
				},
			}
		case "camera_crane_rise":
			tracks = []CameraTrack{
				{
					Property: "camera_position_y",
					Easing:   "out_cubic",
					Keyframes: []AnimationKeyframe{
						{Frame: 0, Value: cy + 120.0},
						{Frame: 55, Value: cy},
						{Frame: 89, Value: cy},
					},
				},
				{
					Property: "camera_rotation_x",
					Easing:   "out_cubic",
					Keyframes: []AnimationKeyframe{
						{Frame: 0, Value: -4.0},
						{Frame: 55, Value: 0.0},
						{Frame: 89, Value: 0.0},
					},
				},
			}
		case "camera_dutch_spatial":
			tracks = []CameraTrack{
				{
					Property: "camera_rotation_z",
					Easing:   "out_cubic",
					Keyframes: []AnimationKeyframe{
						{Frame: 0, Value: -3.5},
						{Frame: 50, Value: 0.0},
						{Frame: 89, Value: 0.0},
					},
				},
				{
					Property: "camera_position_z",
					Easing:   "out_cubic",
					Keyframes: []AnimationKeyframe{
						{Frame: 0, Value: -1560.0},
						{Frame: 50, Value: -1400.0},
						{Frame: 89, Value: -1400.0},
					},
				},
			}
		case "camera_flyby_parallax":
			tracks = []CameraTrack{
				{
					Property: "camera_position_x",
					Easing:   "out_cubic",
					Keyframes: []AnimationKeyframe{
						{Frame: 0, Value: cx - 110.0},
						{Frame: 60, Value: cx},
						{Frame: 89, Value: cx},
					},
				},
				{
					Property: "camera_rotation_y",
					Easing:   "out_cubic",
					Keyframes: []AnimationKeyframe{
						{Frame: 0, Value: -8.5},
						{Frame: 60, Value: 0.0},
						{Frame: 89, Value: 0.0},
					},
				},
				{
					Property: "camera_position_z",
					Easing:   "out_cubic",
					Keyframes: []AnimationKeyframe{
						{Frame: 0, Value: -1600.0},
						{Frame: 60, Value: -1400.0},
						{Frame: 89, Value: -1400.0},
					},
				},
			}
		}
		if len(tracks) > 0 {
			plan.CameraAnimation = &CameraAnimation{Tracks: tracks}
		}
	}

	// Patch background duration to match the final canvas duration.
	if len(plan.Layers) > 0 && plan.Layers[0].ID == "background" {
		plan.Layers[0].DurationFrames = plan.Canvas.DurationFrames
	}
	// Patch source layer duration — it spans the full clip.
	if sourceLayerIndex >= 0 {
		plan.Layers[sourceLayerIndex].DurationFrames = plan.Canvas.DurationFrames
		if sourceLayerIndex+1 < len(plan.Layers) && plan.Layers[sourceLayerIndex+1].ID == "source__stroke" {
			plan.Layers[sourceLayerIndex+1].DurationFrames = plan.Canvas.DurationFrames
		}
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

	plan.Schema, plan.Version = RenderPlanWireVersion(&plan)

	// Stable asset order (the registry sorts) keeps prepared-plan
	// fingerprints reproducible. The plan stays typed; the caller marshals it
	// exactly once at the Chronon boundary.
	return &plan, registry.Assets(), stats, unknown, nil
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
		if item.EntityStyleID != "" {
			if _, ok := ResolveEntityStyle(item.EntityStyleID, src.PlanID, src.VideoID, item.ID); !ok {
				return nil, fmt.Errorf("overlay: item %q has unsupported entity_style_id %q", item.ID, item.EntityStyleID)
			}
			if kind != KindEntityCard {
				return nil, fmt.Errorf("overlay: item %q entity_style_id is only valid for entity_card items", item.ID)
			}
			if len(item.Assets) == 0 || strings.TrimSpace(item.EntityCaption) == "" {
				return nil, fmt.Errorf("overlay: item %q entity_style_id %q requires an image and entity_caption", item.ID, item.EntityStyleID)
			}
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
		ri := resolvedItem{Item: item, Spec: spec, Kind: kind, Params: params, Start: start, End: end}

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
		if err := validateTextRuntimeOverrides(params, item.ID, kind, preset, len(item.Assets) > 0, strings.TrimSpace(item.EntityCaption) != ""); err != nil {
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
	case isEntityKind(ri.Kind) && len(ri.Item.ImageLayers) > 0:
		return compileImageItem(ri, src, registry)
	case isEntityKind(ri.Kind) && len(ri.Item.Assets) > 0:
		return compileEntityCard(ri, src, registry)
	case isVideoKind(ri.Kind):
		layer, err := compileVideoOverlayLayer(ri, src, registry)
		if err != nil {
			return nil, err
		}
		return []Layer{layer}, nil
	case isImageKind(ri.Kind):
		return compileImageItem(ri, src, registry)
	case isShapeKind(ri.Kind):
		layer, err := resolveShape(ri, src)
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
		return compileTextVisualAccentLayers(ri, src, layer)
	}
}

// Brush motions target text as well as images. Their recipe components must be
// emitted beside the phrase layer; the ordinary text motion lowering only
// consumes tracks and would otherwise silently omit the authored brush stroke.
func compileTextVisualAccentLayers(ri resolvedItem, src *semanticPlan, text Layer) ([]Layer, error) {
	if strings.HasPrefix(ri.Item.MotionID, "typewriter_modern_") && text.Style != nil {
		// These date/number animations mirror the modern Short Phrases previews.
		// Respect a producer's explicit font override, but otherwise use the
		// bundled Bricolage face rather than the generic phrase preset default.
		if _, explicit := ri.Params["font_family"]; !explicit {
			// Resolve the internal modern-typewriter default through the same
			// font registry as explicit family overrides; keep its asset path
			// out of this lowering branch.
			text.Style.Font, _ = runtimeFontPath("bricolage_grotesque")
		}
	}
	definition, err := visualAccentsDefinition(ri.Item.MotionID)
	if err != nil {
		return nil, err
	}
	var layers []Layer
	if definition == nil || definition.Category != "brush_v1" {
		layers = []Layer{text}
	} else {
		targetsText := false
		for _, target := range definition.Targets {
			targetsText = targetsText || target == "text"
		}
		if !targetsText {
			return nil, fmt.Errorf("overlay: brush motion %q does not target text", definition.ID)
		}
		// Brush paths for phrases use the visible text block as their local
		// coordinate system. Phrase text layers span the full canvas so long
		// copy can wrap; using that entire box for a circle or underline makes
		// short phrases look lost and long phrases run edge-to-edge.
		textAnchor := brushTextAnchor(text, src)
		textDefinition := *definition
		if definition.ImageRecipe != nil {
			recipe := *definition.ImageRecipe
			recipe.Components = append([]motion.ImageMotionComponent(nil), definition.ImageRecipe.Components...)
			for i := range recipe.Components {
				switch recipe.Components[i].PathKind {
				case "line":
					if definition.ID != "brush_marker_highlight" {
						recipe.Components[i].PathKind = "underline"
					}
				case "double_line":
					recipe.Components[i].PathKind = "underline_double"
				case "wave":
					recipe.Components[i].PathKind = "underline_wave"
				}
			}
			textDefinition.ImageRecipe = &recipe
		}
		layers, err = compileVisualAccentsComponents(src, textAnchor, textDefinition)
		if err != nil {
			return nil, err
		}
		for i := range layers {
			if layers[i].ID == textAnchor.ID {
				layers[i] = text
				break
			}
		}
	}
	if cursor, ok := compileTypewriterCursor(ri, src, text); ok {
		layers = append(layers, cursor)
	}
	return layers, nil
}

func brushTextAnchor(text Layer, src *semanticPlan) Layer {
	anchor := text
	fontSize := 72.0
	if text.Style != nil && text.Style.FontSize > 0 {
		fontSize = text.Style.FontSize
	}
	maxUnits := 0.0
	lineCount := 0
	for _, line := range strings.Split(text.Text, "\n") {
		lineCount++
		units := 0.0
		for _, r := range line {
			switch {
			case r == ' ':
				units += 0.31
			case strings.ContainsRune("ilI.,:;!'|", r):
				units += 0.30
			case strings.ContainsRune("MW@%&", r):
				units += 0.83
			case r >= '0' && r <= '9':
				units += 0.56
			default:
				units += 0.55
			}
		}
		if units > maxUnits {
			maxUnits = units
		}
	}
	width := maxUnits*fontSize + fontSize*1.05
	minWidth := fontSize * 3
	maxWidth := float64(src.Width - 96)
	if width < minWidth {
		width = minWidth
	}
	if width > maxWidth {
		width = maxWidth
	}
	height := math.Max(fontSize*1.5, float64(lineCount)*fontSize*1.3+16)
	maxHeight := float64(src.Height - 96)
	if height > maxHeight {
		height = maxHeight
	}
	anchor.Size = []float64{width, height}
	return anchor
}

// compileEntityCard lowers a portrait and its optional entity caption. The
// image preset owns the portrait geometry and motion; position_x/position_y
// can move the portrait so caption_layout can use the opposite side.
func compileEntityCard(ri resolvedItem, src *semanticPlan, registry *assetRegistry) ([]Layer, error) {
	var currentStyle entityStyleVariant
	var hasStyle bool
	if ri.Item.EntityStyleID != "" {
		imageMotionOverride := ri.Item.MotionID
		captionMotionOverride := ri.Item.CaptionMotionID
		captionMotionParams := ri.Item.CaptionMotionParams
		style, ok := ResolveEntityStyle(ri.Item.EntityStyleID, src.PlanID, src.VideoID, ri.Item.ID)
		if !ok {
			style = selectRandomEntityStyle(src.PlanID, src.VideoID, ri.Item.ID)
		}
		currentStyle = style
		hasStyle = true
		ri.Item.MotionID = style.ImageMotionID
		if imageMotionOverride != "" {
			ri.Item.MotionID = imageMotionOverride
		}
		ri.Item.CaptionMotionID = style.CaptionMotionID
		if captionMotionOverride != "" {
			ri.Item.CaptionMotionID = captionMotionOverride
		}
		ri.Item.CaptionMotionParams = captionMotionParams
		ri.Item.CaptionLayout = style.CaptionLayout
		ri.Item.CaptionFontFamily = style.CaptionFontFamily
		ri.Item.CaptionColor = style.CaptionColor
		// Place the runtime portrait in one of two balanced columns, or center it
		// for below-caption layouts.
		x := 0.0
		switch style.ImageSide {
		case "left":
			x = -float64(src.Width) * 0.25
		case "right":
			x = float64(src.Width) * 0.25
		case "center", "below":
			x = 0.0
		default:
			return nil, fmt.Errorf("overlay: entity style %q has unsupported image side %q", style.Name, style.ImageSide)
		}
		ri.Params["position_x"] = x
		ri.Params["position_y"] = 0.0
	}
	img := imageLayer(ri, registry.Path(ri.Item.Assets[0].ID))
	if hasStyle && currentStyle.CameraMotionID != "" {
		img.Enable3D = true
	}
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
		// Keep the source portrait uncropped; an explicit position lets the
		// producer place it on either side while the preset retains its size.
		img.Position = []float64{0, 0}
		img.Fit = FitContain
	}
	if x, ok := numericValue(ri.Params["position_x"]); ok {
		y, _ := numericValue(ri.Params["position_y"])
		img.Position = []float64{x, y}
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
		captionItem := ri.Item
		captionLayer, err := compileEntityCaptionLayer(ri, src, captionItem, caption, &layers[imageIndex], "entity")
		if err != nil {
			return nil, err
		}
		if premium != nil && len(premium.ImageRecipe.CaptionTracks) > 0 {
			premiumLayerActive(&captionLayer, premiumTracks(*premium, premium.ImageRecipe.CaptionTracks, captionLayer.DurationFrames))
		}
		if hasStyle && currentStyle.IsBadge && currentStyle.BadgeColor != "" {
			badgeWidth := captionLayer.Size[0] + 56.0
			if badgeWidth < 300.0 {
				badgeWidth = 300.0
			}
			badgeHeight := captionLayer.Size[1] + 16.0
			if badgeHeight < 64.0 {
				badgeHeight = 64.0
			}
			badgeLayer := Layer{
				ID:             ri.Item.ID + ":entity_badge",
				Type:           "shape",
				Position:       []float64{captionLayer.Position[0], captionLayer.Position[1]},
				Size:           []float64{badgeWidth, badgeHeight},
				StartFrame:     captionLayer.StartFrame,
				DurationFrames: captionLayer.DurationFrames,
				Shape: &LayerShape{
					Type:         "rect",
					Fill:         currentStyle.BadgeColor,
					CornerRadius: []float64{24, 24, 24, 24},
				},
			}
			if captionLayer.Animation != nil {
				badgeLayer.Animation = captionLayer.Animation
			}
			layers = append(layers, badgeLayer)
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
		percent, ok := numericValue(raw)
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

const phraseWrapCols = 25
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
