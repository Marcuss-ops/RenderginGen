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
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/motion"
)

// SemanticSchema is PipelineGen's semantic overlay contract. RenderingGen
// compiles it mechanically; PipelineGen remains the owner of its decisions.
const SemanticSchema = "renderinggen.overlay-plan.v1"

// Plan is the concrete chronon.render-plan.v2 document the worker executes.
// It is carried here so callers/tests can decode a submitted plan; the worker
// itself treats it as an opaque blob passed straight to Chronon.
type Plan struct {
	Schema  string  `json:"schema"`
	Version int     `json:"version"`
	JobID   string  `json:"job_id"`
	Canvas  Canvas  `json:"canvas"`
	Layers  []Layer `json:"layers"`
	Output  Output  `json:"output"`
}

type Canvas struct {
	Width          int   `json:"width"`
	Height         int   `json:"height"`
	FPSNum         int   `json:"fps_num"`
	FPSDen         int   `json:"fps_den"`
	DurationFrames int64 `json:"duration_frames"`
}

type Layer struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Asset string `json:"asset,omitempty"`
	// Source is the video-source logical path for Video layers
	// (VIDEO_BACKGROUND): Chronon video layers reference `source`.
	Source         string      `json:"source,omitempty"`
	Text           string      `json:"text,omitempty"`
	BoxWidth       int         `json:"box_width,omitempty"`
	BoxHeight      int         `json:"box_height,omitempty"`
	Fit            string      `json:"fit,omitempty"`
	Position       []float64   `json:"position,omitempty"`
	Style          *LayerStyle `json:"style,omitempty"`
	Opacity        float64     `json:"opacity,omitempty"`
	BlendMode      string      `json:"blend_mode,omitempty"`
	Loop           bool        `json:"loop,omitempty"`
	StartFrame     int64       `json:"start_frame"`
	DurationFrames int64       `json:"duration_frames"`
	// Animation is the motion preset applied to the layer.
	Animation *LayerAnimation `json:"animation,omitempty"`
}

// LayerStyle is the single concrete visual representation sent to Chronon.
type LayerStyle struct {
	FontFamily string       `json:"font_family,omitempty"`
	FontWeight int          `json:"font_weight,omitempty"`
	FontSize   float64      `json:"font_size,omitempty"`
	Fill       string       `json:"fill,omitempty"`
	Shadow     *LayerShadow `json:"shadow,omitempty"`
}

