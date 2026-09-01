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
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SemanticSchema is PipelineGen's semantic overlay contract. RenderingGen
// compiles it mechanically; PipelineGen remains the owner of its decisions.
const SemanticSchema = "renderinggen.overlay-plan.v1"

// Plan is the concrete chronon.render-plan.v1 document the worker executes.
// It is carried here so callers/tests can decode a submitted plan; the worker
// itself treats it as an opaque blob passed straight to Chronon.
type Plan struct {
	Schema       string  `json:"schema"`
	Version      int     `json:"version"`
	JobID        string  `json:"job_id"`
	StyleProfile string  `json:"style_profile,omitempty"`
	Canvas       Canvas  `json:"canvas"`
	Layers       []Layer `json:"layers"`
	Output       Output  `json:"output"`
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
	Source string `json:"source,omitempty"`
	Text   string `json:"text,omitempty"`
	// Preset is authoring/debug metadata in the in-memory DTO only; concrete
	// serialized plans omit it after full lowering.
	Preset         string    `json:"preset,omitempty"`
	Font           string    `json:"font,omitempty"`
	FontSize       float64   `json:"font_size,omitempty"`
	BoxWidth       int       `json:"box_width,omitempty"`
	BoxHeight      int       `json:"box_height,omitempty"`
	Fit            string    `json:"fit,omitempty"`
	Position       []float64 `json:"position,omitempty"`
	Color          []float64 `json:"color,omitempty"`
	Opacity        float64   `json:"opacity,omitempty"`
	BlendMode      string    `json:"blend_mode,omitempty"`
	Loop           bool      `json:"loop,omitempty"`
	StartFrame     int64     `json:"start_frame"`
	DurationFrames int64     `json:"duration_frames"`
	// Animation is the motion preset applied to the layer.
	Animation *LayerAnimation `json:"animation,omitempty"`
}

// AnimationTrack is the renderer-neutral motion contract produced by
// RenderingGen after selector/stagger expansion.
type AnimationTrack struct {
	Property  string              `json:"property"`
	Keyframes []AnimationKeyframe `json:"keyframes"`
	Easing    string              `json:"easing,omitempty"`
}

type AnimationKeyframe struct {
	Frame int64   `json:"frame"`
	Value float64 `json:"value"`
}

// LayerAnimation contains only generic tracks. The optional preset label is
// debug metadata and is not an executable renderer instruction.
type LayerAnimation struct {
	Preset              string           `json:"preset,omitempty"`
	Unit                string           `json:"unit,omitempty"`
	EnterDurationFrames int              `json:"enter_duration_frames,omitempty"`
	ExitDurationFrames  int              `json:"exit_duration_frames,omitempty"`
	Tracks              []AnimationTrack `json:"tracks,omitempty"`
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
		// Concrete chronon.render-plan.v1 → pass through untouched.
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
	SchemaVersion   string              `json:"schema_version"`
	PlanID          string              `json:"plan_id"`
	VideoID         string              `json:"video_id"`
	Width           int                 `json:"width"`
	Height          int                 `json:"height"`
	FPSNum          int                 `json:"fps_num"`
	FPSDen          int                 `json:"fps_den"`
	OutputProfileID string              `json:"output_profile_id"`
	StyleProfile    string              `json:"style_profile"`
	Background      *semanticBackground `json:"background,omitempty"`
	Items           []semanticItem      `json:"items"`
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
	PresetID string             `json:"preset_id"`
	Text     string             `json:"text"`
	StartMS  int64              `json:"start_ms"`
	EndMS    int64              `json:"end_ms"`
	Params   map[string]any     `json:"params"`
	Assets   []semanticAssetRef `json:"asset_refs"`
}

// semanticEntityRef is the plan's entity_ref block: the content-addressed
// entity identity of the item. The compiler only uses it to fall back to a
// display text when the item carries no explicit text — it never performs
// entity linking or ranking.
type semanticEntityRef struct {
	EntityID string `json:"entity_id"`
	Type     string `json:"type"`
	// Name is the new contract's canonical name spelling; CanonicalName is
	// the legacy spelling. Both are accepted so old plans keep compiling.
	Name          string `json:"name"`
	CanonicalName string `json:"canonical_name"`
	SurfaceText   string `json:"surface_text,omitempty"`
}

type semanticAssetRef struct {
	ID        string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	MediaType string `json:"media_type"`
}

