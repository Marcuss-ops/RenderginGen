// Package overlay owns the RenderingGen-side boundary between PipelineGen and
// Chronon. RenderingGen is an EXECUTION WORKER: it validates a job, materializes
// its assets, writes plan.json, renders with Chronon and publishes the artifact.
// It does not perform NER, entity linking or editorial ranking.
//
// PipelineGen owns the semantic decisions and emits overlay-plan.v1. The
// worker performs the mechanical lowering through the RenderingGen-owned
// official preset catalog, then continues through one asset/render/artifact
// pipeline. Chronon is only the execution engine.
//
// File layout: compiler.go owns the public contract types and entry points,
// semantic_compile.go the semantic→concrete lowering, subtitle_burn.go the
// ASS subtitle lowering and semantic_helpers.go the preset/motion helpers.
package overlay

import (
	"encoding/json"
	"fmt"
)

// SemanticSchema is PipelineGen's semantic overlay contract. RenderingGen
// compiles it mechanically; PipelineGen remains the owner of its decisions.
const SemanticSchema = "renderinggen.overlay-plan.v1"

// SemanticSchemaVersion is the integer form of the same contract version. It
// is the SINGLE source for the version the worker advertises (`overlay_schema`
// on /health and in the worker registry): advertising a bare integer that no
// consumer could map back to a document schema was an ambiguous identity fact.
// The advertised value is pinned to SemanticSchema by
// schema_version_test.go.
const SemanticSchemaVersion = 1

// Plan is the single concrete Chronon render-plan model. The compiler builds
// it, the processor mutates it mechanically (asset-path normalization,
// subtitle burn-in) and it is marshaled exactly once at the Chronon boundary.
type Plan struct {
	Schema          string           `json:"schema"`
	Version         int              `json:"version"`
	JobID           string           `json:"job_id"`
	Camera          *CameraPlan      `json:"camera,omitempty"`
	CameraAnimation *CameraAnimation `json:"camera_animation,omitempty"`
	Canvas          Canvas           `json:"canvas"`
	Layers          []Layer          `json:"layers"`
	Output          Output           `json:"output"`
}

type CameraPlan struct {
	Type     string     `json:"type"`
	Position [3]float64 `json:"position"`
	Rotation [3]float64 `json:"rotation_deg"`
	FOVDeg   float64    `json:"fov_deg"`
	Near     float64    `json:"near"`
	Far      float64    `json:"far"`
	Zoom     float64    `json:"zoom,omitempty"`
}

type CameraAnimation struct {
	Tracks []CameraTrack `json:"tracks"`
}

