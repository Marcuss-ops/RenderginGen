package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/typography"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	layers := typography.BuildWordLayers("benchmark", "Designed for speed.", "assets/fonts/Inter-Bold.ttf", 76, []float64{1, 1, 1, 1}, 0, 4, 120, "reveal_from_bottom")
	plan := overlay.Plan{Schema: "chronon.render-plan", Version: 1, JobID: "typography-suite", Canvas: overlay.Canvas{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationFrames: 120}, Layers: layers, Output: overlay.Output{Path: "result.mp4", Format: "mp4", Codec: "h264"}}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		panic(err)
	}
	path := filepath.Join(cwd, "typography_benchmark_videos", "typography_suite_plan.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote generic typography plan: %s (%d layers)\n", path, len(layers))
}
