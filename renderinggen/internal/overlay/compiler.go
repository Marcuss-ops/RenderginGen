// Package overlay owns the RenderingGen-side bridge from the semantic overlay
// contract emitted by PipelineGen to the concrete plan consumed by Chronon.
package overlay

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"strings"
)

const SemanticSchema = "renderinggen.overlay-plan.v1"

type semanticPlan struct {
	SchemaVersion string         `json:"schema_version"`
	PlanID        string         `json:"plan_id"`
	VideoID       string         `json:"video_id"`
	Width         int            `json:"width"`
	Height        int            `json:"height"`
	FPS           int            `json:"fps"`
	Renderer      string         `json:"renderer_version,omitempty"`
	Items         []semanticItem `json:"items"`
}

type semanticItem struct {
	ID         string          `json:"id"`
	TemplateID string          `json:"template_id"`
	Text       string          `json:"text,omitempty"`
	StartMS    int64           `json:"start_ms"`
	EndMS      int64           `json:"end_ms"`
	Params     map[string]any  `json:"params,omitempty"`
	Assets     []semanticAsset `json:"asset_refs,omitempty"`
}

type semanticAsset struct {
	AssetID   string `json:"asset_id"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type,omitempty"`
}

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
	FPS            int   `json:"fps"`
	DurationFrames int64 `json:"duration_frames"`
}

type Layer struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	Asset          string    `json:"asset,omitempty"`
	Text           string    `json:"text,omitempty"`
	Preset         string    `json:"preset,omitempty"`
	BoxWidth       int       `json:"box_width,omitempty"`
	BoxHeight      int       `json:"box_height,omitempty"`
	Fit            string    `json:"fit,omitempty"`
	Position       []float64 `json:"position,omitempty"`
	StartFrame     int64     `json:"start_frame"`
	DurationFrames int64     `json:"duration_frames"`
}

type Output struct {
	Path   string `json:"path"`
	Format string `json:"format"`
	Codec  string `json:"codec"`
}

type Asset struct {
	Hash        string `json:"hash"`
	LogicalPath string `json:"logical_path"`
}

// CompileIfSemantic returns the input unchanged for an already-concrete
// Chronon plan. For a PipelineGen semantic plan it returns the concrete plan
// and the content-addressed assets that must be materialized first.
func CompileIfSemantic(raw []byte) ([]byte, []Asset, bool, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, nil, false, fmt.Errorf("overlay: decode plan: %w", err)
	}
	if probe.SchemaVersion == "" {
		return raw, nil, false, nil
	}
	if probe.SchemaVersion != SemanticSchema {
		return nil, nil, true, fmt.Errorf("overlay: unsupported semantic schema %q", probe.SchemaVersion)
	}
	var src semanticPlan
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, nil, true, fmt.Errorf("overlay: decode semantic plan: %w", err)
	}
	if err := validate(src); err != nil {
		return nil, nil, true, err
	}
	frameAt := func(ms int64) int64 { return int64(math.Round(float64(ms) * float64(src.FPS) / 1000)) }
	var maxEnd int64
	for _, item := range src.Items {
		if item.EndMS > maxEnd {
			maxEnd = item.EndMS
		}
	}
	plan := Plan{
		Schema: "chronon.render-plan", Version: 1, JobID: src.PlanID,
		Canvas: Canvas{Width: src.Width, Height: src.Height, FPS: src.FPS, DurationFrames: frameAt(maxEnd)},
		Output: Output{Path: "result.mp4", Format: "mp4", Codec: "h264"},
	}
	var assets []Asset
	seenAssets := map[string]bool{}
	for _, item := range src.Items {
		layer, err := compileLayer(src, item, frameAt)
		if err != nil {
			return nil, nil, true, err
		}
		plan.Layers = append(plan.Layers, layer)
		for _, ref := range item.Assets {
			if ref.SHA256 == "" || seenAssets[ref.SHA256] {
				continue
			}
			seenAssets[ref.SHA256] = true
			assets = append(assets, Asset{Hash: ref.SHA256, LogicalPath: assetPath(ref.URL)})
		}
	}
	b, err := json.Marshal(plan)
	if err != nil {
		return nil, nil, true, fmt.Errorf("overlay: encode Chronon plan: %w", err)
	}
	return b, assets, true, nil
}

