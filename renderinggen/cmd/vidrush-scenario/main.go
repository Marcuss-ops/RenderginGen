// Command vidrush-scenario defines and compiles ONE concatenated runtime
// scenario: a single semantic overlay plan that walks every animated overlay
// family a social/editorial cut uses, back to back on one timeline —
//
//	frasi     2 important phrases (phrase_default + catalog phrase motions)
//	date      timeline_date_card (date_v1)
//	metric    metric_stat_card (metric_v1)
//	entità    2 entity cards with text (PERSON portrait + caption, ORGANIZATION
//	          text card) — the max-2 entity scene
//	luoghi    LOCATION entity card + a georeferenced local map with pins
//	immagini  composite image items with 2, 3, 4 and 5 independently animated
//	          image layers (the images-x2/x3/x4/x5 matrix)
//
// The command generates its own deterministic fixtures (map plate, portraits,
// scene plates) into -assets-root, so the same checkout renders the same
// pixels without ffmpeg, network access or checked-in binaries. The official
// font is staged from -font (or discovered in testdata/golden) because the
// renderer resolves every text layer's font from the assets root.
//
// It writes three artefacts into -out-dir:
//
//	semantic_plan.json  the renderinggen.overlay-plan.v1 document (input of record)
//	render_plan.json    the compiled chronon.render-plan.v2/v3 the renderer consumes
//	scenario.json       the runtime descriptor (acts, windows, sampled frames,
//	                    thresholds) the verification script drives
//
// The scenario definition lives HERE, once: the script renders and probes
// pixels, but it never restates a window or a frame number, so the two halves
// cannot drift apart. Both documents are built through renderbatch (the typed
// writer) and lowered through the worker's own compiler — no hand-rolled JSON
// and no second lowering.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
)

const (
	scenarioPlanID = "vidrush-scenario"
	canvasWidth    = 1920
	canvasHeight   = 1080
	scenarioFPS    = 24
)

// Act windows. Acts are concatenated — every act starts on the frame the
// previous one ends, so the whole timeline is one continuous chain of
// animations with no overlay-free gap.
const (
	frasiStart, frasiEnd       = int64(0), int64(4000)
	dateStart, dateEnd         = frasiEnd, int64(7000)
	metricStart, metricEnd     = dateEnd, int64(10000)
	entitaStart, entitaEnd     = metricEnd, int64(16000)
	luoghiStart, luoghiEnd     = entitaEnd, int64(22000)
	immaginiStart, immaginiEnd = luoghiEnd, int64(34000)
	scenarioDuration           = immaginiEnd
)

// entryFraction / midFraction are the probe sampling points inside an item's
// window: entry is early enough that a settled picture is still entering,
// mid is where the presence probe reads the item at (or near) rest.
const (
	entryFraction = 0.25
	midFraction   = 0.6
)

// childSpec is one independently animated layer of a composite image item.
type childSpec struct {
	ID      string
	Motion  string
	Width   float64
	Height  float64
	X, Y    float64
	StartMS int64
	EndMS   int64
}

// itemSpec is one overlay of the scenario plus the probe contract the
// verification script enforces for it.
type itemSpec struct {
	Name              string
	Kind              string // phrase | date | metric | entity_image | entity_text | location | map | image_composite
	Template          string
	Preset            string
	ImagePreset       string
	Motion            string
	Text              string
	EntityID          string
	EntityCaption     string
	CaptionMotion     string
	CaptionLayout     string
	CaptionFontFamily string
	CaptionColor      string
	StartMS           int64
	EndMS             int64
	Asset             string // logical path relative to the assets root (portrait / map plate)
	Children          []childSpec
	Params            map[string]any
	MinDiffPixels     int
	CenterOnly        bool
}

type actSpec struct {
	ID    string
	Title string
	Items []itemSpec
	Start int64
	End   int64
}

