// Command overlay-timeline fuses N sequential overlays into ONE semantic
// overlay-plan (→ ONE MP4) instead of one job — and one overlay MP4 — per
// overlay.
//
// The problem it solves. The batch builders (overlaybatch.Build*Manifest) emit
// one single-item plan per overlay, so a cut with a phrase, a date, a metric
// and two entity cards renders as four separate MP4s that a downstream
// compositor must stack — and each overlay re-renders the same background. The
// renderer never required that split: the semantic compiler resolves N items,
// each with its own start_ms/end_ms window, and lowerMotion (motion.go) sizes
// every entrance (one third of the phrase lifetime) and exit
// (appendExitTracks) INSIDE the item's own window. The harmonic rule is
// therefore structural, not per-pair hand tuning: give every overlay the
// window it deserves and concatenate them — item k+1 starts on the frame item
// k ends — and every animation enters and leaves naturally, with no gap and
// no forced carry-over of the previous overlay into the next one.
//
// This command is the generic, data-driven version of cmd/vidrush-scenario:
// the scenario hard-codes its acts, this one reads a JSON timeline definition.
//
// Input (-timeline), one overlay per row, in playback order:
//
//	{
//	  "plan_id": "my-cut",
//	  "width": 1920, "height": 1080, "fps": 24,
//	  "background": {"kind": "color", "color": [0.98, 0.98, 0.97, 1]},
//	  "overlays": [
//	    {"kind": "important_phrase", "text": "QUESTO CAMBIA TUTTO",
//	     "motion_id": "apple_phrase_v2", "duration_ms": 2000},
//	    {"kind": "timeline_date", "template_id": "TIMELINE_DATE_CARD",
//	     "text": "13 September 2026", "motion_id": "date_timeline_sweep",
//	     "duration_ms": 3000},
//	    {"kind": "entity_card", "template_id": "PERSON",
//	     "entity_id": "person:ada", "text": "ADA LOVELACE",
//	     "entity_caption": "ADA LOVELACE", "caption_motion_id": "text_word_rise",
//	     "image_preset_id": "image_scale_in", "preset_id": "phrase_default",
//	     "motion_id": "image_depth_dolly",
//	     "asset": "assets/semantic/portrait.png", "duration_ms": 3000},
//	    {"kind": "entity_image", "template_id": "IMAGE_OVERLAY",
//	     "preset_id": "image_scale_in", "motion_id": "image_yaw_reveal",
//	     "asset": "assets/semantic/plate.png", "duration_ms": 3000,
//	     "params": {"width": 620.0, "height": 760.0}}
//	  ]
//	}
//
// Supported kinds: the full semantic item set the worker's compiler accepts —
// text (important_phrase, important_word, number, metric_stat, timeline_date,
// entity_card, organization, location, quote, …), entity_image (single asset),
// map (georeferenced declaration carried verbatim) and composite entity_image
// with explicit image_layers. Each row may override every contract field the
// worker accepts (preset_id, motion_id, params, style, caption_*).
//
// Output (-out-dir):
//
//	semantic_plan.json   the renderinggen.overlay-plan.v1 document (input of record)
//	render_plan.json     the compiled chronon.render-plan the renderer consumes
//	timeline.json        the timing descriptor: each overlay's ms/frame window,
//	                     so a verification script (or a human) can check the
//	                     chain without restating a single number
//
// Every overlay is validated by the worker's own decoder and compiler through
// renderbatch.BuildPlan: an unknown kind, preset, motion or font fails at
// build time, not at render time.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
)

const defaultFPS = 24

// timelineRow is one overlay of the timeline definition.
type timelineRow struct {
	ID        string `json:"id,omitempty"`
	Kind      string `json:"kind"`
	Template  string `json:"template_id,omitempty"`
	Preset    string `json:"preset_id,omitempty"`
	ImagePre  string `json:"image_preset_id,omitempty"`
	Motion    string `json:"motion_id,omitempty"`
	Text      string `json:"text,omitempty"`
	EntityID  string `json:"entity_id,omitempty"`
	EntityCap string `json:"entity_caption,omitempty"`
	// Caption block (entity cards with an animated caption).
	CaptionMotion string `json:"caption_motion_id,omitempty"`
	CaptionLayout string `json:"caption_layout,omitempty"`
	CaptionFont   string `json:"caption_font_family,omitempty"`
	CaptionColor  string `json:"caption_color,omitempty"`
	// Asset is the content-addressed media (entity portrait, image overlay,
	// map plate). Composite rows use layers instead.
	Asset string `json:"asset,omitempty"`
	// Layers are the independently animated children of a composite image row.
	Layers []layerRow `json:"image_layers,omitempty"`
	// Map carries the georeferenced declaration of a kind=map row verbatim.
	Map *overlay.SemanticMap `json:"map,omitempty"`
	// Params/Style transport the row's runtime controls verbatim.
	Params map[string]any `json:"params,omitempty"`
	Style  map[string]any `json:"style,omitempty"`
	// DurationMS is the row's OWN window length; the builder concatenates.
	DurationMS int64 `json:"duration_ms"`
}