type CameraTrack struct {
	Property  string              `json:"property"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
	Easing    string              `json:"easing,omitempty"`
}

type Asset struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
}

func newPlan(jobID string, width, height, fpsNum, fpsDen int, duration int64) *Plan {
	return &Plan{Schema: "chronon.render-plan.v2", Version: 2, JobID: jobID,
		Canvas: Canvas{Width: width, Height: height, FPSNum: fpsNum, FPSDen: fpsDen, DurationFrames: duration},
		Output: Output{Path: "result.mp4", Format: "mp4", Codec: "h264"}}
}

// CompileSemantic lowers the PipelineGen semantic contract to the concrete
// Chronon plan consumed by the worker. It returns the plan, its materialized
// assets and the ledger counters produced by the SAME compile pass, so the
// ledger can never drift from the layers actually emitted.
//
// It resolves RenderingGen's official preset catalog; it does not perform NER,
// entity linking or editorial ranking — those decisions arrive in
// kind/template_id/preset_id/text from PipelineGen.
//
// It returns the typed plan so downstream stages (asset-path normalization,
// subtitle burn, metadata extraction, backend gating) mutate ONE in-memory
// object instead of re-decoding JSON; marshal exactly once at the Chronon
// boundary via Plan.Marshal.
//
// Fail-closed: the compiler is the single owner of the semantic→concrete
// lowering. Byte-for-byte pass-through of an untyped document bypasses
// validation and style resolution, so anything that is not the semantic
// overlay-plan contract is rejected instead of executed.
func CompileSemantic(raw []byte) (CompileResult, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return CompileResult{}, fmt.Errorf("overlay: decode plan: %w", err)
	}
	if probe.SchemaVersion != SemanticSchema {
		return CompileResult{}, fmt.Errorf("overlay: unsupported plan schema %q (semantic %q is the only accepted contract)", probe.SchemaVersion, SemanticSchema)
	}
	plan, assets, stats, unknown, err := compileSemantic(raw)
	if err != nil {
		return CompileResult{}, err
	}
	var metadata struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return CompileResult{}, fmt.Errorf("overlay: decode plan metadata: %w", err)
	}
	prepared, err := buildPreparedPackage(plan, metadata.Language, assets)
	if err != nil {
		return CompileResult{}, err
	}
	return CompileResult{Plan: plan, Assets: assets, Stats: stats, Prepared: prepared, UnknownTemplates: unknown}, nil
}

// Marshal serializes the typed plan once at the Chronon boundary.
func (p *Plan) Marshal() ([]byte, error) {
	clone := *p
	hasV3 := clone.Camera != nil && len(clone.Layers) > 0
	for _, l := range p.Layers {
		if l.Type == "shape" || len(l.Effects) > 0 || l.Shape != nil || l.ScreenSpace ||
			len(l.EffectParamTracks) > 0 || l.Parent != "" || l.TransitionIn != nil || len(l.Masks) > 0 ||
			layerAnimationNeedsV3(l.Animation) {
			hasV3 = true
			break
		}
	}
	if hasV3 {
		clone.Schema = "chronon.render-plan.v3"
		clone.Version = 3
	}
	return json.Marshal(&clone)
}

func layerAnimationNeedsV3(animation *LayerAnimation) bool {
	if animation == nil {
		return false
	}
	for _, track := range animation.Tracks {
		switch track.Property {
		case "stroke_width", "stroke_color", "fill_color", "blur":
			return true
		}
	}
	return false
}

// The struct fields below are the compiler's projection of
// contracts/overlay-plan.v1.schema.json. The two declarations must describe
// EXACTLY the same property set in both directions —
// TestContractSchemaMatchesCompilerStructs fails on any drift — so a producer
// cannot send a field the worker silently drops, and the worker cannot accept
// a field the published contract does not declare (which is what made
// motion_id/motion_params unusable for a schema-validating producer before).
// Fields marked "producer metadata" are accepted for contract completeness and
// intentionally play no part in the lowering.
type semanticPlan struct {
	SchemaVersion   string          `json:"schema_version"`
	PlanID          string          `json:"plan_id"`
	VideoID         string          `json:"video_id"`
	ProjectID       string          `json:"project_id,omitempty"`       // producer metadata
	ScriptName      string          `json:"script_name,omitempty"`      // producer delivery metadata
	Language        string          `json:"language,omitempty"`         // producer delivery metadata
	RendererVersion string          `json:"renderer_version,omitempty"` // producer metadata
	Fingerprint     string          `json:"fingerprint,omitempty"`      // producer metadata
	Source          *semanticSource `json:"source,omitempty"`
	ForegroundScale int             `json:"foreground_scale_percent,omitempty"`
	// SourceFrame is the optional card treatment of the source clip (border
	// frame + drop shadow + rounded corners + on-edge stroke). An unresolvable
	// declaration is a compile error instead of a silently dropped style block.
	SourceFrame *semanticSourceFrame `json:"source_frame,omitempty"`
	Width       int                  `json:"width"`
	Height      int                  `json:"height"`
	FPSNum      int                  `json:"fps_num"`
	FPSDen      int                  `json:"fps_den"`
	// DurationMS is the explicit clip duration. When provided it seeds the
	// canvas duration before items are processed; items can only extend it.
	// Required when items is empty (clip render without entity overlays).
	DurationMS      int64               `json:"duration_ms,omitempty"`
	OutputProfileID string              `json:"output_profile_id"`
	MediaContract   string              `json:"media_contract,omitempty"` // producer media metadata
	Background      *semanticBackground `json:"background,omitempty"`
	Subtitles       *semanticSubtitles  `json:"subtitles,omitempty"`
	Watermark       *semanticWatermark  `json:"watermark,omitempty"`
	Audio           *semanticAudio      `json:"audio,omitempty"`
	Items           []semanticItem      `json:"items"`
}

type semanticSource struct {
	AssetID string `json:"asset_id"`
	Path    string `json:"path,omitempty"`
	SHA256  string `json:"sha256"`
}

// semanticSourceFrame is the producer-owned card declaration of a media layer
// (the source clip and, per item, a video overlay). Both surfaces share one
// declaration shape: `source_frame` at plan level and `frame` on the item.
type semanticSourceFrame struct {
	Border       *semanticFrameBorder `json:"border,omitempty"`
	Shadow       *semanticFrameShadow `json:"shadow,omitempty"`
	Stroke       *semanticFrameStroke `json:"stroke,omitempty"`
	ClipRadiusPX float64              `json:"clip_radius_px,omitempty"`
}

// semanticFrameStroke is an outline painted over the clip perimeter.
type semanticFrameStroke struct {
	WidthPX float64 `json:"width_px"`
	Color   string  `json:"color"`
}

// semanticFrameBorder is the visible frame around the media box.
// WidthPX is the visible thickness in output pixels; RadiusPX is the frame's
// outer corner radius (the media's own inner radius is derived from it).
type semanticFrameBorder struct {
	WidthPX  float64 `json:"width_px"`
	Color    string  `json:"color"`
	RadiusPX float64 `json:"radius_px,omitempty"`
}

// semanticFrameShadow is the drop shadow of the card (the frame when a border
// exists, the media layer itself otherwise).
type semanticFrameShadow struct {
	Color    string  `json:"color"`
	Opacity  float64 `json:"opacity,omitempty"`
	BlurPX   float64 `json:"blur_px,omitempty"`
	OffsetXP float64 `json:"offset_x_px,omitempty"`
	OffsetYP float64 `json:"offset_y_px,omitempty"`
}

type semanticSubtitles struct {
	AssetRefs []SemanticAssetRef `json:"asset_refs,omitempty"`
	StyleID   string             `json:"style_id,omitempty"`
	Mode      string             `json:"mode,omitempty"`
	// Style is the caller's typed visual override. It is REQUIRED for burn
	// mode: the worker never invents subtitle geometry/color/shadow.
	Style map[string]any `json:"style,omitempty"`
}

type semanticWatermark struct {
	Text      string             `json:"text,omitempty"`
	AssetRefs []SemanticAssetRef `json:"asset_refs,omitempty"`
	FontRef   *SemanticAssetRef  `json:"font_ref,omitempty"`
	Position  string             `json:"position,omitempty"`
	Opacity   *float64           `json:"opacity,omitempty"`
	// MarginPX is the requested distance from the canvas edge. Required for
	// text watermarks: the worker never guesses layout.
	MarginPX *int           `json:"margin_px,omitempty"`
	Style    map[string]any `json:"style,omitempty"`
}

type semanticAudio struct {
	Mode       string `json:"mode,omitempty"`
	Codec      string `json:"codec,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}

type semanticBackground struct {
	Kind      string             `json:"kind"`
	Color     []float64          `json:"color,omitempty"`
	AssetRefs []SemanticAssetRef `json:"asset_refs,omitempty"`
	Fit       string             `json:"fit,omitempty"`
	Opacity   *float64           `json:"opacity,omitempty"`
	Loop      bool               `json:"loop,omitempty"`
}

type semanticItem struct {
	ID string `json:"id"`
	// SceneID/EntityID/RenderKey are producer correlation metadata: they are
	// part of the published contract, are accepted, and never influence the
	// lowering.
	SceneID   string `json:"scene_id,omitempty"`
	EntityID  string `json:"entity_id,omitempty"`
	RenderKey string `json:"render_key,omitempty"`
	// Kind is the semantic SSOT of the item. It is authoritative when
	// PipelineGen sends it; when it is absent the template registry supplies
	// it. A kind that contradicts the template's kind is rejected fail-closed
	// (see TemplateSpec.resolveKind).
	Kind string `json:"kind"`
	// Template is optional for a simple text primitive. A non-empty value
	// selects a reusable composition/template; the displayed text remains
	// producer-owned and is never stored in the template catalog.
	Template string `json:"template_id,omitempty"`
	// StyleID is an opaque Chronon-owned text-style identity. RenderingGen
	// validates its shape and transports it; it never maps a style to a font,
	// color or layout default.
	StyleID string `json:"style_id,omitempty"`
	// TemplateSlots are producer-owned content values for a reusable template
	// instance. They are intentionally opaque to RenderingGen.
	TemplateSlots map[string]any `json:"template_slots,omitempty"`
	// PresetID is the semantic preset selected by PipelineGen (the plan's
	// preset_id contract slot). It is preferred over the template mapping.
	PresetID      string         `json:"preset_id"`
	ImagePresetID string         `json:"image_preset_id,omitempty"`
	MotionID      string         `json:"motion_id"`
	MotionParams  map[string]any `json:"motion_params"`
	Text          string         `json:"text"`
	EntityCaption string         `json:"entity_caption,omitempty"`
	// CaptionMotionID is the optional motion override for the entity caption
	// layer. Empty resolves the shared default (text_fade_up); a value that
	// does not target text is refused by the caption lowering instead of
	// lowering image tracks onto a text layer.
	CaptionMotionID string `json:"caption_motion_id,omitempty"`
	StartMS         int64  `json:"start_ms"`
	EndMS           int64  `json:"end_ms"`
	// DurationMS is producer-owned timing metadata. It is validated against
	// end_ms-start_ms at the semantic boundary and is not emitted to Chronon.
	DurationMS *int64 `json:"duration_ms,omitempty"`
	// Frame is the optional card treatment of this item's video overlay.
	Frame       *semanticSourceFrame `json:"frame,omitempty"`
	Params      map[string]any       `json:"params"`
	Style       map[string]any       `json:"style"`
	Assets      []SemanticAssetRef   `json:"asset_refs"`
	ImageLayers []SemanticImageLayer `json:"image_layers"`
	Map         *SemanticMap         `json:"map"`
}

// SemanticMap is the worker mirror of the georeferenced map declaration.
// It is deliberately typed so strict decoding, bounds checks and schema parity
// cannot silently discard map geometry or legal attribution.
type SemanticMap struct {
	Provider      string                 `json:"provider"`
	SourceID      string                 `json:"source_id"`
	SourceLicense string                 `json:"source_license"`
	Center        SemanticMapPoint       `json:"center"`
	Zoom          int                    `json:"zoom"`
	Width         int                    `json:"width"`
	Height        int                    `json:"height"`
	Attribution   string                 `json:"attribution"`
	MotionID      string                 `json:"motion_id"`
	Pins          []SemanticMapPin       `json:"pins"`
	LODs          []SemanticMapLOD       `json:"lods,omitempty"`
	CameraMove    *SemanticMapCameraMove `json:"camera_move,omitempty"`
}

type SemanticMapLOD struct {
	AssetID       string           `json:"asset_id"`
	SourceID      string           `json:"source_id"`
	SourceLicense string           `json:"source_license"`
	Attribution   string           `json:"attribution"`
	Center        SemanticMapPoint `json:"center"`
	Zoom          int              `json:"zoom"`
	Width         int              `json:"width"`
	Height        int              `json:"height"`
}

type SemanticMapCameraMove struct {
	From         SemanticMapPoint `json:"from"`
	To           SemanticMapPoint `json:"to"`
	StartZoom    float64          `json:"start_zoom"`
	EndZoom      float64          `json:"end_zoom"`
	StartTiltDeg float64          `json:"start_tilt_deg"`
	EndTiltDeg   float64          `json:"end_tilt_deg"`
	BearingDeg   float64          `json:"bearing_deg"`
}

type SemanticMapPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type SemanticMapPin struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Color     string  `json:"color"`
	RadiusPX  float64 `json:"radius_px"`
}