// scenarioItems is the full concatenated timeline. Motion ids are catalog
// ids (see internal/motion/catalog); a typo fails the compile, not the render.
var scenarioItems = []itemSpec{
	// ── frasi: 2 important phrases ────────────────────────────────────────
	{
		Name: "frasi", Kind: "phrase", Template: "IMPORTANT_PHRASE", Preset: "phrase_default",
		Motion: "apple_phrase_v2", Text: "QUESTO CAMBIA TUTTO",
		StartMS: frasiStart, EndMS: frasiStart + 2000, MinDiffPixels: 300,
	},
	{
		Name: "frasi_b", Kind: "phrase", Template: "IMPORTANT_PHRASE", Preset: "phrase_default",
		Motion: "kinetic_split_word", Text: "LA STORIA CONTINUA",
		StartMS: frasiStart + 2000, EndMS: frasiEnd, MinDiffPixels: 300,
	},

	// ── date: one timeline date card ──────────────────────────────────────
	{
		Name: "data", Kind: "date", Template: "TIMELINE_DATE_CARD", Preset: "phrase_default",
		Motion: "date_timeline_sweep", Text: "13 September 2026",
		StartMS: dateStart, EndMS: dateEnd, MinDiffPixels: 300,
	},

	// ── metric: one metric stat card ──────────────────────────────────────
	{
		Name: "metrica", Kind: "metric", Template: "METRIC_STAT_CARD", Preset: "phrase_default",
		Motion: "metric_count_flip", Text: "700 VIDEO",
		StartMS: metricStart, EndMS: metricEnd, MinDiffPixels: 300,
	},

	// ── entità: two Trump portrait cards with animated V1 captions ────────
	{
		Name: "entita_trump_newsroom", Kind: "entity_card", Template: "PERSON",
		Preset: "phrase_default", ImagePreset: "image_scale_in", Motion: "image_depth_dolly",
		Text: "DONALD TRUMP", EntityID: "person:donald-trump",
		EntityCaption: "DONALD TRUMP", CaptionMotion: "trump_entity_text_02",
		CaptionLayout:     "right",
		CaptionFontFamily: "bricolage_grotesque", CaptionColor: "#161718",
		StartMS: entitaStart, EndMS: entitaStart + 3000, MinDiffPixels: 20000, CenterOnly: true,
		Asset:  "assets/semantic/vidrush-portrait-trump.png",
		Params: map[string]any{"width": 620.0, "height": 760.0, "position_x": -500.0, "position_y": -20.0},
	},
	{
		Name: "entita_trump_halftone", Kind: "entity_card", Template: "PERSON",
		Preset: "phrase_default", ImagePreset: "image_scale_in", Motion: "image_yaw_reveal",
		Text: "DONALD TRUMP", EntityID: "person:donald-trump",
		EntityCaption: "DONALD TRUMP", CaptionMotion: "trump_entity_text_03",
		CaptionLayout:     "left",
		CaptionFontFamily: "bricolage_grotesque", CaptionColor: "#161718",
		StartMS: entitaStart + 3000, EndMS: entitaEnd, MinDiffPixels: 300,
		Asset:  "assets/semantic/vidrush-portrait-trump.png",
		Params: map[string]any{"width": 620.0, "height": 760.0, "position_x": 500.0, "position_y": -20.0},
	},

	// ── luoghi: a LOCATION card followed by the georeferenced map ─────────
	{
		Name: "luogo", Kind: "location", Template: "LOCATION", Preset: "phrase_default",
		Motion: "text_word_rise", Text: "London · Paris · Rome",
		EntityID: "gpe:europe-route",
		StartMS:  luoghiStart, EndMS: luoghiStart + 3000, MinDiffPixels: 300,
	},
	{
		Name: "mappa", Kind: "map", Template: "MAP",
		Motion:  "image_focus_reveal",
		StartMS: luoghiStart + 3000, EndMS: luoghiEnd, MinDiffPixels: 20000, CenterOnly: true,
		Asset: "assets/maps/vidrush-world-plate.png",
	},

	// ── immagini: composite items with 2, 3, 4 and 5 animated layers ──────
	{
		Name: "immagini_x2", Kind: "entity_image", Template: "IMAGE_OVERLAY", Preset: "image_scale_in",
		StartMS: immaginiStart, EndMS: immaginiStart + 3000, MinDiffPixels: 20000, CenterOnly: true,
		Children: []childSpec{
			{ID: "a", Motion: "image_depth_dolly", Width: 760, Height: 520, X: -400, Y: 0, StartMS: 0, EndMS: 3000},
			{ID: "b", Motion: "image_yaw_reveal", Width: 760, Height: 520, X: 400, Y: 0, StartMS: 300, EndMS: 3000},
		},
	},
	{
		Name: "immagini_x3", Kind: "entity_image", Template: "IMAGE_OVERLAY", Preset: "image_scale_in",
		StartMS: immaginiStart + 3000, EndMS: immaginiStart + 6000, MinDiffPixels: 20000, CenterOnly: true,
		Children: []childSpec{
			{ID: "a", Motion: "image_photo_drop", Width: 620, Height: 420, X: -420, Y: -240, StartMS: 0, EndMS: 3000},
			{ID: "b", Motion: "image_document_push", Width: 620, Height: 420, X: 420, Y: -240, StartMS: 200, EndMS: 3000},
			{ID: "c", Motion: "image_float_settle", Width: 620, Height: 420, X: 0, Y: 240, StartMS: 400, EndMS: 3000},
		},
	},
	{
		Name: "immagini_x4", Kind: "entity_image", Template: "IMAGE_OVERLAY", Preset: "image_scale_in",
		StartMS: immaginiStart + 6000, EndMS: immaginiStart + 9000, MinDiffPixels: 20000, CenterOnly: true,
		Children: []childSpec{
			{ID: "a", Motion: "image_collage_scatter", Width: 620, Height: 420, X: -420, Y: -240, StartMS: 0, EndMS: 3000},
			{ID: "b", Motion: "image_evidence_focus", Width: 620, Height: 420, X: 420, Y: -240, StartMS: 150, EndMS: 3000},
			{ID: "c", Motion: "image_25d_depth_float_in", Width: 620, Height: 420, X: -420, Y: 240, StartMS: 300, EndMS: 3000},
			{ID: "d", Motion: "image_depth_cascade", Width: 620, Height: 420, X: 420, Y: 240, StartMS: 450, EndMS: 3000},
		},
	},
	{
		Name: "immagini_x5", Kind: "entity_image", Template: "IMAGE_OVERLAY", Preset: "image_scale_in",
		StartMS: immaginiStart + 9000, EndMS: immaginiEnd, MinDiffPixels: 20000, CenterOnly: true,
		Children: []childSpec{
			{ID: "a", Motion: "image_roll_in", Width: 560, Height: 360, X: -440, Y: -280, StartMS: 0, EndMS: 3000},
			{ID: "b", Motion: "image_orbit_enter", Width: 560, Height: 360, X: 440, Y: -280, StartMS: 150, EndMS: 3000},
			{ID: "c", Motion: "image_tilt_parallax", Width: 560, Height: 360, X: -440, Y: 60, StartMS: 300, EndMS: 3000},
			{ID: "d", Motion: "image_focus_push", Width: 560, Height: 360, X: 440, Y: 60, StartMS: 450, EndMS: 3000},
			{ID: "e", Motion: "image_perspective_stack", Width: 560, Height: 360, X: 0, Y: 340, StartMS: 600, EndMS: 3000},
		},
	},
}