// layerRow is one independently animated child of a composite image row.
type layerRow struct {
	ID      string         `json:"id"`
	Asset   string         `json:"asset"`
	Preset  string         `json:"preset_id,omitempty"`
	Motion  string         `json:"motion_id,omitempty"`
	StartMS int64          `json:"start_ms"`
	EndMS   int64          `json:"end_ms"`
	Params  map[string]any `json:"params,omitempty"`
}

// timelineDefinition is the -timeline document.
type timelineDefinition struct {
	PlanID     string               `json:"plan_id"`
	Width      int                  `json:"width,omitempty"`
	Height     int                  `json:"height,omitempty"`
	FPS        int                  `json:"fps,omitempty"`
	Background *renderbatch.Surface `json:"background,omitempty"`
	// GapMS pads EVERY boundary: 0 (default) is strict concatenation — item
	// k+1 starts on the frame item k ends. A positive value is a uniform
	// breathing gap between overlays, in ms, quantized to whole frames.
	GapMS int64 `json:"gap_ms,omitempty"`
	// Language travels on the plan (font selection for translated cuts).
	Language string        `json:"language,omitempty"`
	Overlays []timelineRow `json:"overlays"`
}

// computedRow is one overlay with its computed window.
type computedRow struct {
	timelineRow
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
}

// timelineDescriptor is the -out-dir timing descriptor.
type timelineDescriptor struct {
	PlanID     string          `json:"plan_id"`
	Width      int             `json:"width"`
	Height     int             `json:"height"`
	FPS        int             `json:"fps"`
	DurationMS int64           `json:"duration_ms"`
	DurationF  int             `json:"duration_frames"`
	Items      []descriptorRow `json:"items"`
}

type descriptorRow struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Motion     string `json:"motion,omitempty"`
	StartMS    int64  `json:"start_ms"`
	EndMS      int64  `json:"end_ms"`
	StartFrame int    `json:"start_frame"`
	EndFrame   int    `json:"end_frame"`
	EntryFrame int    `json:"entry_frame"`
	Asset      string `json:"asset,omitempty"`
}

func main() {
	timelinePath := flag.String("timeline", "", "timeline definition JSON (required)")
	outDir := flag.String("out-dir", "", "directory for the plan documents and the timing descriptor (required)")
	assetsRoot := flag.String("assets-root", "", "workspace the assets' logical paths resolve against (required when a row names an asset)")
	flag.Parse()
	if *timelinePath == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "overlay-timeline: -timeline and -out-dir are required")
		os.Exit(2)
	}
	if err := run(*timelinePath, *outDir, *assetsRoot); err != nil {
		fmt.Fprintf(os.Stderr, "overlay-timeline: %v\n", err)
		os.Exit(1)
	}
}