// SemanticAssetRef is one content-addressed asset reference of the semantic
// contract (items[].asset_refs[] and watermark.font_ref). It is exported because
// the producer-side writer (internal/renderbatch) transports the very same
// entry: one declaration, so the writer and the worker's decoder cannot describe
// an asset_refs entry with two field sets that drift apart. Its JSON field set
// is pinned to the published contract by TestContractSchemaMatchesCompilerStructs.
type SemanticAssetRef struct {
	ID        string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	MediaType string `json:"media_type"`
}

// SemanticImageLayer is one independently timed and animated source image in a
// composite image item. Assets are declared once on the parent item; each child
// names its asset_id from that declared set.
type SemanticImageLayer struct {
	ID           string               `json:"id"`
	AssetID      string               `json:"asset_id"`
	StartMS      int64                `json:"start_ms"`
	EndMS        int64                `json:"end_ms"`
	PresetID     string               `json:"preset_id"`
	MotionID     string               `json:"motion_id"`
	MotionParams map[string]any       `json:"motion_params"`
	Caption      string               `json:"caption,omitempty"`
	Params       map[string]any       `json:"params"`
	Frame        *semanticSourceFrame `json:"frame,omitempty"`
}

// Audio carries the audio policy in the Chronon render plan so the
// renderer knows whether to copy or transcode the source audio stream.
type Audio struct {
	Mode       string `json:"mode,omitempty"`
	Codec      string `json:"codec,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}

type Canvas struct {
	Width          int   `json:"width"`
	Height         int   `json:"height"`
	FPSNum         int   `json:"fps_num"`
	FPSDen         int   `json:"fps_den"`
	DurationFrames int64 `json:"duration_frames"`
}
type Output struct {
	Path      string `json:"path"`
	Format    string `json:"format"`
	Codec     string `json:"codec"`
	ProfileID string `json:"profile_id,omitempty"`
	// Retained for typed semantic metadata; omitted from Chronon's v2 JSON.
	Audio *Audio `json:"-"`
}

// AnimationTrack is the renderer-neutral motion contract produced by
// RenderingGen after selector/stagger expansion.
type AnimationTrack struct {
	// Property is omitted when empty: layer-level tracks always name their
	// property, but text-selector sub-tracks (start/end/offset/amount) are
	// positionless by contract — chronon.render-plan.v2 closes the object with
	// additionalProperties:false and rejects an empty "property" key.
	Property  string              `json:"property,omitempty"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
	Easing    string              `json:"easing,omitempty"`
}