// acts group the items for the descriptor and the script's per-act probes.
var acts = []actSpec{
	{ID: "frasi", Title: "2 frasi", Start: frasiStart, End: frasiEnd},
	{ID: "date", Title: "Date & Metric — date", Start: dateStart, End: dateEnd},
	{ID: "metric", Title: "Date & Metric — metric", Start: metricStart, End: metricEnd},
	{ID: "entita", Title: "Entità con testo (max 2)", Start: entitaStart, End: entitaEnd},
	{ID: "luoghi", Title: "Luoghi + mappa", Start: luoghiStart, End: luoghiEnd},
	{ID: "immagini", Title: "Immagini x2 x3 x4 x5", Start: immaginiStart, End: immaginiEnd},
}

// mapPins are the grounded places of the map act. They are the same certified
// waypoints the golden flyover canary uses, so the Web Mercator grounding is
// exercised against known pixel expectations.
var mapPins = []overlay.SemanticMapPin{
	{ID: "london", Label: "London", Latitude: 51.5074, Longitude: -0.1278, Color: "#38BDF8", RadiusPX: 12},
	{ID: "paris", Label: "Paris", Latitude: 48.8566, Longitude: 2.3522, Color: "#F59E0B", RadiusPX: 12},
	{ID: "rome", Label: "Rome", Latitude: 41.890210, Longitude: 12.492231, Color: "#E11D48", RadiusPX: 16},
}