func validate(p semanticPlan) error {
	if p.PlanID == "" || p.VideoID == "" || p.Width <= 0 || p.Height <= 0 || p.FPS <= 0 {
		return fmt.Errorf("overlay: semantic plan identity and canvas are required")
	}
	if p.Renderer != "" && p.Renderer != "chronon" {
		return fmt.Errorf("overlay: unsupported renderer %q", p.Renderer)
	}
	knownTemplates := map[string]bool{
		"IMPORTANT_PHRASE": true,
		"IMPORTANT_WORD":   true,
		"IMAGE_OVERLAY":    true,
		"BACKGROUND":       true,
	}
	// Templates that render an asset source; an item of these kinds without a
	// resolvable asset must be rejected before rendering, never silently fixed.
	assetTemplates := map[string]bool{
		"IMAGE_OVERLAY": true,
		"BACKGROUND":    true,
	}
	seen := map[string]bool{}
	for _, item := range p.Items {
		if item.ID == "" || item.TemplateID == "" || seen[item.ID] {
			return fmt.Errorf("overlay: invalid or duplicate item %q", item.ID)
		}
		seen[item.ID] = true
		if !knownTemplates[item.TemplateID] {
			return fmt.Errorf("overlay: unsupported template %q", item.TemplateID)
		}
		if item.StartMS < 0 || item.EndMS <= item.StartMS {
			return fmt.Errorf("overlay: invalid timing for item %q", item.ID)
		}
		if assetTemplates[item.TemplateID] && len(item.Assets) == 0 {
			return fmt.Errorf("overlay: item %q (%s) requires an asset source", item.ID, item.TemplateID)
		}
		for _, ref := range item.Assets {
			if ref.AssetID == "" {
				return fmt.Errorf("overlay: item %q has asset without asset_id", item.ID)
			}
			if ref.SHA256 == "" {
				return fmt.Errorf("overlay: item %q has asset %q without sha256", item.ID, ref.AssetID)
			}
		}
	}
	if len(p.Items) == 0 {
		return fmt.Errorf("overlay: semantic plan has no items")
	}
	return nil
}

func compileLayer(p semanticPlan, item semanticItem, frameAt func(int64) int64) (Layer, error) {
	start, end := frameAt(item.StartMS), frameAt(item.EndMS)
	if end <= start {
		return Layer{}, fmt.Errorf("overlay: item %q rounds to empty frame range", item.ID)
	}
	layer := Layer{ID: item.ID, StartFrame: start, DurationFrames: end - start}
	switch item.TemplateID {
	case "IMPORTANT_PHRASE":
		layer.Type, layer.Preset = "text", "title_centered"
	case "IMPORTANT_WORD":
		layer.Type, layer.Preset = "text", "kinetic_word"
	case "IMAGE_OVERLAY":
		layer.Type, layer.Fit, layer.BoxWidth, layer.BoxHeight, layer.Position = "image", "contain", 260, 260, []float64{380, 0}
	case "BACKGROUND":
		layer.Type, layer.Fit, layer.BoxWidth, layer.BoxHeight = "image", "cover", p.Width, p.Height
		layer.StartFrame, layer.DurationFrames = 0, frameAt(maxEnd(p.Items))
	default:
		return Layer{}, fmt.Errorf("overlay: unsupported template %q", item.TemplateID)
	}
	layer.Text = item.Text
	if len(item.Assets) > 0 {
		layer.Asset = assetPath(item.Assets[0].URL)
	}
	return layer, nil
}

func maxEnd(items []semanticItem) int64 {
	var end int64
	for _, item := range items {
		if item.EndMS > end {
			end = item.EndMS
		}
	}
	return end
}
func assetPath(raw string) string {
	base := path.Base(strings.TrimSpace(raw))
	if base == "." || base == "/" || base == "" {
		base = "asset"
	}
	return "assets/" + base
}