type AnimationKeyframe struct {
	Frame int64 `json:"frame"`
	Value any   `json:"value"`
}

type Layer struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Asset     string    `json:"asset,omitempty"`
	Source    string    `json:"source,omitempty"`
	Color     []float64 `json:"color,omitempty"`
	Text      string    `json:"text,omitempty"`
	BoxWidth  int       `json:"-"`
	BoxHeight int       `json:"-"`
	// MapRaster dimensions are transient verification metadata. The processor
	// compares the staged PNG's decoded dimensions to this georeferenced size.
	MapRasterWidth  int       `json:"-"`
	MapRasterHeight int       `json:"-"`
	Size            []float64 `json:"size,omitempty"`
	Fit             string    `json:"fit,omitempty"`
	// EntityImage is transient compiler metadata. The processor clears the
	// source's contain bars after assets have been materialized, then serializes
	// only the derived geometry in Size.
	EntityImage bool `json:"-"`
	// EntityCaptionForImageID links a first-class caption to the image whose
	// final source-aspect geometry is resolved after asset materialization.
	EntityCaptionForImageID string          `json:"-"`
	Radius                  float64         `json:"radius,omitempty"`
	Position                []float64       `json:"position,omitempty"`
	Scale                   []float64       `json:"scale,omitempty"`
	Style                   *LayerStyle     `json:"style,omitempty"`
	StartFrame              int64           `json:"start_frame"`
	DurationFrames          int64           `json:"duration_frames"`
	Animation               *LayerAnimation `json:"animation,omitempty"`
	TextAnimators           []TextAnimator  `json:"text_animators,omitempty"`
	// Derived by the lowering pass from concrete Z/rotation tracks. Motion
	// authors never need to duplicate this renderer routing bit by hand.
	Enable3D    bool `json:"enable_3d,omitempty"`
	ScreenSpace bool `json:"screen_space,omitempty"`
	// Opacity is preserved as a pointer so a LOD layer can explicitly fade out.
	// Opacity is a POINTER so an explicit 0 survives the wire. With a plain
	// float64 + omitempty the contract's "opacity 0 = invisible" collapsed
	// into "key absent", and the renderer's decoder then treated the layer as
	// fully opaque: the one value the contract documents as special was the
	// one value that could not be expressed. Nil means "not declared" (the
	// renderer default applies).
	Opacity           *float64                `json:"opacity,omitempty"`
	Loop              bool                    `json:"loop,omitempty"`
	Light             *LayerLight             `json:"light,omitempty"`
	Shape             *LayerShape             `json:"shape,omitempty"`
	Effects           []LayerEffect           `json:"effects,omitempty"`
	EffectParamTracks []LayerEffectParamTrack `json:"effect_param_tracks,omitempty"`
	Parent            string                  `json:"parent,omitempty"`
	TransitionIn      *LayerTransition        `json:"transition_in,omitempty"`
	Masks             []LayerMask             `json:"masks,omitempty"`
	// These transient relations keep premium frame geometry aligned when the
	// processor resolves an entity image's final source-aspect size.
	PremiumParentImageID  string    `json:"-"`
	PremiumParentSize     []float64 `json:"-"`
	PremiumSizeScale      []float64 `json:"-"`
	PremiumPositionOffset []float64 `json:"-"`
	PremiumRadiusScale    float64   `json:"-"`
	PremiumZOffset        float64   `json:"-"`
	PremiumShapeKind      string    `json:"-"`
	PremiumPathKind       string    `json:"-"`
	PremiumCanvasSize     bool      `json:"-"`
	PremiumSyncTransform  bool      `json:"-"`
	PremiumWipeMask       bool      `json:"-"`
	// FrameStroke is transient compiler metadata expanded to a native shape
	// layer after the media layer is compiled; it never crosses the Chronon wire.
	FrameStroke *LayerStroke `json:"-"`
}