// scenarioItem is one descriptor row: the item, its window and the sampled
// frames the verification script compares.
type descriptorItem struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Motion        string `json:"motion,omitempty"`
	StartMS       int64  `json:"start_ms"`
	EndMS         int64  `json:"end_ms"`
	StartFrame    int    `json:"start_frame"`
	MidFrame      int    `json:"mid_frame"`
	EntryFrame    int    `json:"entry_frame"`
	MinDiffPixels int    `json:"min_diff_pixels"`
	CenterOnly    bool   `json:"center_only"`
}

type descriptorAct struct {
	ID    string           `json:"id"`
	Title string           `json:"title"`
	Start int64            `json:"start_ms"`
	End   int64            `json:"end_ms"`
	Items []descriptorItem `json:"items"`
}

// scenarioDescriptor is what the script reads; it must be self-contained.
type scenarioDescriptor struct {
	PlanID       string          `json:"plan_id"`
	Width        int             `json:"width"`
	Height       int             `json:"height"`
	FPS          int             `json:"fps"`
	DurationMS   int64           `json:"duration_ms"`
	SemanticPlan string          `json:"semantic_plan"`
	RenderPlan   string          `json:"render_plan"`
	Acts         []descriptorAct `json:"acts"`
}

func msToFrame(ms int64) int { return int(ms * scenarioFPS / 1000) }

func frameAt(startMS, endMS int64, fraction float64) int {
	return msToFrame(startMS + int64(float64(endMS-startMS)*fraction))
}

func main() {
	assetsRoot := flag.String("assets-root", "", "workspace the generated assets and the plan's logical asset paths resolve against (required)")
	outDir := flag.String("out-dir", "", "directory the plan documents and the scenario descriptor are written to (required)")
	fontPath := flag.String("font", "", "Poppins-Bold.ttf to stage as assets/fonts/Poppins-Bold.ttf (default: discovered in testdata/golden)")
	flag.Parse()
	if *assetsRoot == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "vidrush-scenario: -assets-root and -out-dir are required")
		os.Exit(2)
	}
	if err := run(*assetsRoot, *outDir, *fontPath); err != nil {
		fmt.Fprintf(os.Stderr, "vidrush-scenario: %v\n", err)
		os.Exit(1)
	}
}

