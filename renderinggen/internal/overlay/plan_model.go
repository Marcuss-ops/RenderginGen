package overlay

import (
	"encoding/json"
)

// plan_model.go owns the concrete Chronon render-plan model: the object the
// compiler builds, the processor mutates mechanically and the serializer writes
// exactly once at the Chronon boundary. It is declarations plus their JSON
// codecs only — the lowering lives in compiler.go and the semantic_*_compile.go
// files, and no logic here may depend on them.

// Plan is the single concrete Chronon render-plan model. The compiler builds
// it, the processor mutates it mechanically (asset-path normalization,
// subtitle burn-in) and it is marshaled exactly once at the Chronon boundary.
type Plan struct {
	Schema        string         `json:"schema"`
	Version       int            `json:"version"`
	JobID         string         `json:"job_id"`
	Camera        *CameraPlan    `json:"camera,omitempty"`
	CameraSamples []CameraSample `json:"camera_samples,omitempty"`
	// CameraAnimation is a transient compiler input. It is sampled before the
	// concrete plan is serialized and must never cross the Chronon wire.
	CameraAnimation *CameraAnimation `json:"-"`
	Canvas          Canvas           `json:"canvas"`
	Layers          []Layer          `json:"layers"`
	Audio           *AudioMixPlan    `json:"audio,omitempty"`
	Output          Output           `json:"output"`
	// ItemWindows is the compile pass's per-semantic-item frame windows
	// ([StartFrame, EndFrame) in canvas frames), in plan order. It is a
	// TRANSIENT compiler output — like CameraAnimation it never crosses the
	// Chronon wire (json:"-") — consumed by the worker's P4 segmented-output
	// planner (one MP4 per item cut from one encoder session). Populated from
	// the same resolvedItem pass that lowers the layers, so the windows can
	// never drift from the items that were actually compiled.
	ItemWindows []ItemWindow `json:"-"`
}

// ItemWindow is one semantic item's frame window in canvas frames (half-open,
// [StartFrame, EndFrame)).
type ItemWindow struct {
	ItemID     string
	StartFrame int64
	EndFrame   int64
}

type CameraPlan struct {
	Type string `json:"type"`
	// Node is the AE rig: one_node aims by rotation only, two_node locks
	// aim on Target (point of interest). Omitted means one_node.
	Node       string      `json:"node,omitempty"`
	Position   [3]float64  `json:"position"`
	Rotation   [3]float64  `json:"rotation_deg"`
	Target     *[3]float64 `json:"target,omitempty"`
	TargetName string      `json:"target_name,omitempty"`
	FOVDeg     float64     `json:"fov_deg"`
	Near       float64     `json:"near"`
	Far        float64     `json:"far"`
	Zoom       float64     `json:"zoom,omitempty"`
	// FocalMM is the physical lens in mm on the 36x24 gate (AE 24/28/35/
	// 50/85 family). Zero means "not authored": FOVDeg wins.
	FocalMM float64 `json:"focal_length_mm,omitempty"`
	// FStop is the physical aperture as f/N. Zero means "not authored".
	FStop float64 `json:"f_stop,omitempty"`
	// IrisBlades shapes bokeh. Zero means circular.
	IrisBlades int        `json:"iris_blades,omitempty"`
	DOF        *CameraDOF `json:"dof,omitempty"`
}

// CameraDOF is the wire depth-of-field block. Enabled selects the legacy
// blur-per-unit path; an authored FStop additionally selects the physical
// lens model in the renderer.
type CameraDOF struct {
	Enabled       bool    `json:"enabled"`
	FocusDistance float64 `json:"focus_distance,omitempty"`
	Aperture      float64 `json:"aperture,omitempty"`
	MaxBlur       float64 `json:"max_blur,omitempty"`
	FStop         float64 `json:"f_stop,omitempty"`
	IrisBlades    int     `json:"iris_blades,omitempty"`
	HighlightGain float64 `json:"highlight_gain,omitempty"`
}

type CameraAnimation struct {
	Tracks []CameraTrack `json:"tracks"`
}