type LayerShape struct {
	Type            string              `json:"type"`
	Fill            any                 `json:"fill,omitempty"`
	Radius          float64             `json:"radius,omitempty"`
	CornerRadius    []float64           `json:"corner_radius,omitempty"`
	Stroke          *LayerStroke        `json:"stroke,omitempty"`
	Gradient        *LayerGradient      `json:"-"`
	Points          int                 `json:"points,omitempty"`
	InnerRadius     float64             `json:"inner_radius_ratio,omitempty"`
	RotationDeg     float64             `json:"rotation_degrees,omitempty"`
	ArrowDirection  string              `json:"-"`
	ArrowHeadLength float64             `json:"-"`
	ArrowHeadWidth  float64             `json:"-"`
	ArrowShaftWidth float64             `json:"-"`
	GridSpacing     float64             `json:"spacing,omitempty"`
	GridLineWidth   float64             `json:"-"`
	DotRadius       float64             `json:"dot_radius,omitempty"`
	ArcStartDeg     float64             `json:"start_degrees,omitempty"`
	ArcSweepDeg     float64             `json:"sweep_degrees,omitempty"`
	ArcThickness    float64             `json:"-"`
	WorldMap        *LayerWorldMap      `json:"world_map,omitempty"`
	Chart           *LayerChart         `json:"chart,omitempty"`
	DeviceFrame     string              `json:"-"`
	Device          string              `json:"device,omitempty"`
	Chrome          *bool               `json:"chrome,omitempty"`
	Path            []LayerPathCommand  `json:"path,omitempty"`
	Operators       []LayerPathOperator `json:"operators,omitempty"`
}