func run(assetsRoot, outDir, fontPath string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	if err := generateAssets(assetsRoot); err != nil {
		return err
	}
	if err := stageOfficialFont(assetsRoot, fontPath); err != nil {
		return err
	}
	items, err := buildItems(assetsRoot)
	if err != nil {
		return err
	}
	// The typed writer builds the semantic document AND validates it through the
	// worker's decoder, so an unresolvable preset or motion fails here.
	raw, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
		PlanID:     scenarioPlanID,
		Width:      canvasWidth,
		Height:     canvasHeight,
		FPSNum:     scenarioFPS,
		FPSDen:     1,
		DurationMS: scenarioDuration,
		Background: &renderbatch.Surface{Kind: "color", Color: []float64{0.98, 0.98, 0.97, 1}},
		Items:      items,
	})
	if err != nil {
		return fmt.Errorf("build semantic plan: %w", err)
	}
	compiled, err := renderbatch.CompileRenderPlan(raw)
	if err != nil {
		return fmt.Errorf("compile render plan: %w", err)
	}

	semanticPath := filepath.Join(outDir, "semantic_plan.json")
	renderPath := filepath.Join(outDir, "render_plan.json")
	if err := os.WriteFile(semanticPath, raw, 0o644); err != nil {
		return fmt.Errorf("write semantic plan: %w", err)
	}
	if err := os.WriteFile(renderPath, compiled, 0o644); err != nil {
		return fmt.Errorf("write render plan: %w", err)
	}

	descriptor := scenarioDescriptor{
		PlanID:       scenarioPlanID,
		Width:        canvasWidth,
		Height:       canvasHeight,
		FPS:          scenarioFPS,
		DurationMS:   scenarioDuration,
		SemanticPlan: semanticPath,
		RenderPlan:   renderPath,
	}
	for _, act := range acts {
		row := descriptorAct{ID: act.ID, Title: act.Title, Start: act.Start, End: act.End}
		for _, spec := range scenarioItems {
			if spec.StartMS < act.Start || spec.EndMS > act.End {
				continue
			}
			row.Items = append(row.Items, descriptorItem{
				Name: spec.Name, Kind: spec.Kind, Motion: spec.Motion,
				StartMS: spec.StartMS, EndMS: spec.EndMS,
				StartFrame:    msToFrame(spec.StartMS),
				MidFrame:      frameAt(spec.StartMS, spec.EndMS, midFraction),
				EntryFrame:    frameAt(spec.StartMS, spec.EndMS, entryFraction),
				MinDiffPixels: spec.MinDiffPixels,
				CenterOnly:    spec.CenterOnly,
			})
		}
		descriptor.Acts = append(descriptor.Acts, row)
	}
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return fmt.Errorf("encode scenario descriptor: %w", err)
	}
	scenarioPath := filepath.Join(outDir, "scenario.json")
	if err := os.WriteFile(scenarioPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write scenario descriptor: %w", err)
	}
	fmt.Printf("semantic plan: %s\n", semanticPath)
	fmt.Printf("render plan:   %s\n", renderPath)
	fmt.Printf("descriptor:    %s\n", scenarioPath)
	return nil
}

// buildItems materializes the typed items, their generated assets and their
// content addresses. Every asset must exist at the logical path the plan
// declares, so a generation failure surfaces here instead of at render time.
func buildItems(assetsRoot string) ([]renderbatch.PlanItem, error) {
	items := make([]renderbatch.PlanItem, 0, len(scenarioItems))
	for _, spec := range scenarioItems {
		duration := spec.EndMS - spec.StartMS
		item := renderbatch.PlanItem{
			ID: spec.Name, TemplateID: spec.Template, Kind: spec.Kind,
			PresetID: spec.Preset, ImagePresetID: spec.ImagePreset,
			MotionID: spec.Motion, Text: spec.Text, EntityID: spec.EntityID,
			EntityCaption: spec.EntityCaption, CaptionMotionID: spec.CaptionMotion,
			CaptionLayout:     spec.CaptionLayout,
			CaptionFontFamily: spec.CaptionFontFamily, CaptionColor: spec.CaptionColor,
			DurationMS: &duration,
			StartMS:    spec.StartMS, EndMS: spec.EndMS,
			Params: spec.Params,
		}
		switch {
		case len(spec.Children) > 0:
			// Composite image item: one asset and one animated layer per child.
			refs, layers, err := compositeAssets(assetsRoot, spec)
			if err != nil {
				return nil, err
			}
			item.AssetRefs = refs
			item.ImageLayers = layers
		case spec.Kind == "map":
			sha, err := assetSHA256(filepath.Join(assetsRoot, filepath.FromSlash(spec.Asset)))
			if err != nil {
				return nil, err
			}
			item.AssetRefs = []renderbatch.PlanAssetRef{{
				AssetID: "vidrush-basemap", SHA256: sha, URL: spec.Asset, MediaType: "image/png",
			}}
			item.Map = &overlay.SemanticMap{
				Provider: "local", SourceID: "vidrush-operator-plate", SourceLicense: "operator-supplied",
				Center: overlay.SemanticMapPoint{}, Zoom: 1,
				Width: canvasWidth, Height: canvasHeight,
				Attribution: "Map data (c) operator-certified plate",
				MotionID:    spec.Motion,
				Pins:        append([]overlay.SemanticMapPin(nil), mapPins...),
			}
		case spec.Asset != "":
			// Entity portrait: the card carries the image, the caption rides the
			// entity_caption contract.
			sha, err := assetSHA256(filepath.Join(assetsRoot, filepath.FromSlash(spec.Asset)))
			if err != nil {
				return nil, err
			}
			item.AssetRefs = []renderbatch.PlanAssetRef{{
				AssetID: "vidrush-portrait-trump", SHA256: sha, URL: spec.Asset, MediaType: "image/png",
			}}
		}
		items = append(items, item)
	}
	return items, nil
}