func run(timelinePath, outDir, assetsRoot string) error {
	raw, err := os.ReadFile(timelinePath)
	if err != nil {
		return fmt.Errorf("read timeline: %w", err)
	}
	var def timelineDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return fmt.Errorf("decode timeline: %w", err)
	}
	// Reject duplicate asset ids up front: the compiler's asset registry fails
	// closed on a collision, and the error is clearer before anything is built.
	if err := checkDuplicateAssetIDs(def.Overlays); err != nil {
		return err
	}
	if strings.TrimSpace(def.PlanID) == "" {
		return fmt.Errorf("timeline: plan_id is required")
	}
	if len(def.Overlays) == 0 {
		return fmt.Errorf("timeline: overlays carries no rows")
	}
	width, height := def.Width, def.Height
	if width <= 0 || height <= 0 {
		width, height = 1920, 1080
	}
	fps := def.FPS
	if fps <= 0 {
		fps = defaultFPS
	}
	if def.GapMS < 0 {
		return fmt.Errorf("timeline: gap_ms must be non-negative")
	}
	// The gap is a whole number of frames so a boundary never lands mid-frame.
	gapMS := quantizeGap(def.GapMS, fps)

	rows, err := computeWindows(def.Overlays, gapMS, fps)
	if err != nil {
		return err
	}

	items, err := buildItems(rows, assetsRoot, fps)
	if err != nil {
		return err
	}
	duration := rows[len(rows)-1].EndMS

	spec := renderbatch.PlanSpec{
		PlanID:     def.PlanID,
		ProjectID:  def.PlanID,
		Language:   def.Language,
		Width:      width,
		Height:     height,
		FPSNum:     fps,
		FPSDen:     1,
		DurationMS: duration,
		Background: def.Background,
		Items:      items,
	}
	// The typed writer validates the document through the worker's own decoder
	// AND lowers it through the same compiler the worker runs: an unresolvable
	// kind, preset, motion or template fails here, before anything is written.
	semantic, err := renderbatch.BuildPlan(spec)
	if err != nil {
		return fmt.Errorf("build semantic plan: %w", err)
	}
	compiled, err := renderbatch.CompileRenderPlan(semantic)
	if err != nil {
		return fmt.Errorf("compile render plan: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	semanticPath := filepath.Join(outDir, "semantic_plan.json")
	renderPath := filepath.Join(outDir, "render_plan.json")
	if err := os.WriteFile(semanticPath, semantic, 0o644); err != nil {
		return fmt.Errorf("write semantic plan: %w", err)
	}
	if err := os.WriteFile(renderPath, compiled, 0o644); err != nil {
		return fmt.Errorf("write render plan: %w", err)
	}

	descriptor := timelineDescriptor{
		PlanID:     def.PlanID,
		Width:      width,
		Height:     height,
		FPS:        fps,
		DurationMS: duration,
		DurationF:  int(duration * int64(fps) / 1000),
	}
	for _, row := range rows {
		descriptor.Items = append(descriptor.Items, descriptorRow{
			ID: rowID(row.timelineRow), Kind: row.Kind, Motion: row.Motion,
			StartMS: row.StartMS, EndMS: row.EndMS,
			StartFrame: int(row.StartMS * int64(fps) / 1000),
			EndFrame:   int(row.EndMS * int64(fps) / 1000),
			EntryFrame: int((row.StartMS + (row.EndMS-row.StartMS)/4) * int64(fps) / 1000),
			Asset:      row.Asset,
		})
	}
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return fmt.Errorf("encode descriptor: %w", err)
	}
	descriptorPath := filepath.Join(outDir, "timeline.json")
	if err := os.WriteFile(descriptorPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write descriptor: %w", err)
	}

	fmt.Printf("overlays:      %d\n", len(rows))
	fmt.Printf("duration:      %d ms (%d frames @ %d fps)\n", duration, descriptor.DurationF, fps)
	fmt.Printf("semantic plan: %s\n", semanticPath)
	fmt.Printf("render plan:   %s\n", renderPath)
	fmt.Printf("descriptor:    %s\n", descriptorPath)
	return nil
}

// checkDuplicateAssetIDs rejects a timeline where two rows derive the same
// asset id (the file stem). The compiler's registry fails closed on the
// collision anyway, but the error names only the plan, so the timeline-level
// check names the offending rows.
func checkDuplicateAssetIDs(rows []timelineRow) error {
	owner := map[string]string{}
	for _, row := range rows {
		for _, path := range rowAssetPaths(row) {
			id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			if prev, dup := owner[id]; dup && prev != path {
				return fmt.Errorf("overlay %q and %q derive the same asset id %q from different files (%s, %s): rename one file or set an explicit asset_id", rowID(row), ownerRow(prev, rows), id, prev, path)
			}
			owner[id] = path
		}
	}
	return nil
}

// ownerRow finds the row that first declared the given asset path.
func ownerRow(path string, rows []timelineRow) string {
	for _, row := range rows {
		for _, candidate := range rowAssetPaths(row) {
			if candidate == path {
				return rowID(row)
			}
		}
	}
	return "?"
}

// rowAssetPaths is every asset file a row references (its own plus its layers').
func rowAssetPaths(row timelineRow) []string {
	var paths []string
	if row.Asset != "" {
		paths = append(paths, row.Asset)
	}
	for _, layer := range row.Layers {
		if layer.Asset != "" {
			paths = append(paths, layer.Asset)
		}
	}
	return paths
}

// quantizeGap rounds the requested gap to whole frames at fps.
func quantizeGap(gapMS int64, fps int) int64 {
	if gapMS <= 0 {
		return 0
	}
	frameMS := int64(time.Second) / int64(fps)
	if frameMS == 0 {
		return gapMS
	}
	return ((gapMS + frameMS/2) / frameMS) * frameMS
}

// computeWindows lays the rows out on one continuous chain. The rule is the
// vidrush-scenario one, generalized: item k+1 starts on the frame item k ends
// (optionally padded by a uniform whole-frame gap), and each row keeps its OWN
// duration — the harmonic timing comes from the compiler sizing every
// entrance/exit inside its item's window, not from per-pair tuning here.
func computeWindows(rows []timelineRow, gapMS int64, fps int) ([]computedRow, error) {
	computed := make([]computedRow, 0, len(rows))
	cursor := int64(0)
	for index, row := range rows {
		if row.DurationMS <= 0 {
			return nil, fmt.Errorf("overlay %d (%s): duration_ms must be positive", index, rowID(row))
		}
		row.ID = strings.TrimSpace(row.ID)
		if row.ID == "" {
			row.ID = fmt.Sprintf("overlay-%02d", index+1)
		}
		start := cursor
		cursor += row.DurationMS + gapMS
		computed = append(computed, computedRow{timelineRow: row, StartMS: start, EndMS: start + row.DurationMS})
	}
	return computed, nil
}