type LayerPathOperator struct {
	Kind   string          `json:"kind"`
	Params LayerTrimParams `json:"params"`
}

type LayerPathAnimation struct {
	Easing    string              `json:"easing,omitempty"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
}

type LayerTrimParams struct {
	Start     float64             `json:"start"`
	End       float64             `json:"end"`
	Animation *LayerPathAnimation `json:"animation,omitempty"`
}

type LayerEffectParamTrack struct {
	EffectID  string              `json:"effect_id"`
	Param     string              `json:"param"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
	Easing    string              `json:"easing,omitempty"`
}

type LayerTransition struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	DurationFrames int64          `json:"duration_frames"`
	Direction      string         `json:"direction,omitempty"`
	Easing         string         `json:"easing,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
}

type LayerMask struct {
	Type       string              `json:"type"`
	Mode       string              `json:"mode"`
	Position   []float64           `json:"position,omitempty"`
	Size       []float64           `json:"size,omitempty"`
	Radius     float64             `json:"radius,omitempty"`
	Feather    float64             `json:"feather,omitempty"`
	Opacity    float64             `json:"opacity,omitempty"`
	Path       []LayerPathCommand  `json:"path,omitempty"`
	TargetPath []LayerPathCommand  `json:"target_path,omitempty"`
	Animation  *LayerPathAnimation `json:"animation,omitempty"`
	// Field carries the Field2D recipe of a paint_v1 field mask. It maps to
	// Chronon's native mask type "field" (MaskType::Field2D), which samples
	// the deterministic Field2D generator directly — no second mask engine.
	Field map[string]any `json:"field,omitempty"`
	// ExpansionTrack/OpacityTrack carry the paint reveal on a field mask:
	// the renderer animates the mask threshold through expansion (negative
	// growth) and the overall coverage through opacity. Field masks reject a
	// morph `animation` by contract, so the reveal travels on these tracks.
	ExpansionTrack *LayerMaskParameterTrack `json:"expansion_track,omitempty"`
	OpacityTrack   *LayerMaskParameterTrack `json:"opacity_track,omitempty"`
}

// LayerMaskParameterTrack is one scalar mask parameter animation. Values are
// layer-relative frames like every other track.
type LayerMaskParameterTrack struct {
	Property  string              `json:"property"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
	Easing    string              `json:"easing,omitempty"`
}