// compositeAssets generates one scene plate per child and lowers the child
// list to the contract's image_layers. The plates are deterministic, so the
// plan's hashes are stable across runs.
func compositeAssets(assetsRoot string, spec itemSpec) ([]renderbatch.PlanAssetRef, []overlay.SemanticImageLayer, error) {
	refs := make([]renderbatch.PlanAssetRef, 0, len(spec.Children))
	layers := make([]overlay.SemanticImageLayer, 0, len(spec.Children))
	for index, child := range spec.Children {
		assetID := fmt.Sprintf("vidrush-%s-%02d", spec.Name, index+1)
		logical := fmt.Sprintf("assets/semantic/%s.png", assetID)
		path := filepath.Join(assetsRoot, filepath.FromSlash(logical))
		if err := writeScenePlate(path, int(child.Width), int(child.Height), sceneHue(spec.Name, index)); err != nil {
			return nil, nil, err
		}
		sha, err := assetSHA256(path)
		if err != nil {
			return nil, nil, err
		}
		refs = append(refs, renderbatch.PlanAssetRef{
			AssetID: assetID, SHA256: sha, URL: logical, MediaType: "image/png",
		})
		layers = append(layers, overlay.SemanticImageLayer{
			ID: child.ID, AssetID: assetID,
			StartMS: child.StartMS, EndMS: child.EndMS,
			PresetID: "image_scale_in", MotionID: child.Motion,
			Params: map[string]any{
				"width": child.Width, "height": child.Height,
				"position_x": child.X, "position_y": child.Y, "fit": "contain",
			},
		})
	}
	return refs, layers, nil
}

func assetSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("scenario asset %s is not materialized: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// generateAssets writes every generated fixture the plan references. The map
// plate must match the canvas exactly (the static map contract), and the
// portraits are square so the entity card's contain box is exercised.
func generateAssets(assetsRoot string) error {
	if err := os.MkdirAll(filepath.Join(assetsRoot, "assets", "semantic"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(assetsRoot, "assets", "maps"), 0o755); err != nil {
		return err
	}
	if err := writeWorldPlate(filepath.Join(assetsRoot, "assets", "maps", "vidrush-world-plate.png")); err != nil {
		return err
	}
	portraitSources := []string{
		filepath.Join("ChrononTemplate", "out", "entity_caption_reference_v3", "assets", "portraits", "donald-trump-editorial.png"),
		filepath.Join("..", "ChrononTemplate", "out", "entity_caption_reference_v3", "assets", "portraits", "donald-trump-editorial.png"),
		filepath.Join("..", "..", "ChrononTemplate", "out", "entity_caption_reference_v3", "assets", "portraits", "donald-trump-editorial.png"),
		filepath.Join("..", "..", "..", "ChrononTemplate", "out", "entity_caption_reference_v3", "assets", "portraits", "donald-trump-editorial.png"),
		filepath.Join("..", "..", "..", "..", "ChrononTemplate", "out", "entity_caption_reference_v3", "assets", "portraits", "donald-trump-editorial.png"),
	}
	var portrait []byte
	var source string
	for _, candidate := range portraitSources {
		if data, err := os.ReadFile(candidate); err == nil {
			portrait, source = data, candidate
			break
		}
	}
	if portrait == nil {
		return fmt.Errorf("Trump portrait from entity_caption_reference_v3 not found (checked %v)", portraitSources)
	}
	if err := os.WriteFile(filepath.Join(assetsRoot, "assets", "semantic", "vidrush-portrait-trump.png"), portrait, 0o644); err != nil {
		return fmt.Errorf("copy Trump portrait from %s: %w", source, err)
	}
	fontSources := []string{
		filepath.Join("ChrononTemplate", "out", "entity_caption_reference_v3", "assets", "fonts", "Bricolage-Grotesque.ttf"),
		filepath.Join("..", "ChrononTemplate", "out", "entity_caption_reference_v3", "assets", "fonts", "Bricolage-Grotesque.ttf"),
		filepath.Join("..", "..", "ChrononTemplate", "out", "entity_caption_reference_v3", "assets", "fonts", "Bricolage-Grotesque.ttf"),
		filepath.Join("..", "..", "..", "ChrononTemplate", "out", "entity_caption_reference_v3", "assets", "fonts", "Bricolage-Grotesque.ttf"),
		filepath.Join("..", "..", "..", "..", "ChrononTemplate", "out", "entity_caption_reference_v3", "assets", "fonts", "Bricolage-Grotesque.ttf"),
	}
	for _, candidate := range fontSources {
		if data, err := os.ReadFile(candidate); err == nil {
			target := filepath.Join(assetsRoot, "assets", "fonts", "Bricolage-Grotesque.ttf")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o644)
		}
	}
	return fmt.Errorf("Bricolage Grotesque font not found (checked %v)", fontSources)
}

// stageOfficialFont copies the official preset font into the assets root. The
// renderer resolves assets/fonts/Poppins-Bold.ttf from the workspace for every
// text layer, so a render without it fails; the compile itself does not need
// the bytes, which is why a missing font is a warning here and a hard error in
// the run script.
func stageOfficialFont(assetsRoot, fontPath string) error {
	target := filepath.Join(assetsRoot, "assets", "fonts", "Poppins-Bold.ttf")
	if _, err := os.Stat(target); err == nil {
		return nil
	}
	if fontPath == "" {
		// The font lives in RenderingGen/testdata/golden; which relative path
		// reaches it depends on the caller's cwd, so try every depth a caller
		// in this repository plausibly runs from.
		for _, candidate := range []string{
			filepath.Join("testdata", "golden", "assets", "fonts", "Poppins-Bold.ttf"),
			filepath.Join("..", "testdata", "golden", "assets", "fonts", "Poppins-Bold.ttf"),
			filepath.Join("..", "..", "testdata", "golden", "assets", "fonts", "Poppins-Bold.ttf"),
			filepath.Join("..", "..", "..", "testdata", "golden", "assets", "fonts", "Poppins-Bold.ttf"),
		} {
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				fontPath = candidate
				break
			}
		}
	}
	if fontPath == "" {
		fmt.Fprintln(os.Stderr, "vidrush-scenario: warning: official font not found; pass -font to stage assets/fonts/Poppins-Bold.ttf for a render")
		return nil
	}
	data, err := os.ReadFile(fontPath)
	if err != nil {
		return fmt.Errorf("read font %s: %w", fontPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("stage font: %w", err)
	}
	return nil
}

// writeWorldPlate paints the deterministic map raster: a dark ocean, a
// latitude/longitude graticule and stylized landmasses. The map contract only
// certifies provenance, geometry and grounding — the pixels are the
// operator's — so the plate is generated, never fetched.
func writeWorldPlate(path string) error {
	img := image.NewRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))
	ocean := color.RGBA{R: 16, G: 28, B: 38, A: 255}
	for y := 0; y < canvasHeight; y++ {
		for x := 0; x < canvasWidth; x++ {
			img.SetRGBA(x, y, ocean)
		}
	}
	grid := color.RGBA{R: 60, G: 92, B: 110, A: 255}
	for x := 0; x < canvasWidth; x += 120 {
		for y := 0; y < canvasHeight; y++ {
			img.SetRGBA(x, y, grid)
		}
	}
	for y := 0; y < canvasHeight; y += 120 {
		for x := 0; x < canvasWidth; x++ {
			img.SetRGBA(x, y, grid)
		}
	}
	land := color.RGBA{R: 54, G: 96, B: 74, A: 255}
	landEdges := color.RGBA{R: 118, G: 168, B: 128, A: 255}
	for _, blob := range []struct{ x, y, rx, ry int }{
		{430, 380, 210, 150}, // Europe / Africa
		{560, 760, 150, 210},
		{1320, 420, 260, 190}, // Asia
		{1520, 780, 180, 150},
		{250, 470, 130, 170}, // Americas
		{330, 800, 110, 150},
	} {
		fillEllipse(img, blob.x, blob.y, blob.rx, blob.ry, land)
		strokeEllipse(img, blob.x, blob.y, blob.rx, blob.ry, landEdges)
	}
	return encodePNG(path, img)
}