type LayerShadow struct {
	Color   string    `json:"color,omitempty"`
	Opacity float64   `json:"opacity,omitempty"`
	Blur    float64   `json:"blur,omitempty"`
	Offset  []float64 `json:"offset,omitempty"`
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

// LayerAnimation contains only generic tracks.
type LayerAnimation struct {
	Tracks []AnimationTrack `json:"tracks,omitempty"`
}

type Output struct {
	Path      string `json:"path"`
	Format    string `json:"format"`
	Codec     string `json:"codec"`
	ProfileID string `json:"profile_id,omitempty"`
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
// Concrete Chronon plans remain a byte-for-byte pass-through path.
func CompileIfSemantic(raw []byte) ([]byte, []Asset, bool, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, nil, false, fmt.Errorf("overlay: decode plan: %w", err)
	}
	if probe.SchemaVersion == "" {
		// Concrete Chronon plans remain a byte-for-byte compatibility path.
		// New authoring/typography callers must use the semantic contract above;
		// this branch only preserves existing worker jobs and test fixtures.
		return raw, nil, false, nil
	}
	if probe.SchemaVersion == SemanticSchema {
		compiled, assets, err := compileSemantic(raw)
		if err != nil {
			return nil, nil, false, err
		}
		return compiled, assets, true, nil
	}
	return nil, nil, false, fmt.Errorf("overlay: unsupported plan schema %q", probe.SchemaVersion)
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
	Style     map[string]any     `json:"style,omitempty"`
}

type semanticWatermark struct {
	Text      string             `json:"text,omitempty"`
	AssetRefs []semanticAssetRef `json:"asset_refs,omitempty"`
	FontRef   *semanticAssetRef  `json:"font_ref,omitempty"`
	Position  string             `json:"position,omitempty"`
	Opacity   *float64           `json:"opacity,omitempty"`
	Style     map[string]any     `json:"style,omitempty"`
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
	PresetID     string             `json:"preset_id"`
	MotionID     string             `json:"motion_id"`
	MotionParams map[string]any     `json:"motion_params"`
	Text         string             `json:"text"`
	StartMS      int64              `json:"start_ms"`
	EndMS        int64              `json:"end_ms"`
	Params       map[string]any     `json:"params"`
	Assets       []semanticAssetRef `json:"asset_refs"`
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

type concretePlan struct {
	Schema  string          `json:"schema"`
	Version int             `json:"version"`
	JobID   string          `json:"job_id"`
	Canvas  concreteCanvas  `json:"canvas"`
	Layers  []concreteLayer `json:"layers"`
	Output  concreteOutput  `json:"output"`
}

type concreteCanvas struct {
	Width          int   `json:"width"`
	Height         int   `json:"height"`
	FPSNum         int   `json:"fps_num"`
	FPSDen         int   `json:"fps_den"`
	DurationFrames int64 `json:"duration_frames"`
}
type concreteOutput struct {
	Path      string `json:"path"`
	Format    string `json:"format"`
	Codec     string `json:"codec"`
	ProfileID string `json:"profile_id,omitempty"`
	// Retained for typed semantic metadata; omitted from Chronon's v2 JSON.
	Audio *concreteAudio `json:"-"`
}

// concreteAudio carries the audio policy in the Chronon render plan so the
// renderer knows whether to copy or transcode the source audio stream.
type concreteAudio struct {
	Mode       string `json:"mode,omitempty"`
	Codec      string `json:"codec,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}
type concreteLayer struct {
	ID             string                 `json:"id"`
	Type           string                 `json:"type"`
	Asset          string                 `json:"asset,omitempty"`
	Source         string                 `json:"source,omitempty"`
	Color          []float64              `json:"color,omitempty"`
	Text           string                 `json:"text,omitempty"`
	BoxWidth       int                    `json:"-"`
	BoxHeight      int                    `json:"-"`
	Size           []float64              `json:"size,omitempty"`
	Fit            string                 `json:"fit,omitempty"`
	Position       []float64              `json:"position,omitempty"`
	Style          *concreteStyle         `json:"style,omitempty"`
	StartFrame     int64                  `json:"start_frame"`
	DurationFrames int64                  `json:"duration_frames"`
	Animation      *concreteAnimation     `json:"animation,omitempty"`
	TextAnimators  []concreteTextAnimator `json:"text_animators,omitempty"`
	Opacity        float64                `json:"opacity,omitempty"`
	Loop           bool                   `json:"loop,omitempty"`
}
type concreteStyle struct {
	Font     string          `json:"font,omitempty"`
	FontSize float64         `json:"font_size,omitempty"`
	Fill     string          `json:"fill,omitempty"`
	Shadow   *concreteShadow `json:"shadow,omitempty"`
}
type concreteTextSelector struct {
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
type concreteTextAnimator struct {
	ID         string                 `json:"id,omitempty"`
	Selectors  []concreteTextSelector `json:"selectors"`
	Properties []AnimationTrack       `json:"properties"`
}
type concreteStroke struct {
	Color string  `json:"color,omitempty"`
	Width float64 `json:"width,omitempty"`
}
type concreteShadow struct {
	Color   string    `json:"color,omitempty"`
	Opacity float64   `json:"opacity,omitempty"`
	Blur    float64   `json:"blur,omitempty"`
	Offset  []float64 `json:"offset,omitempty"`
}
type concreteBackground struct {
	Color   string    `json:"color,omitempty"`
	Opacity float64   `json:"opacity,omitempty"`
	Radius  float64   `json:"radius,omitempty"`
	Padding []float64 `json:"padding,omitempty"`
}
type concreteAnimation struct {
	Tracks        []AnimationTrack       `json:"tracks,omitempty"`
	TextAnimators []concreteTextAnimator `json:"-"`
}

// BurnASSIntoPlan lowers an ASS subtitle track into ordinary Chronon text
// layers. Chronon then rasterizes each cue once, uploads the texture to GPU,
// and composites it before NVENC; no post-render ffmpeg subtitle pass is
// needed. The input plan must already be concrete render-plan.v2 and the
// fontPath must be a prepared workspace-relative font asset.
func BurnASSIntoPlan(planBytes, assBytes []byte, fontPath string) ([]byte, int, error) {
	if strings.TrimSpace(fontPath) == "" {
		return nil, 0, fmt.Errorf("overlay: burn subtitles requires a prepared font")
	}
	var plan concretePlan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		return nil, 0, fmt.Errorf("overlay: decode concrete plan for subtitles: %w", err)
	}
	cues, err := parseASSCues(assBytes, plan.Canvas.FPSNum, plan.Canvas.FPSDen)
	if err != nil {
		return nil, 0, err
	}
	for _, layer := range plan.Layers {
		if strings.HasPrefix(layer.ID, "subtitle_cue_") {
			return planBytes, 0, nil
		}
	}
	for i, cue := range cues {
		if cue.Text == "" || cue.EndFrame <= cue.StartFrame {
			continue
		}
		plan.Layers = append(plan.Layers, concreteLayer{
			ID: "subtitle_cue_" + strconv.Itoa(i), Type: "text", Text: cue.Text,
			Size:     []float64{float64(plan.Canvas.Width - 120), 140},
			Position: []float64{60, float64(plan.Canvas.Height) * 0.76},
			Style: &concreteStyle{
				Font: fontPath, FontSize: 52, Fill: "#FFFFFF",
				Shadow: &concreteShadow{Color: "#000000", Opacity: 0.92, Blur: 4, Offset: []float64{0, 3}},
			},
			StartFrame: cue.StartFrame, DurationFrames: cue.EndFrame - cue.StartFrame,
		})
	}
	if len(cues) == 0 {
		return planBytes, 0, nil
	}
	out, err := json.Marshal(plan)
	if err != nil {
		return nil, 0, fmt.Errorf("overlay: encode subtitle layers: %w", err)
	}
	count := 0
	for _, layer := range plan.Layers {
		if strings.HasPrefix(layer.ID, "subtitle_cue_") {
			count++
		}
	}
	return out, count, nil
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
		text = regexp.MustCompile(`\{[^}]*\}`).ReplaceAllString(text, "")
		text = strings.ReplaceAll(strings.ReplaceAll(text, `\N`, "\n"), `\n`, "\n")
		cues = append(cues, assCue{StartFrame: start, EndFrame: end, Text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("overlay: read subtitles: %w", err)
	}
	return cues, nil
}

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

var safeAssetID = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func compileSemantic(raw []byte) ([]byte, []Asset, error) {
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
	plan := concretePlan{Schema: "chronon.render-plan.v2", Version: 2, JobID: src.PlanID,
		Canvas: concreteCanvas{Width: src.Width, Height: src.Height, FPSNum: src.FPSNum, FPSDen: src.FPSDen},
		Output: concreteOutput{Path: "result.mp4", Format: "mp4", Codec: "h264", ProfileID: src.OutputProfileID}}

	// Seed canvas duration from the explicit duration_ms when provided. Items
	// can extend it but cannot shrink it. For clip renders with items:[] this
	// is the only source of duration; the compiler rejects zero duration at the
	// end of the function.
	if src.DurationMS > 0 {
		startFrame, endFrame := msFrames(0, src.DurationMS, int64(src.FPSNum), int64(src.FPSDen))
		_ = startFrame
		plan.Canvas.DurationFrames = endFrame
	}

	assetPaths := map[string]string{}
	if src.Audio != nil {
		plan.Output.Audio = &concreteAudio{
			Mode: src.Audio.Mode, Codec: src.Audio.Codec,
			SampleRate: src.Audio.SampleRate, Channels: src.Audio.Channels,
		}
	}
	var assets []Asset
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
			if ref.ID == "" || len(ref.SHA256) != 64 || strings.Trim(ref.SHA256, "0123456789abcdefABCDEF") != "" {
				return nil, nil, fmt.Errorf("overlay: background has invalid asset ref %q", ref.ID)
			}
			path := assetPaths[ref.ID]
			if path == "" {
				path = semanticAssetPath(ref)
				assetPaths[ref.ID] = path
				assets = append(assets, Asset{Hash: strings.ToLower(ref.SHA256), LogicalPath: path})
			}
		}
		layer := concreteLayer{ID: "background", Type: layerKind, BoxWidth: src.Width, BoxHeight: src.Height,
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
				layer.Source = assetPaths[ref.ID]
			} else {
				layer.Asset = assetPaths[ref.ID]
			}
			layer.Loop = bg.Loop
		}
		plan.Layers = append(plan.Layers, layer)
	}
	for _, item := range src.Items {
		for _, ref := range item.Assets {
			if ref.ID == "" || len(ref.SHA256) != 64 || strings.Trim(ref.SHA256, "0123456789abcdefABCDEF") != "" {
				return nil, nil, fmt.Errorf("overlay: item %q has invalid asset ref %q", item.ID, ref.ID)
			}
			path := assetPaths[ref.ID]
			if path != "" {
				for _, existing := range assets {
					if existing.LogicalPath == path && !strings.EqualFold(existing.Hash, ref.SHA256) {
						return nil, nil, fmt.Errorf("overlay: asset_id %q is associated with multiple SHA-256 values", ref.ID)
					}
				}
			} else {
				path = semanticAssetPath(ref)
				assetPaths[ref.ID] = path
				assets = append(assets, Asset{Hash: strings.ToLower(ref.SHA256), LogicalPath: path})
			}
		}
	}

	// Source clip — lowers to a full-canvas video layer. When foreground_scale
	// is set the source is scaled and centered on the canvas.
	var sourceLayerIndex = -1
	if src.Source != nil && src.Source.AssetID != "" {
		path := src.Source.Path
		if path == "" {
			path = semanticAssetPath(semanticAssetRef{ID: src.Source.AssetID, SHA256: src.Source.SHA256})
		}
		if _, ok := assetPaths[src.Source.AssetID]; !ok {
			assetPaths[src.Source.AssetID] = path
			assets = append(assets, Asset{Hash: strings.ToLower(src.Source.SHA256), LogicalPath: path})
		}
		srcLayer := concreteLayer{ID: "source", Type: "video", Source: path, StartFrame: 0}
		// Foreground scale: compute scaled geometry and center on canvas.
		// ForegroundScale == 0 or 100 means full-canvas (no scaling).
		if src.ForegroundScale > 0 && src.ForegroundScale < 100 {
			scaledW := int(math.Round(float64(src.Width) * float64(src.ForegroundScale) / 100))
			scaledH := int(math.Round(float64(src.Height) * float64(src.ForegroundScale) / 100))
			offsetX := float64(src.Width-scaledW) / 2
			offsetY := float64(src.Height-scaledH) / 2
			srcLayer.Size = []float64{float64(scaledW), float64(scaledH)}
			srcLayer.Position = []float64{offsetX, offsetY}
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
		ref := sub.AssetRefs[0]
		if ref.ID == "" || len(ref.SHA256) != 64 || strings.Trim(ref.SHA256, "0123456789abcdefABCDEF") != "" {
			return nil, nil, fmt.Errorf("overlay: subtitle has invalid asset ref %q", ref.ID)
		}
		path := assetPaths[ref.ID]
		if path == "" {
			path = semanticAssetPath(ref)
			assetPaths[ref.ID] = path
			assets = append(assets, Asset{Hash: strings.ToLower(ref.SHA256), LogicalPath: path})
		}
		_ = path // retained in the asset manifest for publication
	}

	// Watermark — lowers to a text or image layer at the requested position.
	if wm := src.Watermark; wm != nil {
		font := ""
		if wm.FontRef != nil {
			if wm.FontRef.ID == "" || len(wm.FontRef.SHA256) != 64 || strings.Trim(wm.FontRef.SHA256, "0123456789abcdefABCDEF") != "" {
				return nil, nil, fmt.Errorf("overlay: watermark font has invalid asset ref %q", wm.FontRef.ID)
			}
			font = assetPaths[wm.FontRef.ID]
			if font == "" {
				font = semanticAssetPath(*wm.FontRef)
				assetPaths[wm.FontRef.ID] = font
				assets = append(assets, Asset{Hash: strings.ToLower(wm.FontRef.SHA256), LogicalPath: font})
			}
		}
		if font == "" {
			return nil, nil, fmt.Errorf("overlay: text watermark requires font_ref")
		}
		wmLayer := concreteLayer{ID: "watermark", StartFrame: 0,
			Style: &concreteStyle{Font: font, FontSize: 42, Fill: "#FFFFFF"}}
		if wm.Opacity != nil {
			wmLayer.Opacity = *wm.Opacity
		}
		// Resolve position → concrete [x, y] pixel coordinate.
		wmLayer.Position = resolveWatermarkPosition(wm.Position, src.Width, src.Height)
		if wm.Text != "" && len(wm.AssetRefs) == 0 {
			// Text-only watermark.
			wmLayer.Type = "text"
			wmLayer.Text = wm.Text
		} else if len(wm.AssetRefs) > 0 {
			ref := wm.AssetRefs[0]
			if ref.ID == "" || len(ref.SHA256) != 64 || strings.Trim(ref.SHA256, "0123456789abcdefABCDEF") != "" {
				return nil, nil, fmt.Errorf("overlay: watermark has invalid asset ref %q", ref.ID)
			}
			path := assetPaths[ref.ID]
			if path == "" {
				path = semanticAssetPath(ref)
				assetPaths[ref.ID] = path
				assets = append(assets, Asset{Hash: strings.ToLower(ref.SHA256), LogicalPath: path})
			}
			wmLayer.Type = "image"
			wmLayer.Asset = path
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
			img := imageLayer(item, start, end, assetPaths[item.Assets[0].ID], params)
			plan.Layers = append(plan.Layers, img)
		}
		text := item.Text
		if text == "" {
			text = entityRefText(item)
		}
		layer := concreteLayer{ID: item.ID, Type: "text", Text: text, StartFrame: start, DurationFrames: end - start}
		if isImageTemplate(item.Template) {
			layer = imageLayer(item, start, end, assetPaths[item.Assets[0].ID], params)
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
			layer.Animation = animationForDefinition(definition)
		}
		if layer.Style != nil && layer.Position == nil {
			posX, hasPosX := params["position_x"].(float64)
			posY, hasPosY := params["position_y"].(float64)
			if hasPosX && hasPosY {
				layer.Position = []float64{posX, posY}
			} else {
				layer.Position = resolveLayout(definition.Layout, layer.BoxWidth, layer.BoxHeight, src.Width, src.Height)
				if hasPosX {
					layer.Position[0] = posX
				}
				if hasPosY {
					layer.Position[1] = posY
				}
			}
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
	// Stable asset order makes prepared-plan fingerprints reproducible.
	sort.Slice(assets, func(i, j int) bool { return assets[i].LogicalPath < assets[j].LogicalPath })
	compiled, err := json.Marshal(plan)
	if err != nil {
		return nil, nil, fmt.Errorf("overlay: encode Chronon plan: %w", err)
	}
	return compiled, assets, nil
}

// resolveWatermarkPosition converts a semantic position name to a concrete
// [x, y] pixel coordinate on the canvas. The coordinate is the top-left
// corner of a nominal 200×80 watermark box; callers with different sizes
// should override via the layer's Size field.
func resolveWatermarkPosition(position string, canvasW, canvasH int) []float64 {
	const wmW, wmH, margin = 200, 80, 24
	switch strings.ToLower(strings.TrimSpace(position)) {
	case "top_left":
		return []float64{margin, margin}
	case "top_right":
		return []float64{float64(canvasW - wmW - margin), margin}
	case "center":
		return []float64{float64(canvasW-wmW) / 2, float64(canvasH-wmH) / 2}
	case "bottom_left":
		return []float64{margin, float64(canvasH - wmH - margin)}
	case "bottom_right":
		return []float64{float64(canvasW - wmW - margin), float64(canvasH - wmH - margin)}
	default:
		// Unknown position: center as safe fallback.
		return []float64{float64(canvasW-wmW) / 2, float64(canvasH-wmH) / 2}
	}
}

func animationForMotion(id string, params map[string]any, textValue string, duration int64) (*concreteAnimation, error) {
	plugin, err := motion.Registry.Resolve(id)
	if err != nil {
		return nil, fmt.Errorf("overlay: %w", err)
	}
	tracks, err := plugin.Compile(motion.MotionContext{Text: textValue, DurationFrames: duration}, motion.MotionParams(params))
	if err != nil {
		return nil, fmt.Errorf("overlay: compile motion %q: %w", id, err)
	}
	animation := &concreteAnimation{Tracks: fromMotionTracks(tracks)}
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

func validateTextMotion(animators []concreteTextAnimator, id string) error {
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
	case "PERSON", "ORGANIZATION", "LOCATION":
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

func semanticAssetPath(ref semanticAssetRef) string {
	if strings.HasPrefix(ref.URL, "assets/") {
		return filepath.ToSlash(ref.URL)
	}
	id := safeAssetID.ReplaceAllString(ref.ID, "_")
	ext := filepath.Ext(ref.URL)
	if ext == "" {
		switch strings.ToLower(ref.MediaType) {
		case "image/png":
			ext = ".png"
		case "image/jpeg":
			ext = ".jpg"
		case "video/mp4":
			ext = ".mp4"
		case "font/ttf":
			ext = ".ttf"
		}
	}
	return "assets/semantic/" + id + ext
}
func imageLayer(item semanticItem, start, end int64, asset string, params map[string]any) concreteLayer {
	w, h := intParam(params, "width", 320), intParam(params, "height", 320)
	return concreteLayer{ID: item.ID + "_image", Type: "image", Asset: asset, BoxWidth: w, BoxHeight: h, Size: []float64{float64(w), float64(h)}, Fit: stringParam(params, "fit", "contain"), StartFrame: start, DurationFrames: end - start}
}

func applyPresetDefinition(layer *concreteLayer, d OfficialPresetDefinition) {
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
	layer.Style = &concreteStyle{Font: d.Style.FontFamily, FontSize: d.Style.FontSize, Fill: rgbaHex(d.Style.Fill)}
	if d.Style.Shadow != nil {
		s := d.Style.Shadow
		layer.Style.Shadow = &concreteShadow{Color: s.Color, Opacity: s.Opacity, Blur: s.Blur, Offset: append([]float64(nil), s.Offset...)}
	}
}

func animationForDefinition(d OfficialPresetDefinition) *concreteAnimation {
	if d.Motion.Name == "" {
		return nil
	}
	return &concreteAnimation{Tracks: tracksForMotion(d.Motion)}
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
