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

// Plan is the single concrete chronon.render-plan.v2 model. The compiler
// builds it, the processor mutates it mechanically (asset-path normalization,
// subtitle burn-in) and it is marshaled exactly once at the Chronon boundary.
type Plan struct {
	Schema  string  `json:"schema"`
	Version int     `json:"version"`
	JobID   string  `json:"job_id"`
	Canvas  Canvas  `json:"canvas"`
	Layers  []Layer `json:"layers"`
	Output  Output  `json:"output"`
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
	plan, assets, stats, err := compileSemantic(raw)
	if err != nil {
		return CompileResult{}, err
	}
	return CompileResult{Plan: plan, Assets: assets, Stats: stats}, nil
}

// Marshal serializes the typed plan once at the Chronon boundary.
func (p *Plan) Marshal() ([]byte, error) {
	return json.Marshal(p)
}

type semanticPlan struct {
	SchemaVersion   string          `json:"schema_version"`
	PlanID          string          `json:"plan_id"`
	VideoID         string          `json:"video_id"`
	Source          *semanticSource `json:"source,omitempty"`
	ForegroundScale int             `json:"foreground_scale_percent,omitempty"`
	Width           int             `json:"width"`
	Height          int             `json:"height"`
	FPSNum          int             `json:"fps_num"`
	FPSDen          int             `json:"fps_den"`
	// DurationMS is the explicit clip duration. When provided it seeds the
	// canvas duration before items are processed; items can only extend it.
	// Required when items is empty (clip render without entity overlays).
	DurationMS      int64               `json:"duration_ms,omitempty"`
	OutputProfileID string              `json:"output_profile_id"`
	StyleProfile    string              `json:"style_profile"`
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

type semanticSubtitles struct {
	AssetRefs []semanticAssetRef `json:"asset_refs,omitempty"`
	StyleID   string             `json:"style_id,omitempty"`
	Mode      string             `json:"mode,omitempty"`
	// Style is the caller's typed visual override. It is REQUIRED for burn
	// mode: the worker never invents subtitle geometry/color/shadow.
	Style map[string]any `json:"style,omitempty"`
}

type semanticWatermark struct {
	Text      string             `json:"text,omitempty"`
	AssetRefs []semanticAssetRef `json:"asset_refs,omitempty"`
	FontRef   *semanticAssetRef  `json:"font_ref,omitempty"`
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
	AssetRefs []semanticAssetRef `json:"asset_refs,omitempty"`
	Fit       string             `json:"fit,omitempty"`
	Opacity   *float64           `json:"opacity,omitempty"`
	Loop      bool               `json:"loop,omitempty"`
}

type semanticItem struct {
	ID string `json:"id"`
	// Kind is the semantic SSOT of the item. It is authoritative when
	// PipelineGen sends it; when it is absent the template registry supplies
	// it. A kind that contradicts the template's kind is rejected fail-closed
	// (see TemplateSpec.resolveKind).
	Kind     string `json:"kind"`
	Template string `json:"template_id"`
	// PresetID is the semantic preset selected by PipelineGen (the plan's
	// preset_id contract slot). It is preferred over the template mapping.
	PresetID      string             `json:"preset_id"`
	ImagePresetID string             `json:"image_preset_id"`
	MotionID      string             `json:"motion_id"`
	MotionParams  map[string]any     `json:"motion_params"`
	Text          string             `json:"text"`
	StartMS       int64              `json:"start_ms"`
	EndMS         int64              `json:"end_ms"`
	Params        map[string]any     `json:"params"`
	Assets        []semanticAssetRef `json:"asset_refs"`
}

type semanticAssetRef struct {
	ID        string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	MediaType string `json:"media_type"`
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
	Property  string              `json:"property"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
	Easing    string              `json:"easing,omitempty"`
}

type AnimationKeyframe struct {
	Frame int64 `json:"frame"`
	Value any   `json:"value"`
}

type Layer struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Asset          string          `json:"asset,omitempty"`
	Source         string          `json:"source,omitempty"`
	Color          []float64       `json:"color,omitempty"`
	Text           string          `json:"text,omitempty"`
	BoxWidth       int             `json:"-"`
	BoxHeight      int             `json:"-"`
	Size           []float64       `json:"size,omitempty"`
	Fit            string          `json:"fit,omitempty"`
	Radius         float64         `json:"radius,omitempty"`
	Position       []float64       `json:"position,omitempty"`
	Scale          []float64       `json:"scale,omitempty"`
	Style          *LayerStyle     `json:"style,omitempty"`
	StartFrame     int64           `json:"start_frame"`
	DurationFrames int64           `json:"duration_frames"`
	Animation      *LayerAnimation `json:"animation,omitempty"`
	TextAnimators  []TextAnimator  `json:"text_animators,omitempty"`
	Opacity        float64         `json:"opacity,omitempty"`
	Loop           bool            `json:"loop,omitempty"`
}
type LayerStyle struct {
	Font     string       `json:"font,omitempty"`
	FontSize float64      `json:"font_size,omitempty"`
	Fill     string       `json:"fill,omitempty"`
	Stroke   *LayerStroke `json:"stroke,omitempty"`
	Shadow   *LayerShadow `json:"shadow,omitempty"`
}
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
