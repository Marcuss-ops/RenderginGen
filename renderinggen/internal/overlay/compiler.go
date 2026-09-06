// Package overlay owns the RenderingGen-side boundary between PipelineGen and
// Chronon. RenderingGen is an EXECUTION WORKER: it validates a job, materializes
// its assets, writes plan.json, renders with Chronon and publishes the artifact.
// It does not perform NER, entity linking or editorial ranking.
//
// PipelineGen owns the semantic decisions and emits overlay-plan.v1. The
// worker performs the mechanical lowering through the RenderingGen-owned
// official preset catalog, then continues through one asset/render/artifact
// pipeline. Chronon is only the execution engine.
package overlay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/motion"
)

// SemanticSchema is PipelineGen's semantic overlay contract. RenderingGen
// compiles it mechanically; PipelineGen remains the owner of its decisions.
const SemanticSchema = "renderinggen.overlay-plan.v1"

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

// CompileIfSemantic lowers the PipelineGen semantic contract to the concrete
// Chronon plan consumed by the worker. It resolves RenderingGen's official
// preset catalog; it does not perform NER, entity linking or editorial
// ranking. Those decisions arrive in template_id/entity_id from PipelineGen.
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
func CompileIfSemantic(raw []byte) (*Plan, []Asset, bool, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, nil, false, fmt.Errorf("overlay: decode plan: %w", err)
	}
	if probe.SchemaVersion == SemanticSchema {
		compiled, assets, err := compileSemantic(raw)
		if err != nil {
			return nil, nil, false, err
		}
		return compiled, assets, true, nil
	}
	return nil, nil, false, fmt.Errorf("overlay: unsupported plan schema %q (semantic %q is the only accepted contract)", probe.SchemaVersion, SemanticSchema)
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
	ID        string             `json:"id"`
	SceneID   string             `json:"scene_id"`
	EntityID  string             `json:"entity_id"`
	EntityRef *semanticEntityRef `json:"entity_ref"`
	Kind      string             `json:"kind"`
	Template  string             `json:"template_id"`
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

