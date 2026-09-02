package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
)

type semanticTypographyPlan struct {
	SchemaVersion string                   `json:"schema_version"`
	PlanID        string                   `json:"plan_id"`
	VideoID       string                   `json:"video_id"`
	Width         int                      `json:"width"`
	Height        int                      `json:"height"`
	FPSNum        int                      `json:"fps_num"`
	FPSDen        int                      `json:"fps_den"`
	OutputProfile string                   `json:"output_profile_id"`
	Items         []semanticTypographyItem `json:"items"`
}

type semanticTypographyItem struct {
	ID         string         `json:"id"`
	Template   string         `json:"template_id"`
	PresetID   string         `json:"preset_id"`
	MotionID   string         `json:"motion_id"`
	MotionArgs map[string]any `json:"motion_params,omitempty"`
	Text       string         `json:"text"`
	StartMS    int64          `json:"start_ms"`
	EndMS      int64          `json:"end_ms"`
}

func main() {
	motionID := flag.String("motion", "character_cascade", "MotionRegistry ID to compile")
	out := flag.String("out", "typography_benchmark_videos/typography_suite_plan.json", "output plan path")
	flag.Parse()

	semantic := semanticTypographyPlan{
		SchemaVersion: "renderinggen.overlay-plan.v1",
		PlanID:        "typography-suite",
		VideoID:       "typography-suite",
		Width:         1920,
		Height:        1080,
		FPSNum:        24,
		FPSDen:        1,
		OutputProfile: "preview",
		Items: []semanticTypographyItem{{
			ID: "typography_title", Template: "IMPORTANT_PHRASE", PresetID: "phrase_focus_v1",
			MotionID: *motionID, Text: "Designed for speed.", StartMS: 0, EndMS: 5000,
		}},
	}
	raw, err := json.Marshal(semantic)
	if err != nil {
		panic(err)
	}
	data, _, semanticCompiled, err := overlay.CompileIfSemantic(raw)
	if err != nil {
		panic(err)
	}
	if !semanticCompiled {
		panic("typography runner: semantic plan did not compile through RenderingGen")
	}
	var document struct {
		Schema  string `json:"schema"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		panic(err)
	}
	if document.Schema != "chronon.render-plan.v2" || document.Version != 2 {
		panic(fmt.Sprintf("typography runner: compiler emitted %s v%d, want chronon.render-plan.v2", document.Schema, document.Version))
	}
	data, err = json.MarshalIndent(json.RawMessage(data), "", "  ")
	if err != nil {
		panic(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	path := *out
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote v2 typography plan via MotionRegistry: %s (motion=%s)\n", path, *motionID)
}