type CameraSample struct {
	Frame  float64    `json:"frame"`
	Camera CameraPlan `json:"camera"`
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

// Audio carries the audio policy in the Chronon render plan so the
// renderer knows whether to copy or transcode the source audio stream.
type Audio struct {
	Mode       string `json:"mode,omitempty"`
	Codec      string `json:"codec,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}

// AudioMixPlan is the concrete Chronon audio bus emitted for image-bound SFX.
// Optional/legacy overlay plans omit it and preserve their existing audio path.
type AudioMixPlan struct {
	Clips []AudioClipPlan `json:"clips"`
}

type AudioClipPlan struct {
	ID      string  `json:"id"`
	Asset   string  `json:"asset"`
	Start   int64   `json:"start"`
	ClipIn  int64   `json:"clip_in,omitempty"`
	ClipOut int64   `json:"clip_out,omitempty"`
	GainDB  float64 `json:"gain_db"`
	Role    string  `json:"role"`
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
	// ColorSpace is the concrete Chronon output colour contract. Canonical
	// profiles declare rec709 explicitly so the renderer cannot fall back to
	// its sRGB default.
	ColorSpace string `json:"color_space,omitempty"`
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
	// Component identifies a catalog-owned shape/text component track and is
	// transient compiler metadata; the Chronon wire remains just the track.
	Component string `json:"-"`
}

type AnimationKeyframe struct {
	Frame int64 `json:"frame"`
	Value any   `json:"value"`
}

type Layer struct {
	ID        string           `json:"id"`
	Type      string           `json:"type"`
	Asset     string           `json:"asset,omitempty"`
	Source    string           `json:"source,omitempty"`
	Color     []float64        `json:"color,omitempty"`
	Text      string           `json:"text,omitempty"`
	TextSpans []map[string]any `json:"spans,omitempty"`
	BoxWidth  int              `json:"-"`
	BoxHeight int              `json:"-"`
	// DeriveWidthFromAsset marks a standalone image layer that authored a fixed
	// height but no width: the processor decodes the materialized asset and
	// derives the card width from its aspect before the plan reaches Chronon.
	// A guessed box here would distort the photo.
	DeriveWidthFromAsset bool `json:"-"`
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
	// CaptionForImageID links a first-class caption to the image whose final
	// source-aspect geometry is resolved after asset materialization.
	CaptionForImageID string `json:"-"`
	CaptionLayout     string `json:"-"`
	// ChainPanOffset is transient compiler metadata for a chain camera-pan
	// strip: the canvas-space translation the group track applies to this
	// layer. The processor subtracts it before fitting a linked caption and
	// adds it back, so media materialization cannot collapse the strip.
	ChainPanOffset []float64       `json:"-"`
	Radius         float64         `json:"radius,omitempty"`
	Position       []float64       `json:"position,omitempty"`
	Scale          []float64       `json:"scale,omitempty"`
	Style          *LayerStyle     `json:"style,omitempty"`
	StartFrame     int64           `json:"start_frame"`
	DurationFrames int64           `json:"duration_frames"`
	Animation      *LayerAnimation `json:"animation,omitempty"`
	TextAnimators  []TextAnimator  `json:"text_animators,omitempty"`
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
	PremiumParentImageID    string    `json:"-"`
	PremiumParentSize       []float64 `json:"-"`
	PremiumSizeScale        []float64 `json:"-"`
	PremiumPositionOffset   []float64 `json:"-"`
	PremiumRadiusScale      float64   `json:"-"`
	PremiumZOffset          float64   `json:"-"`
	PremiumShapeKind        string    `json:"-"`
	PremiumPathKind         string    `json:"-"`
	PremiumCanvasSize       bool      `json:"-"`
	PremiumSyncTransform    bool      `json:"-"`
	PremiumAbsolutePosition bool      `json:"-"`
	PremiumWipeMask         bool      `json:"-"`
	// FrameStroke is transient compiler metadata expanded to a native shape
	// layer after the media layer is compiled; it never crosses the Chronon wire.
	FrameStroke *LayerStroke `json:"-"`
}

// MarshalJSON keeps adjustment layers on the effect-only wire shape required
// by the Chronon v3 schema. That schema's adjustment branch allows only id,
// type and effects (additionalProperties:false); serializing the zero-valued
// start_frame/duration_frames made every effects-bearing clip fail validation
// before the renderer saw the plan.
func (l Layer) MarshalJSON() ([]byte, error) {
	if l.Type == "adjustment" {
		return json.Marshal(struct {
			ID      string        `json:"id"`
			Type    string        `json:"type"`
			Effects []LayerEffect `json:"effects"`
		}{ID: l.ID, Type: l.Type, Effects: l.Effects})
	}
	type layerWire Layer
	return json.Marshal(layerWire(l))
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
	Offset    float64             `json:"offset"`
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
	Property  string              `json:"property,omitempty"`
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

// Text fit modes are part of the Chronon render-plan contract: a layer that
// declares min_font_size/max_font_size must also declare a non-none fit mode,
// otherwise plan preparation rejects the payload before any frame is drawn
// ("text font size limits require a non-none fit_mode"). Producers that pin a
// fit interval spell the mode from here so the vocabulary has one owner.
const (
	// FitModeShrinkOnly lets the layout engine shrink bounded text down to
	// min_font_size and never grow past max_font_size. With min == max the
	// pinned size renders verbatim.
	FitModeShrinkOnly = "shrink_only"
)

type LayerStyle struct {
	Font        string       `json:"font,omitempty"`
	FontSize    float64      `json:"font_size,omitempty"`
	Fill        string       `json:"fill,omitempty"`
	TextAlign   string       `json:"text_align,omitempty"`
	FitMode     string       `json:"fit_mode,omitempty"`
	MinFontSize float64      `json:"min_font_size,omitempty"`
	MaxFontSize float64      `json:"max_font_size,omitempty"`
	Stroke      *LayerStroke `json:"stroke,omitempty"`
	Shadow      *LayerShadow `json:"shadow,omitempty"`
	// Shadows is the multi-shadow form of the renderer's style contract: an
	// ordered stack of at most three drop shadows. The engine rejects a layer
	// that declares both shadow and shadows, so the compiler never emits both.
	Shadows    []LayerShadow    `json:"shadows,omitempty"`
	Glow       *LayerGlow       `json:"glow,omitempty"`
	Background *LayerBackground `json:"background,omitempty"`
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
	// LiftToShape marks a runtime card_background that must be lowered to a
	// native shape plate. Preset-authored backgrounds keep their historical
	// (non-rendered) wire form; only the payload's plate is lifted.
	LiftToShape bool `json:"-"`
}

type LayerStroke struct {
	Color string  `json:"color,omitempty"`
	Width float64 `json:"width,omitempty"`
}
type TextSelector struct {
	ID             string          `json:"id,omitempty"`
	SemanticID     string          `json:"semantic_id,omitempty"`
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