// semanticEntityRef is the plan's entity_ref block: the content-addressed
// entity identity of the item. The compiler only uses it to fall back to a
// display text when the item carries no explicit text — it never performs
// entity linking or ranking.
type semanticEntityRef struct {
	EntityID    string `json:"entity_id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	SurfaceText string `json:"surface_text,omitempty"`
}

type semanticAssetRef struct {
	ID        string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	MediaType string `json:"media_type"`
}

// SubtitleAsset returns the content hash and burn policy declared by a
// semantic overlay plan. It is used after materialization, when the worker can
// safely read the verified subtitle bytes and lower them into Chronon layers.
func SubtitleAsset(raw []byte) (hash string, burn bool, ok bool, err error) {
	var src semanticPlan
	if err := json.Unmarshal(raw, &src); err != nil {
		return "", false, false, fmt.Errorf("overlay: decode subtitle contract: %w", err)
	}
	if src.Subtitles == nil || len(src.Subtitles.AssetRefs) == 0 {
		return "", false, false, nil
	}
	ref := src.Subtitles.AssetRefs[0]
	return strings.ToLower(ref.SHA256), strings.EqualFold(strings.TrimSpace(src.Subtitles.Mode), "burn"), true, nil
}

// SubtitleStyleAsset resolves the plan's typed subtitle style into the
// concrete Chronon style + safe-area box used by BurnASSIntoPlan. The plan is
// the single style owner; resolution goes through the canonical visual style
// resolver (parseStyleBlock + subtitleLayerStyle), so a burn-mode plan
// without a fully declared style is rejected fail-closed — the compiler
// never substitutes its own typography.
func SubtitleStyleAsset(raw []byte) (*LayerStyle, SubtitleStyleBox, error) {
	var doc struct {
		Canvas struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"canvas"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, SubtitleStyleBox{}, fmt.Errorf("overlay: decode subtitle style contract: %w", err)
	}
	var src semanticPlan
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, SubtitleStyleBox{}, fmt.Errorf("overlay: decode subtitle style contract: %w", err)
	}
	if src.Subtitles == nil || src.Subtitles.Style == nil {
		return nil, SubtitleStyleBox{}, nil
	}
	block, err := parseStyleBlock(src.Subtitles.Style)
	if err != nil {
		return nil, SubtitleStyleBox{}, err
	}
	style, err := subtitleLayerStyle(block, "")
	if err != nil {
		return nil, SubtitleStyleBox{}, err
	}
	if block.Position == "" {
		return nil, SubtitleStyleBox{}, fmt.Errorf("overlay: subtitle style must declare position (placement is owned by PipelineGen)")
	}
	width, height := src.Width, src.Height
	if width <= 0 || height <= 0 {
		width, height = doc.Canvas.Width, doc.Canvas.Height
	}
	if width <= 0 || height <= 0 {
		return nil, SubtitleStyleBox{}, fmt.Errorf("overlay: subtitle burn requires a canvas")
	}
	position, size, err := subtitleCueGeometry(block, width, height, 1)
	if err != nil {
		return nil, SubtitleStyleBox{}, err
	}
	box := SubtitleStyleBox{
		Width:  int(size[0]),
		Height: int(size[1]),
		X:      int(position[0] + float64(width)/2 - size[0]/2),
		Y:      int(position[1] + float64(height)/2 - size[1]/2),
	}
	return style, box, nil
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
	Shadow   *LayerShadow `json:"shadow,omitempty"`
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

// BurnASSIntoPlan lowers an ASS subtitle track into ordinary Chronon text
// layers. Chronon then rasterizes each cue once, uploads the texture to GPU,
// and composites it before NVENC; no post-render ffmpeg subtitle pass is
// needed. The input plan must already be concrete render-plan.v2, the
// fontPath must be a prepared workspace-relative font asset, and the style
// must be fully typed by the caller (PipelineGen's plan) — this function
// invents no typography or geometry.
//
// BurnASSIntoPlanTyped is the typed-plan variant used by the processor: it
// mutates the in-memory plan instead of re-encoding JSON.
func BurnASSIntoPlan(planBytes, assBytes []byte, fontPath string, style *LayerStyle, box SubtitleStyleBox) ([]byte, int, error) {
	var plan Plan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		return nil, 0, fmt.Errorf("overlay: decode concrete plan for subtitles: %w", err)
	}
	count, err := appendSubtitleLayers(&plan, assBytes, fontPath, style, box)
	if err != nil {
		return nil, 0, err
	}
	out, err := plan.Marshal()
	if err != nil {
		return nil, 0, fmt.Errorf("overlay: encode subtitle layers: %w", err)
	}
	return out, count, nil
}

// BurnASSIntoPlanTyped mutates the typed plan in place with the lowered
// subtitle cue layers and returns the number of cue layers present afterwards.
// The processor path uses this to avoid a JSON round-trip; the plan is
// marshaled exactly once at the Chronon boundary.
func BurnASSIntoPlanTyped(plan *Plan, assBytes []byte, fontPath string, style *LayerStyle, box SubtitleStyleBox) (int, error) {
	return appendSubtitleLayers(plan, assBytes, fontPath, style, box)
}

// appendSubtitleLayers is the shared lowering core: validate inputs, parse
// the ASS cues, append one GPU text layer per cue and return the count.
func appendSubtitleLayers(plan *Plan, assBytes []byte, fontPath string, style *LayerStyle, box SubtitleStyleBox) (int, error) {
	if plan == nil {
		return 0, fmt.Errorf("overlay: burn subtitles requires a plan")
	}
	if strings.TrimSpace(fontPath) == "" {
		return 0, fmt.Errorf("overlay: burn subtitles requires a prepared font")
	}
	if style == nil || style.FontSize == 0 {
		return 0, fmt.Errorf("overlay: burn subtitles requires a typed style with font_size (no compiler defaults)")
	}
	if box.Width <= 0 || box.Height <= 0 {
		return 0, fmt.Errorf("overlay: burn subtitles requires a positive subtitle box")
	}
	cues, err := parseASSCues(assBytes, plan.Canvas.FPSNum, plan.Canvas.FPSDen)
	if err != nil {
		return 0, err
	}
	for _, layer := range plan.Layers {
		if strings.HasPrefix(layer.ID, "subtitle_cue_") {
			return 0, nil // idempotent: cues already lowered
		}
	}
	for i, cue := range cues {
		if cue.Text == "" || cue.EndFrame <= cue.StartFrame {
			continue
		}
		cueStyle := *style
		cueStyle.Font = fontPath
		plan.Layers = append(plan.Layers, Layer{
			ID: "subtitle_cue_" + strconv.Itoa(i), Type: "text", Text: cue.Text,
			Size: []float64{float64(box.Width), float64(box.Height)},
			// Chronon layer positions are offsets from the canvas centre and
			// address the layer centre. Convert the absolute safe-area box.
			Position: []float64{
				float64(box.X) + float64(box.Width)*0.5 - float64(plan.Canvas.Width)*0.5,
				float64(box.Y) + float64(box.Height)*0.5 - float64(plan.Canvas.Height)*0.5,
			},
			Style:      &cueStyle,
			StartFrame: cue.StartFrame, DurationFrames: cue.EndFrame - cue.StartFrame,
		})
	}
	count := 0
	for _, layer := range plan.Layers {
		if strings.HasPrefix(layer.ID, "subtitle_cue_") {
			count++
		}
	}
	return count, nil
}

type assCue struct {
	StartFrame int64
	EndFrame   int64
	Text       string
}

func parseASSCues(raw []byte, fpsNum, fpsDen int) ([]assCue, error) {
	if fpsNum <= 0 || fpsDen <= 0 {
		return nil, fmt.Errorf("overlay: invalid subtitle fps")
	}
	var cues []assCue
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(strings.ToLower(line), "dialogue:") {
			continue
		}
		fields := strings.SplitN(strings.TrimSpace(line[len("Dialogue:"):]), ",", 10)
		if len(fields) < 10 {
			continue
		}
		start, err := assTimeFrame(strings.TrimSpace(fields[1]), fpsNum, fpsDen)
		if err != nil {
			return nil, err
		}
		end, err := assTimeFrame(strings.TrimSpace(fields[2]), fpsNum, fpsDen)
		if err != nil {
			return nil, err
		}
		text := strings.TrimSpace(fields[9])
		text = assBraceTagRe.ReplaceAllString(text, "")
		text = strings.ReplaceAll(strings.ReplaceAll(text, `\N`, "\n"), `\n`, "\n")
		cues = append(cues, assCue{StartFrame: start, EndFrame: end, Text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("overlay: read subtitles: %w", err)
	}
	return cues, nil
}

// assBraceTagRe strips ASS override blocks like {\i1} from dialogue text.
var assBraceTagRe = regexp.MustCompile(`\{[^}]*\}`)

func assTimeFrame(raw string, fpsNum, fpsDen int) (int64, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("overlay: invalid ASS timestamp %q", raw)
	}
	seconds, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	secParts := strings.SplitN(parts[2], ".", 2)
	whole, err := strconv.Atoi(secParts[0])
	if err != nil {
		return 0, err
	}
	centis := 0
	if len(secParts) == 2 {
		centis, err = strconv.Atoi(secParts[1])
		if err != nil {
			return 0, err
		}
	}
	ms := int64(seconds*3600000 + minutes*60000 + whole*1000 + centis*10)
	return (ms*int64(fpsNum) + 1000*int64(fpsDen) - 1) / (1000 * int64(fpsDen)), nil
}

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

// resolveWatermarkPosition was moved to visual_style_resolver.go — the single
// owner of watermark/subtitle geometry resolution.

func animationForMotion(id string, params map[string]any, textValue string, duration int64) (*LayerAnimation, error) {
	plugin, err := motion.Registry.Resolve(id)
	if err != nil {
		return nil, fmt.Errorf("overlay: %w", err)
	}
	tracks, err := plugin.Compile(motion.MotionContext{Text: textValue, DurationFrames: duration}, motion.MotionParams(params))
	if err != nil {
		return nil, fmt.Errorf("overlay: compile motion %q: %w", id, err)
	}
	animation := &LayerAnimation{Tracks: fromMotionTracks(tracks)}
	if textPlugin, ok := plugin.(motion.TextMotionPlugin); ok {
		textDefinitions, err := textPlugin.CompileText(
			motion.MotionContext{Text: textValue, DurationFrames: duration},
			motion.MotionParams(params))
		if err != nil {
			return nil, fmt.Errorf("compile text motion %q: %w", id, err)
		}
		animation.TextAnimators = fromTextMotionDefinitions(textDefinitions, duration)
		if err := validateTextMotion(animation.TextAnimators, id); err != nil {
			return nil, err
		}
	}
	return animation, nil
}

func validateTextMotion(animators []TextAnimator, id string) error {
	for _, animator := range animators {
		if len(animator.Selectors) == 0 || len(animator.Properties) == 0 {
			return fmt.Errorf("overlay: motion %q collapsed to an empty text animator", id)
		}
		nonOpacity := false
		for _, track := range animator.Properties {
			if track.Property != "opacity" {
				nonOpacity = true
				break
			}
		}
		selector := animator.Selectors[0]
		// An opacity-only animator is valid when it has a real per-element
		// selector sweep (for example opacity_wave). A layer selector without
		// that sweep is the legacy whole-layer fade we reject.
		perElementSweep := selector.Unit != "layer" && (selector.Start != nil || selector.End != nil)
		if !nonOpacity && !perElementSweep {
			return fmt.Errorf("overlay: motion %q collapsed to layer fade", id)
		}
	}
	return nil
}

func msFrames(start, end, fpsNum, fpsDen int64) (int64, int64) {
	return int64(math.Floor(float64(start) * float64(fpsNum) / float64(fpsDen) / 1000)),
		int64(math.Ceil(float64(end) * float64(fpsNum) / float64(fpsDen) / 1000))
}

// presetRequiredTemplates are semantic templates that must carry an official
// preset. RenderingGen validates the supplied opaque id, while PipelineGen
// remains responsible for editorial selection.
var presetRequiredTemplates = map[string]bool{
	"IMPORTANT_PHRASE": true,
	"IMPORTANT_WORD":   true,
	"NUMBER":           true,
	"QUOTE":            true,
	"CONCEPT":          true,
	"MONEY":            true,
	"PERCENT":          true,
	"PERSON":           true,
	"ORGANIZATION":     true,
	"LOCATION":         true,
	"IMAGE_OVERLAY":    true,
}

func presetFor(item semanticItem) (string, error) {
	// The plan's preset_id contract slot is the only spelling. RenderingGen's
	// official catalog is authoritative.
	//
	// ADR-029 forward-point (d): RenderingGen is an execution worker and must
	// NOT re-map a template_id to a preset (e.g. it must not know that PERSON
	// means lower_third_safe). The semantic_role → preset_id decision lives
	// only in PipelineGen's SemanticOverlayResolver. A preset-driven template
	// that does not carry a preset_id is rejected; preset-less primitives
	// (PRODUCT, LOGO, LIGHT_LEAK, …) legitimately compile without one.
	p := strings.TrimSpace(item.PresetID)
	if p != "" {
		family := "text"
		if isImageTemplate(item.Template) {
			family = "image"
		}
		return validatePreset(p, item.ID, family)
	}
	if presetRequiredTemplates[strings.ToUpper(item.Template)] {
		return "", fmt.Errorf("overlay: item %q requires preset_id (resolved by PipelineGen's SemanticOverlayResolver)", item.ID)
	}
	return "", nil
}
func validatePreset(p, id, kind string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("overlay: empty preset_id for item %q", id)
	}
	d, err := resolveOfficialPreset(p, kind)
	if err != nil {
		return "", err
	}
	return d.ID, nil
}
func isImageTemplate(t string) bool {
	switch strings.ToUpper(t) {
	case "IMAGE_OVERLAY", "PRODUCT", "LOGO", "LIGHT_LEAK":
		return true
	}
	return false
}
func isEntityTemplate(t string) bool {
	switch strings.ToUpper(t) {
	case "PERSON", "PERSON_DEFAULT", "ORGANIZATION", "ORGANIZATION_DEFAULT", "LOCATION", "LOCATION_DEFAULT", "GPE_DEFAULT":
		return true
	}
	return false
}

// entityRefText returns the display text from the plan's entity_ref block:
// surface_text first, then the canonical name.
// The compiler never invents a name — an empty ref yields empty text.
func entityRefText(item semanticItem) string {
	if item.EntityRef == nil {
		return ""
	}
	if strings.TrimSpace(item.EntityRef.SurfaceText) != "" {
		return item.EntityRef.SurfaceText
	}
	if strings.TrimSpace(item.EntityRef.Name) != "" {
		return item.EntityRef.Name
	}
	return ""
}

func imageLayer(item semanticItem, start, end int64, asset string, params map[string]any) Layer {
	w, h := intParam(params, "width", 320), intParam(params, "height", 320)
	return Layer{ID: item.ID + "_image", Type: "image", Asset: asset, BoxWidth: w, BoxHeight: h, Size: []float64{float64(w), float64(h)}, Fit: stringParam(params, "fit", "contain"), StartFrame: start, DurationFrames: end - start}
}

func applyPresetDefinition(layer *Layer, d OfficialPresetDefinition) {
	if d.Family == PresetImage {
		if layer.BoxWidth == 320 && d.Layout.BoxWidth > 0 {
			layer.BoxWidth = d.Layout.BoxWidth
		}
		if layer.BoxHeight == 320 && d.Layout.BoxHeight > 0 {
			layer.BoxHeight = d.Layout.BoxHeight
		}
		if layer.Fit == "contain" && d.Layout.Fit != "" {
			layer.Fit = d.Layout.Fit
		}
		return
	}
	font := d.Style.FontFamily
	if layer.Style != nil && layer.Style.Font != "" {
		// Prepared fixtures may use a workspace-relative alias (for example
		// fonts/Poppins-Bold.ttf). Preserve it while still applying the
		// catalog-owned visual properties.
		font = layer.Style.Font
	}
	layer.Style = &LayerStyle{Font: font, FontSize: d.Style.FontSize, Fill: rgbaHex(d.Style.Fill)}
	if d.Style.Shadow != nil {
		s := d.Style.Shadow
		layer.Style.Shadow = &LayerShadow{Color: s.Color, Opacity: s.Opacity, Blur: s.Blur, Offset: append([]float64(nil), s.Offset...)}
	}
}

func animationForDefinition(d OfficialPresetDefinition) (*LayerAnimation, error) {
	if d.Motion.Name == "" && d.Motion.ID == "" {
		return nil, nil
	}
	tracks, err := tracksForMotion(d.Motion)
	if err != nil {
		return nil, err
	}
	return &LayerAnimation{Tracks: tracks}, nil
}

func rgbaHex(v []float64) string {
	if len(v) != 4 {
		return ""
	}
	// Chronon render-plan.v2 uses opaque six-digit CSS colors. Alpha is
	// carried by layer/style opacity fields, never embedded in the color.
	return fmt.Sprintf("#%02X%02X%02X", int(v[0]*255), int(v[1]*255), int(v[2]*255))
}

func intParam(p map[string]any, key string, fallback int) int {
	if n, ok := p[key].(float64); ok {
		return int(n)
	}
	return fallback
}
func stringParam(p map[string]any, key, fallback string) string {
	if s, ok := p[key].(string); ok && s != "" {
		return s
	}
	return fallback
}