type LayerChart struct {
	ChartType string      `json:"chart_type,omitempty"`
	Data      []float64   `json:"data,omitempty"`
	Colors    [][]float64 `json:"colors,omitempty"`
}

type LayerGradient struct {
	Type         string              `json:"type"`
	Stops        [][2]any            `json:"-"`
	ColorStops   []LayerGradientStop `json:"color_stops,omitempty"`
	OpacityStops []LayerOpacityStop  `json:"opacity_stops,omitempty"`
	Start        []float64           `json:"start,omitempty"`
	End          []float64           `json:"end,omitempty"`
}

func (g LayerGradient) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type         string              `json:"type"`
		ColorStops   []LayerGradientStop `json:"color_stops"`
		OpacityStops []LayerOpacityStop  `json:"opacity_stops,omitempty"`
		Start        []float64           `json:"start,omitempty"`
		End          []float64           `json:"end,omitempty"`
	}
	return json.Marshal(wire{Type: g.Type, ColorStops: g.ColorStops, OpacityStops: g.OpacityStops, Start: g.Start, End: g.End})
}

type LayerGradientStop struct {
	Position float64   `json:"position"`
	Color    []float64 `json:"color"`
}

type LayerOpacityStop struct {
	Position float64 `json:"position"`
	Opacity  float64 `json:"opacity"`
}

type LayerWorldMap struct {
	Projection     string      `json:"projection,omitempty"`
	Center         []float64   `json:"center,omitempty"`
	Zoom           float64     `json:"zoom,omitempty"`
	Highlight      []string    `json:"highlight,omitempty"`
	HighlightColor string      `json:"highlight_color,omitempty"`
	Route          [][]float64 `json:"route,omitempty"`
	Dots           bool        `json:"dots,omitempty"`
}

