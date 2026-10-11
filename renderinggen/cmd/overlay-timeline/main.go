// Command overlay-timeline fuses N sequential overlays into ONE multi-item
// semantic plan — one render, one MP4 — with the concatenation rule the
// vidrush scenario certifies: overlay k+1 starts on the frame where overlay k
// ends, plus an optional uniform gap quantized to whole frames.
//
// Each timeline row declares its own duration_ms and the ordinary item fields
// (kind, template_id, preset_id, motion_id, text/entity/caption, asset_refs
// or the asset shorthand, image_layers, map, params, style). Rows travel
// through the typed renderbatch writer and the worker's own compiler, so an
// unknown kind/preset/motion/font fails here, before any render.
//
// Usage:
//
//	go run ./cmd/overlay-timeline -timeline timeline.json -out-dir /tmp/out
//
// Timeline file:
//
//	{
//	  "plan_id": "reel", "width": 1280, "height": 720,
//	  "fps_num": 30, "fps_den": 1, "gap_ms": 0,
//	  "rows": [{"duration_ms": 2000, "template_id": "IMPORTANT_PHRASE",
//	             "preset_id": "phrase_default", "text": "Hello"}]
//	}
//
// Output (in -out-dir): semantic_plan.json, render_plan.json and
// timeline.json (per-row ms/frame windows, so verification never re-renders).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
)

// assetShorthand is one content-addressed asset on a row.
type assetShorthand struct {
	AssetID   string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	MediaType string `json:"media_type"`
}

// timelineRow is one overlay row: the ordinary item fields plus its own
// duration. Windows are assigned by the builder, never authored.
type timelineRow struct {
	DurationMS        int64                        `json:"duration_ms"`
	SceneID           string                       `json:"scene_id"`
	Kind              string                       `json:"kind"`
	TemplateID        string                       `json:"template_id"`
	PresetID          string                       `json:"preset_id"`
	ImagePresetID     string                       `json:"image_preset_id"`
	MotionID          string                       `json:"motion_id"`
	EntityID          string                       `json:"entity_id"`
	Text              string                       `json:"text"`
	EntityCaption     string                       `json:"entity_caption"`
	CaptionMotionID   string                       `json:"caption_motion_id"`
	CaptionLayout     string                       `json:"caption_layout"`
	CaptionFontFamily string                       `json:"caption_font_family"`
	CaptionColor      string                       `json:"caption_color"`
	Asset             *assetShorthand              `json:"asset"`
	AssetRefs         []renderbatch.PlanAssetRef   `json:"asset_refs"`
	ImageLayers       []overlay.SemanticImageLayer `json:"image_layers"`
	Map               *overlay.SemanticMap         `json:"map"`
	MotionParams      map[string]any               `json:"motion_params"`
	Params            map[string]any               `json:"params"`
	Style             map[string]any               `json:"style"`
}

// timelineFile is the -timeline document.
type timelineFile struct {
	PlanID    string        `json:"plan_id"`
	ProjectID string        `json:"project_id"`
	Language  string        `json:"language"`
	Width     int           `json:"width"`
	Height    int           `json:"height"`
	FPSNum    int           `json:"fps_num"`
	FPSDen    int           `json:"fps_den"`
	GapMS     int64         `json:"gap_ms"`
	Rows      []timelineRow `json:"rows"`
}

// timelineWindow is one row's assigned window, the verification record.
type timelineWindow struct {
	Index      int    `json:"index"`
	ItemID     string `json:"item_id"`
	StartMS    int64  `json:"start_ms"`
	EndMS      int64  `json:"end_ms"`
	StartFrame int64  `json:"start_frame"`
	EndFrame   int64  `json:"end_frame"`
}

func main() {
	timelinePath := flag.String("timeline", "", "timeline JSON file (required)")
	outDir := flag.String("out-dir", "", "output directory (required)")
	flag.Parse()
	if err := run(*timelinePath, *outDir); err != nil {
		fmt.Fprintln(os.Stderr, "overlay-timeline:", err)
		os.Exit(1)
	}
}

func run(timelinePath, outDir string) error {
	if timelinePath == "" || outDir == "" {
		return fmt.Errorf("-timeline and -out-dir are required")
	}
	raw, err := os.ReadFile(timelinePath)
	if err != nil {
		return fmt.Errorf("read timeline: %w", err)
	}
	var file timelineFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("decode timeline: %w", err)
	}
	spec, windows, err := buildTimeline(file)
	if err != nil {
		return err
	}
	semantic, err := renderbatch.BuildPlan(spec)
	if err != nil {
		return fmt.Errorf("build fused plan: %w", err)
	}
	rendered, err := renderbatch.CompileRenderPlan(semantic)
	if err != nil {
		return fmt.Errorf("compile fused plan: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "semantic_plan.json"), append(semantic, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "render_plan.json"), rendered, 0o600); err != nil {
		return err
	}
	windowsRaw, err := json.MarshalIndent(map[string]any{"plan_id": spec.PlanID, "windows": windows}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "timeline.json"), append(windowsRaw, '\n'), 0o600); err != nil {
		return err
	}
	return nil
}

