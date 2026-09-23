package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
)

// semanticTypographyItem is the shared typed item of the semantic plan writer in
// internal/renderbatch. This command used to declare its own copy of the whole
// overlay-plan.v1 document, so the same wire contract had two hand-maintained
// writer struct sets that could drift apart.
type semanticTypographyItem = renderbatch.PlanItem

type benchmarkSpec struct {
	ID     string
	Status string
	Reason string
	Items  []semanticTypographyItem
}

type manifestEntry struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	PlanPath string `json:"plan_path,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func main() {
	motionID := flag.String("motion", "character_cascade", "MotionRegistry ID to compile")
	out := flag.String("out", "typography_benchmark_videos/typography_suite_plan.json", "output plan path")
	all := flag.Bool("all", false, "generate the fresh v2 suite and manifest")
	flag.Parse()

	if *all {
		if err := generateSuite(*out); err != nil {
			panic(err)
		}
		return
	}
	item := semanticTypographyItem{
		ID: "typography_title", TemplateID: "IMPORTANT_PHRASE", PresetID: "phrase_default",
		MotionID: *motionID, Text: "Designed for speed.", StartMS: 0, EndMS: 5000,
	}
	path, err := writeV2Plan(*out, "typography-suite", []semanticTypographyItem{item})
	if err != nil {
		panic(err)
	}
	fmt.Printf("wrote v2 typography plan via MotionRegistry: %s (motion=%s)\n", path, *motionID)
}

func generateSuite(out string) error {
	dir := out
	if filepath.Ext(dir) != "" {
		dir = filepath.Dir(dir)
	}
	if !filepath.IsAbs(dir) {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = filepath.Join(cwd, dir)
	}
	specs := suiteSpecs()
	manifest := make([]manifestEntry, 0, len(specs))
	for _, spec := range specs {
		entry := manifestEntry{ID: spec.ID, Status: spec.Status, Reason: spec.Reason}
		if spec.Status == "supported" {
			written, err := writeV2Plan(filepath.Join(dir, spec.ID+"_v2_plan.json"), "typography-"+spec.ID, spec.Items)
			if err != nil {
				return fmt.Errorf("%s: %w", spec.ID, err)
			}
			entry.PlanPath = written
		}
		manifest = append(manifest, entry)
	}
	data, err := json.MarshalIndent(struct {
		Schema  string          `json:"schema"`
		Version int             `json:"version"`
		Entries []manifestEntry `json:"entries"`
	}{"renderinggen.typography-certification.v1", 1, manifest}, "", "  ")
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(dir, "typography_suite_v2_manifest.json")
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0644); err != nil {
		return err
	}
	fmt.Printf("wrote v2 typography suite manifest: %s\n", manifestPath)
	return nil
}

func writeV2Plan(path, planID string, items []semanticTypographyItem) (string, error) {
	// Built through the shared writer, which also lowers the document through
	// the worker's own compiler, so an unresolvable preset or motion in the suite
	// fails here with the plan id instead of producing a plan that never renders.
	raw, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
		PlanID:          planID,
		Width:           1920,
		Height:          1080,
		FPSNum:          24,
		FPSDen:          1,
		OutputProfileID: "preview",
		Items:           items,
	})
	if err != nil {
		return "", err
	}
	result, err := overlay.CompileSemantic(raw)
	if err != nil {
		return "", err
	}
	data := result.Plan
	if data.Schema != "chronon.render-plan.v2" || data.Version != 2 {
		return "", fmt.Errorf("compiler emitted %s v%d, want chronon.render-plan.v2", data.Schema, data.Version)
	}
	dataBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(cwd, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(dataBytes, '\n'), 0644); err != nil {
		return "", err
	}
	return path, nil
}

func item(id, motion, text string) semanticTypographyItem {
	return semanticTypographyItem{ID: id, TemplateID: "IMPORTANT_PHRASE", PresetID: "phrase_default", MotionID: motion, Text: text, EndMS: 5000}
}

func itemPos(id, motion, text string, posX, posY float64) semanticTypographyItem {
	it := item(id, motion, text)
	it.MotionParams = map[string]any{"position_x": posX, "position_y": posY}
	return it
}

func suiteSpecs() []benchmarkSpec {
	t := "Designed for speed."
	return []benchmarkSpec{
		{ID: "01_masked_slide_up", Status: "blocked", Reason: "mask/clip primitive is not exposed by render-plan.v2"},
		{ID: "02_word_by_word", Status: "supported", Items: []semanticTypographyItem{item("title", "word_reveal", t)}},
		{ID: "03_character_cascade", Status: "supported", Items: []semanticTypographyItem{item("title", "character_cascade", t)}},
		{ID: "04_tracking_collapse", Status: "supported", Items: []semanticTypographyItem{item("title", "tracking_collapse", t)}},
		{ID: "05_tracking_expansion", Status: "supported", Items: []semanticTypographyItem{item("title", "tracking_expansion", t)}},
		{ID: "06_blur_focus_in", Status: "supported", Items: []semanticTypographyItem{item("title", "blur_focus_in", t)}},
		{ID: "07_soft_scale_reveal", Status: "supported", Items: []semanticTypographyItem{item("title", "soft_scale_reveal", t)}},
		{ID: "08_precision_spring_up", Status: "supported", Items: []semanticTypographyItem{item("title", "precision_spring_up", t)}},
		{ID: "09_split_line_reveal", Status: "supported", Items: []semanticTypographyItem{
			itemPos("line_a", "slide_left", "Create without limits.", 0, 420.0),
			itemPos("line_b", "slide_right", "Beyond boundaries.", 0, 560.0),
		}},
		{ID: "10_center_expansion", Status: "supported", Items: []semanticTypographyItem{item("title", "center_expansion", "PURE")}},
		{ID: "11_rolling_words", Status: "blocked", Reason: "clip/group viewport primitive is not exposed by render-plan.v2"},
		{ID: "12_opacity_wave", Status: "supported", Items: []semanticTypographyItem{item("title", "opacity_wave", t)}},
		{ID: "13_scale_wave", Status: "supported", Items: []semanticTypographyItem{item("title", "scale_wave", t)}},
		{ID: "14_char_wave", Status: "supported", Items: []semanticTypographyItem{item("title", "char_wave", t)}},
		{ID: "15_hero_typography", Status: "supported", Items: []semanticTypographyItem{
			itemPos("tag", "fade_in", "INTRODUCING", 0, 300.0),
			itemPos("headline", "character_cascade", "The Next Dimension", 0, 420.0),
			itemPos("subtitle", "tracking_expansion", "Engineered for speed.", 0, 560.0),
			itemPos("keyword", "soft_scale_reveal", "Pure performance.", 0, 680.0),
		}},
	}
}