type concretePlan struct {
	Schema  string          `json:"schema"`
	Version int             `json:"version"`
	JobID   string          `json:"job_id"`
	Style   string          `json:"style_profile,omitempty"`
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
}
type concreteLayer struct {
	// Preset is retained only in the in-memory test/diagnostic DTO and is
	// excluded from JSON serialization after full lowering.
	Preset         string             `json:"-"`
	ID             string             `json:"id"`
	Type           string             `json:"type"`
	Asset          string             `json:"asset,omitempty"`
	Source         string             `json:"source,omitempty"`
	Color          []float64          `json:"color,omitempty"`
	Text           string             `json:"text,omitempty"`
	Font           string             `json:"font,omitempty"`
	FontSize       float64            `json:"font_size,omitempty"`
	BoxWidth       int                `json:"box_width,omitempty"`
	BoxHeight      int                `json:"box_height,omitempty"`
	Fit            string             `json:"fit,omitempty"`
	Position       []float64          `json:"position,omitempty"`
	StartFrame     int64              `json:"start_frame"`
	DurationFrames int64              `json:"duration_frames"`
	Animation      *concreteAnimation `json:"animation,omitempty"`
	Anchor         *concreteAnchor    `json:"anchor,omitempty"`
	Style          *concreteStyle     `json:"style,omitempty"`
	Opacity        float64            `json:"opacity,omitempty"`
	Loop           bool               `json:"loop,omitempty"`
}
type concreteAnchor struct {
	Type       string  `json:"type"`
	SafeMargin float64 `json:"safe_margin,omitempty"`
	Alignment  string  `json:"alignment,omitempty"`
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
type concreteStyle struct {
	FontFamily string              `json:"font_family,omitempty"`
	FontWeight int                 `json:"font_weight,omitempty"`
	FontSize   float64             `json:"font_size,omitempty"`
	Fill       string              `json:"fill,omitempty"`
	Stroke     *concreteStroke     `json:"stroke,omitempty"`
	Shadow     *concreteShadow     `json:"shadow,omitempty"`
	Background *concreteBackground `json:"background,omitempty"`
}
type concreteAnimation struct {
	// Preset is debug metadata; Tracks are the executable renderer contract.
	Preset string           `json:"preset,omitempty"`
	Unit   string           `json:"unit,omitempty"`
	Enter  int              `json:"enter_duration_frames,omitempty"`
	Exit   int              `json:"exit_duration_frames,omitempty"`
	Tracks []AnimationTrack `json:"tracks,omitempty"`
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
	if len(src.Items) == 0 {
		return nil, nil, fmt.Errorf("overlay: semantic plan has no items")
	}
	if src.StyleProfile == "" {
		src.StyleProfile = "discovery"
	}
	if src.StyleProfile != "discovery" && src.StyleProfile != "young" && src.StyleProfile != "crime" {
		return nil, nil, fmt.Errorf("overlay: unsupported style_profile %q", src.StyleProfile)
	}

	plan := concretePlan{Schema: "chronon.render-plan", Version: 1, JobID: src.PlanID, Style: src.StyleProfile,
		Canvas: concreteCanvas{Width: src.Width, Height: src.Height, FPSNum: src.FPSNum, FPSDen: src.FPSDen},
		Output: concreteOutput{Path: "result.mp4", Format: "mp4", Codec: "h264", ProfileID: src.OutputProfileID}}
	assetPaths := map[string]string{}
	var assets []Asset
	if bg := src.Background; bg != nil {
		kind := strings.ToLower(strings.TrimSpace(bg.Kind))
		if kind != "color" && kind != "image" && kind != "video" {
			return nil, nil, fmt.Errorf("overlay: unsupported background kind %q", bg.Kind)
		}
		if kind == "color" {
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
		layer := concreteLayer{ID: "background", Type: kind, BoxWidth: src.Width, BoxHeight: src.Height,
			Fit: bg.Fit, StartFrame: 0}
		if kind != "color" && layer.Fit == "" {
			layer.Fit = "cover"
		}
		if bg.Opacity != nil {
			layer.Opacity = *bg.Opacity
		}
		if kind == "color" {
			layer.Color = append([]float64(nil), bg.Color...)
		} else {
			ref := bg.AssetRefs[0]
			if kind == "video" {
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
		if isImageTemplate(item.Template) && len(item.Assets) == 0 {
			return nil, nil, fmt.Errorf("overlay: image template %q item %q requires asset_refs", item.Template, item.ID)
		}
		if isEntityTemplate(item.Template) && len(item.Assets) > 0 {
			imagePreset, err := presetForImage(item)
			if err != nil {
				return nil, nil, err
			}
			img := imageLayer(item, start, end, imagePreset, assetPaths[item.Assets[0].ID], params)
			if imageDefinition, imageErr := resolveOfficialPreset(imagePreset, string(PresetImage)); imageErr == nil {
				applyPresetDefinition(&img, imageDefinition)
			}
			plan.Layers = append(plan.Layers, img)
		}
		text := item.Text
		if text == "" {
			text = entityRefText(item)
		}
		layer := concreteLayer{ID: item.ID, Type: "text", Text: text, StartFrame: start, DurationFrames: end - start}
		if isImageTemplate(item.Template) {
			layer = imageLayer(item, start, end, preset, assetPaths[item.Assets[0].ID], params)
		}
		if preset != "" {
			applyPresetDefinition(&layer, definition)
		}
		if anim := animationFor(params); anim != nil {
			layer.Animation = anim
		} else if preset != "" {
			layer.Animation = animationForDefinition(definition)
		}
		plan.Layers = append(plan.Layers, layer)
		if end > plan.Canvas.DurationFrames {
			plan.Canvas.DurationFrames = end
		}
	}
	if len(plan.Layers) > 0 && plan.Layers[0].ID == "background" {
		plan.Layers[0].DurationFrames = plan.Canvas.DurationFrames
	}
	if plan.Canvas.DurationFrames <= 0 {
		return nil, nil, fmt.Errorf("overlay: semantic plan duration is zero")
	}
	// Stable asset order makes prepared-plan fingerprints reproducible.
	sort.Slice(assets, func(i, j int) bool { return assets[i].LogicalPath < assets[j].LogicalPath })
	compiled, err := json.Marshal(plan)
	if err != nil {
		return nil, nil, fmt.Errorf("overlay: encode Chronon plan: %w", err)
	}
	return compiled, assets, nil
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
	// The plan's preset_id contract slot is preferred; the params slot is the
	// legacy spelling. RenderingGen's official catalog is authoritative.
	//
	// ADR-029 forward-point (d): RenderingGen is an execution worker and must
	// NOT re-map a template_id to a preset (e.g. it must not know that PERSON
	// means lower_third_safe). The semantic_role → preset_id decision lives
	// only in PipelineGen's SemanticOverlayResolver. A preset-driven template
	// that does not carry a preset_id is rejected; preset-less primitives
	// (PRODUCT, LOGO, LIGHT_LEAK, …) legitimately compile without one.
	p := strings.TrimSpace(item.PresetID)
	if p == "" {
		if legacy, ok := item.Params["preset_id"].(string); ok {
			p = strings.TrimSpace(legacy)
		}
	}
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
func presetForImage(item semanticItem) (string, error) {
	if p, ok := item.Params["image_preset_id"].(string); ok && p != "" {
		return validatePreset(p, item.ID, "image")
	}
	return "image_focus_in", nil
}

// entityRefText returns the display text from the plan's entity_ref block:
// surface_text first (the verbatim spoken mention), then the canonical name
// (the new contract's `name`), then the legacy `canonical_name` spelling.
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
	return item.EntityRef.CanonicalName
}

func semanticAssetPath(ref semanticAssetRef) string {
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
func imageLayer(item semanticItem, start, end int64, preset, asset string, params map[string]any) concreteLayer {
	return concreteLayer{ID: item.ID + "_image", Type: "image", Asset: asset, BoxWidth: intParam(params, "width", 320), BoxHeight: intParam(params, "height", 320), Fit: stringParam(params, "fit", "contain"), StartFrame: start, DurationFrames: end - start}
}

func applyPresetDefinition(layer *concreteLayer, d OfficialPresetDefinition) {
	layer.Font = d.Font
	layer.FontSize = d.FontSize
	layer.Color = append([]float64(nil), d.Fill...)
	layer.Anchor = &concreteAnchor{Type: d.Anchor, SafeMargin: 0.06, Alignment: d.Alignment}
	if d.Family == PresetImage {
		if layer.BoxWidth == 320 && d.BoxWidth > 0 {
			layer.BoxWidth = d.BoxWidth
		}
		if layer.BoxHeight == 320 && d.BoxHeight > 0 {
			layer.BoxHeight = d.BoxHeight
		}
		if layer.Fit == "contain" && d.Fit != "" {
			layer.Fit = d.Fit
		}
		return
	}
	layer.Style = &concreteStyle{FontFamily: "Poppins", FontWeight: 700, FontSize: d.FontSize, Fill: "#FFFFFF"}
	if d.Glow {
		layer.Style.Shadow = &concreteShadow{Color: "#38BDF8", Opacity: 0.82, Blur: 14, Offset: []float64{0, 2}}
	}
}

func animationForDefinition(d OfficialPresetDefinition) *concreteAnimation {
	if d.Animation == "" {
		return nil
	}
	return &concreteAnimation{Preset: d.Animation, Unit: d.Unit, Enter: d.Enter, Exit: d.Exit, Tracks: tracksForAnimation(d.Animation, d.Unit, d.Enter)}
}
func animationFor(params map[string]any) *concreteAnimation {
	p, _ := params["animation"].(string)
	if p == "" {
		return nil
	}
	a := &concreteAnimation{Preset: p}
	a.Unit = stringParam(params, "unit", "layer")
	a.Enter = intParam(params, "enter_duration_frames", 0)
	a.Exit = intParam(params, "exit_duration_frames", 0)
	a.Tracks = tracksForAnimation(p, a.Unit, a.Enter)
	return a
}

func tracksForAnimation(animation, unit string, enter int) []AnimationTrack {
	if animation == "" || animation == "static" || enter <= 0 {
		return nil
	}
	property := "opacity"
	start := 0.0
	end := 1.0
	switch animation {
	case "scale_drop":
		property, start = "scale", 0.85
	case "reveal_from_bottom":
		property, start = "position_y", 40
	case "slide_in":
		property, start = "position_x", -40
	}
	if property == "position_x" || property == "position_y" {
		end = 0
	}
	return []AnimationTrack{{Property: property, Easing: "out_cubic", Keyframes: []AnimationKeyframe{{Frame: 0, Value: start}, {Frame: int64(enter), Value: end}}}}
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