// buildTimeline assigns strict frame-grid windows: row k+1 starts where row k
// ends plus the uniform gap. All math runs in whole frames so the fused plan
// can never carry a half-frame seam.
func buildTimeline(file timelineFile) (renderbatch.PlanSpec, []timelineWindow, error) {
	if file.PlanID == "" {
		return renderbatch.PlanSpec{}, nil, fmt.Errorf("timeline plan_id is required")
	}
	width, height := file.Width, file.Height
	if width == 0 && height == 0 {
		width, height = 1280, 720
	}
	fpsNum, fpsDen := file.FPSNum, file.FPSDen
	if fpsNum == 0 && fpsDen == 0 {
		fpsNum, fpsDen = 30, 1
	}
	if fpsNum <= 0 || fpsDen <= 0 {
		return renderbatch.PlanSpec{}, nil, fmt.Errorf("timeline needs positive fps, got %d/%d", fpsNum, fpsDen)
	}
	if len(file.Rows) == 0 {
		return renderbatch.PlanSpec{}, nil, fmt.Errorf("timeline declares no rows")
	}
	if file.GapMS < 0 {
		return renderbatch.PlanSpec{}, nil, fmt.Errorf("timeline gap_ms must be >= 0")
	}
	msPerFrame := 1000.0 * float64(fpsDen) / float64(fpsNum)
	gapFrames := int64(math.Round(float64(file.GapMS) / msPerFrame))
	spec := renderbatch.PlanSpec{
		PlanID: file.PlanID, ProjectID: file.ProjectID, Language: file.Language,
		Width: width, Height: height, FPSNum: fpsNum, FPSDen: fpsDen,
	}
	windows := make([]timelineWindow, 0, len(file.Rows))
	frame := int64(0)
	for i, row := range file.Rows {
		if row.DurationMS <= 0 {
			return renderbatch.PlanSpec{}, nil, fmt.Errorf("timeline row %d needs positive duration_ms", i)
		}
		if row.TemplateID == "" {
			return renderbatch.PlanSpec{}, nil, fmt.Errorf("timeline row %d needs template_id", i)
		}
		frames := int64(math.Round(float64(row.DurationMS) / msPerFrame))
		if frames < 1 {
			frames = 1
		}
		startFrame := frame
		endFrame := startFrame + frames
		startMS := int64(math.Round(float64(startFrame) * msPerFrame))
		endMS := int64(math.Round(float64(endFrame) * msPerFrame))
		id := fmt.Sprintf("timeline-%d", i)
		refs := row.AssetRefs
		if row.Asset != nil {
			refs = append([]renderbatch.PlanAssetRef{{
				AssetID: row.Asset.AssetID, SHA256: row.Asset.SHA256,
				URL: row.Asset.URL, MediaType: row.Asset.MediaType,
			}}, refs...)
		}
		duration := endMS - startMS
		spec.Items = append(spec.Items, renderbatch.PlanItem{
			ID: id, SceneID: row.SceneID, TemplateID: row.TemplateID,
			PresetID: row.PresetID, ImagePresetID: row.ImagePresetID,
			MotionID: row.MotionID, Kind: row.Kind, EntityID: row.EntityID,
			Text: row.Text, EntityCaption: row.EntityCaption,
			CaptionMotionID: row.CaptionMotionID, CaptionLayout: row.CaptionLayout,
			CaptionFontFamily: row.CaptionFontFamily, CaptionColor: row.CaptionColor,
			DurationMS: &duration, AssetRefs: refs, ImageLayers: row.ImageLayers,
			Map: row.Map, MotionParams: row.MotionParams, Params: row.Params,
			Style: row.Style, StartMS: startMS, EndMS: endMS,
		})
		windows = append(windows, timelineWindow{
			Index: i, ItemID: id, StartMS: startMS, EndMS: endMS,
			StartFrame: startFrame, EndFrame: endFrame,
		})
		frame = endFrame + gapFrames
	}
	// The canvas ends where the last row ends; a trailing gap extends nothing.
	spec.DurationMS = windows[len(windows)-1].EndMS
	return spec, windows, nil
}