// rowID is the display id of a row (its declared id or its kind).
func rowID(row timelineRow) string {
	if row.ID != "" {
		return row.ID
	}
	if row.Kind != "" {
		return row.Kind
	}
	return "overlay"
}

// buildItems lowers the computed rows to renderbatch items.
func buildItems(rows []computedRow, assetsRoot string, fps int) ([]renderbatch.PlanItem, error) {
	items := make([]renderbatch.PlanItem, 0, len(rows))
	for _, row := range rows {
		duration := row.EndMS - row.StartMS
		item := renderbatch.PlanItem{
			ID:                row.ID,
			Kind:              row.Kind,
			TemplateID:        row.Template,
			PresetID:          row.Preset,
			ImagePresetID:     row.ImagePre,
			MotionID:          row.Motion,
			Text:              row.Text,
			EntityID:          row.EntityID,
			EntityCaption:     row.EntityCap,
			CaptionMotionID:   row.CaptionMotion,
			CaptionLayout:     row.CaptionLayout,
			CaptionFontFamily: row.CaptionFont,
			CaptionColor:      row.CaptionColor,
			DurationMS:        &duration,
			Params:            row.Params,
			Style:             row.Style,
			Map:               row.Map,
			StartMS:           row.StartMS,
			EndMS:             row.EndMS,
		}
		switch {
		case len(row.Layers) > 0:
			refs, layers, err := compositeLayers(row, assetsRoot, fps)
			if err != nil {
				return nil, err
			}
			item.AssetRefs = refs
			item.ImageLayers = layers
		case row.Asset != "":
			ref, err := assetRef(row.Asset, assetsRoot)
			if err != nil {
				return nil, err
			}
			item.AssetRefs = []renderbatch.PlanAssetRef{ref}
		}
		items = append(items, item)
	}
	return items, nil
}

// compositeLayers lowers a composite row's children to the contract's
// image_layers, computing each child's asset content address. Child windows
// are RELATIVE to the parent item's window (the contract's own semantics: the
// compiler adds the item's start frame).
func compositeLayers(row computedRow, assetsRoot string, fps int) ([]renderbatch.PlanAssetRef, []overlay.SemanticImageLayer, error) {
	refs := make([]renderbatch.PlanAssetRef, 0, len(row.Layers))
	layers := make([]overlay.SemanticImageLayer, 0, len(row.Layers))
	for index, child := range row.Layers {
		id := strings.TrimSpace(child.ID)
		if id == "" {
			id = fmt.Sprintf("%s-layer-%02d", row.ID, index+1)
		}
		ref, err := assetRef(child.Asset, assetsRoot)
		if err != nil {
			return nil, nil, err
		}
		if ref.AssetID == "" {
			ref.AssetID = id + "-asset"
		}
		refs = append(refs, ref)
		layers = append(layers, overlay.SemanticImageLayer{
			ID: id, AssetID: ref.AssetID,
			StartMS: child.StartMS, EndMS: child.EndMS,
			PresetID: child.Preset, MotionID: child.Motion,
			Params: child.Params,
		})
	}
	return refs, layers, nil
}

// assetRef builds the content-addressed reference of one asset file. The
// asset id is the file's stem (stable across runs of the same timeline).
func assetRef(asset, assetsRoot string) (renderbatch.PlanAssetRef, error) {
	if assetsRoot == "" {
		return renderbatch.PlanAssetRef{}, fmt.Errorf("asset %s: -assets-root is required when a row names an asset", asset)
	}
	path := filepath.Join(assetsRoot, filepath.FromSlash(asset))
	data, err := os.ReadFile(path)
	if err != nil {
		return renderbatch.PlanAssetRef{}, fmt.Errorf("asset %s is not materialized under -assets-root: %w", asset, err)
	}
	sum := sha256.Sum256(data)
	mediaType := "image/png"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		mediaType = "image/jpeg"
	case ".webp":
		mediaType = "image/webp"
	case ".mp4":
		mediaType = "video/mp4"
	}
	return renderbatch.PlanAssetRef{
		AssetID:   strings.TrimSuffix(filepath.Base(asset), filepath.Ext(asset)),
		SHA256:    hex.EncodeToString(sum[:]),
		URL:       asset,
		MediaType: mediaType,
	}, nil
}