type LayerPathCommand struct {
	Type     string    `json:"type"`
	Point    []float64 `json:"point,omitempty"`
	Points   []float64 `json:"points,omitempty"`
	Control1 []float64 `json:"control1,omitempty"`
	Control2 []float64 `json:"control2,omitempty"`
}

type LayerEffect struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params,omitempty"`
}

func (e LayerEffect) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(e.Params)+1)
	for k, v := range e.Params {
		m[k] = v
	}
	m["type"] = e.Type
	return json.Marshal(m)
}

func (e *LayerEffect) UnmarshalJSON(b []byte) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if t, ok := m["type"].(string); ok {
		e.Type = t
	}
	delete(m, "type")
	e.Params = m
	return nil
}

var (
	_ json.Marshaler   = (*LayerEffect)(nil)
	_ json.Unmarshaler = (*LayerEffect)(nil)
)

type LayerLight struct {
	Radius    float64 `json:"radius"`
	Color     string  `json:"color"`
	Intensity float64 `json:"intensity"`
}

type LayerStyle struct {
	Font        string           `json:"font,omitempty"`
	FontSize    float64          `json:"font_size,omitempty"`
	Fill        string           `json:"fill,omitempty"`
	FitMode     string           `json:"fit_mode,omitempty"`
	MinFontSize float64          `json:"min_font_size,omitempty"`
	MaxFontSize float64          `json:"max_font_size,omitempty"`
	Stroke      *LayerStroke     `json:"stroke,omitempty"`
	Shadow      *LayerShadow     `json:"shadow,omitempty"`
	Glow        *LayerGlow       `json:"glow,omitempty"`
	Background  *LayerBackground `json:"background,omitempty"`
}

// LayerBackground is the rounded, padded plate the renderer paints behind a
// layer. For a media layer it is the card the frame contract lowers to: the
// padding IS the visible border thickness and the radius IS the frame's outer
// corner radius. Text layers already use the same field for their text card.
type LayerBackground struct {
	Color   string    `json:"color"`
	Opacity *float64  `json:"opacity,omitempty"`
	Radius  float64   `json:"radius,omitempty"`
	Padding []float64 `json:"padding,omitempty"`
}

func floatPointer(value float64) *float64 { return &value }

type LayerStroke struct {
	Color string  `json:"color,omitempty"`
	Width float64 `json:"width,omitempty"`
}
type TextSelector struct {
	ID             string          `json:"id,omitempty"`
	Unit           string          `json:"unit,omitempty"`
	Shape          string          `json:"shape,omitempty"`
	Order          string          `json:"order,omitempty"`
	Combine        string          `json:"combine,omitempty"`
	Start          *AnimationTrack `json:"start,omitempty"`
	End            *AnimationTrack `json:"end,omitempty"`
	Offset         *AnimationTrack `json:"offset,omitempty"`
	Amount         *AnimationTrack `json:"amount,omitempty"`
	ExcludeSpaces  bool            `json:"exclude_spaces,omitempty"`
	RandomizeOrder bool            `json:"randomize_order,omitempty"`
	RandomSeed     uint64          `json:"random_seed,omitempty"`
}
type TextAnimator struct {
	ID         string           `json:"id,omitempty"`
	Selectors  []TextSelector   `json:"selectors"`
	Properties []AnimationTrack `json:"properties"`
}
type LayerShadow struct {
	Color   string    `json:"color,omitempty"`
	Opacity float64   `json:"opacity,omitempty"`
	Blur    float64   `json:"blur,omitempty"`
	Offset  []float64 `json:"offset,omitempty"`
}
type LayerGlow struct {
	Radius    float64 `json:"radius"`
	Intensity float64 `json:"intensity"`
	Color     string  `json:"color"`
}
type LayerAnimation struct {
	Tracks        []AnimationTrack `json:"tracks,omitempty"`
	TextAnimators []TextAnimator   `json:"-"`
}

// SubtitleStyleBox is the caller-owned subtitle safe-area box resolved from
// the plan's typed style block.
type SubtitleStyleBox struct {
	Width  int
	Height int
	X      int
	Y      int
}