// writePortrait paints the deterministic entity portrait: a hue gradient with
// a centered monogram disc. Square by contract so the entity card's contain
// box never crops it.
func writePortrait(path string, width, height int) error {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			t := float64(x+y) / float64(width+height)
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(30 + 120*t),
				G: uint8(40 + 60*t),
				B: uint8(80 + 150*(1-t)),
				A: 255,
			})
		}
	}
	fillEllipse(img, width/2, height/2, width/3, height/3, color.RGBA{R: 236, G: 240, B: 248, A: 255})
	fillEllipse(img, width/2, height/2, width/5, height/5, color.RGBA{R: 40, G: 60, B: 110, A: 255})
	return encodePNG(path, img)
}

// writeScenePlate paints one composite child plate. Hue varies per child so
// the x2/x3/x4/x5 matrix never collapses to the same picture.
func writeScenePlate(path string, width, height int, hue float64) error {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	base := hsvToRGB(hue, 0.55, 0.85)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			shade := 0.75 + 0.25*float64(x)/float64(width)
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(float64(base.R) * shade),
				G: uint8(float64(base.G) * shade),
				B: uint8(float64(base.B) * shade),
				A: 255,
			})
		}
	}
	band := color.RGBA{R: 250, G: 250, B: 250, A: 255}
	for i := 0; i < 3; i++ {
		x := width/4 + i*width/4
		for y := height / 6; y < height*5/6; y++ {
			for dx := 0; dx < width/40; dx++ {
				img.SetRGBA(x+dx, y, band)
			}
		}
	}
	return encodePNG(path, img)
}

func sceneHue(name string, index int) float64 {
	sum := 0
	for _, r := range name {
		sum += int(r)
	}
	return math.Mod(float64(sum+index*47), 360) / 360
}

func fillEllipse(img *image.RGBA, cx, cy, rx, ry int, c color.RGBA) {
	for y := cy - ry; y <= cy+ry; y++ {
		for x := cx - rx; x <= cx+rx; x++ {
			dx := float64(x-cx) / float64(rx)
			dy := float64(y-cy) / float64(ry)
			if dx*dx+dy*dy <= 1 {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func strokeEllipse(img *image.RGBA, cx, cy, rx, ry int, c color.RGBA) {
	for y := cy - ry; y <= cy+ry; y++ {
		for x := cx - rx; x <= cx+rx; x++ {
			dx := float64(x-cx) / float64(rx)
			dy := float64(y-cy) / float64(ry)
			d := dx*dx + dy*dy
			if d <= 1 && d >= 0.9 {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func hsvToRGB(h, s, v float64) color.RGBA {
	i := int(h * 6)
	f := h*6 - float64(i)
	p := v * (1 - s)
	q := v * (1 - f*s)
	t := v * (1 - (1-f)*s)
	var r, g, b float64
	switch i % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	case 5:
		r, g, b = v, p, q
	}
	return color.RGBA{R: uint8(r * 255), G: uint8(g * 255), B: uint8(b * 255), A: 255}
}

func encodePNG(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}
