// Command render-web-page-gallery creates restrained phrase animations on a
// classic white page using catalog motions and the Chronon renderer.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
)

const phrase = "Diamo forma alle idee."

type sample struct {
	ID       string `json:"id"`
	MotionID string `json:"motion_id"`
	Name     string `json:"name"`
	Plan     string `json:"plan"`
	Video    string `json:"video,omitempty"`
}

var samples = []sample{
	{ID: "01_blur_soft_reveal", MotionID: "phrase_apple_clean_01_blur_soft_reveal", Name: "Blur soft reveal"},
	{ID: "02_slide_up_soft", MotionID: "phrase_apple_clean_07_slide_up_soft", Name: "Slide up soft"},
	{ID: "03_slide_from_right", MotionID: "phrase_apple_clean_10_slide_from_right_apple", Name: "Slide from right"},
	{ID: "04_scale_soft_pop", MotionID: "phrase_apple_clean_13_scale_soft_pop", Name: "Scale soft pop"},
	{ID: "05_tracking_tighten", MotionID: "phrase_apple_clean_19_tracking_tighten", Name: "Tracking tighten"},
	{ID: "06_blur_scale_clean", MotionID: "phrase_apple_clean_03_blur_scale_clean", Name: "Blur and scale"},
	{ID: "07_slide_down_catch", MotionID: "phrase_apple_clean_09_slide_down_catch", Name: "Slide down catch"},
	{ID: "08_opacity_soft_reveal", MotionID: "phrase_apple_clean_25_opacity_soft_reveal", Name: "Opacity soft reveal"},
	{ID: "09_opacity_hero_settle", MotionID: "phrase_apple_clean_29_opacity_hero_settle", Name: "Opacity hero settle"},
	{ID: "10_opacity_clean_apple", MotionID: "phrase_apple_clean_30_opacity_clean_apple", Name: "Opacity clean"},
}

func main() {
	render := flag.Bool("render", false, "render validated plans to MP4 with Chronon")
	flag.Parse()
	root, err := repoRoot()
	check(err)
	outDir := filepath.Join(root, "RenderingGen", "out", "web_phrase_gallery")
	assetsRoot := filepath.Join(root, "Chronon3d")
	chronon := os.Getenv("CHRONON3D_CLI")
	if chronon == "" {
		chronon = filepath.Join(root, "Chronon3d", "build", "chronon", "linux-video-fast-dev", "apps", "chronon3d_cli", "chronon3d_cli")
	}
	if _, err := os.Stat(chronon); err != nil {
		check(fmt.Errorf("chronon3d_cli unavailable at %s: set CHRONON3D_CLI to a built CLI", chronon))
	}
	check(os.MkdirAll(outDir, 0o755))

	for i := range samples {
		entry := &samples[i]
		if _, err := motion.Registry.Resolve(entry.MotionID); err != nil {
			check(fmt.Errorf("unknown motion %s: %w", entry.MotionID, err))
		}
		planPath := filepath.Join(outDir, entry.ID+".plan.json")
		videoPath := filepath.Join(outDir, entry.ID+".mp4")
		check(writePlan(planPath, entry))
		output, err := exec.Command(chronon, "validate", "--plan", planPath, "--assets-root", assetsRoot).CombinedOutput()
		if err != nil {
			check(fmt.Errorf("validate %s: %w\n%s", entry.ID, err, output))
		}
		entry.Plan = filepath.Base(planPath)
		if *render {
			cmd := exec.Command(chronon, "render", "--plan", planPath, "--assets-root", assetsRoot,
				"--backend", "software", "--profile", "preview", "--hardware", "none", "-o", videoPath)
			output, err = cmd.CombinedOutput()
			if err != nil {
				check(fmt.Errorf("render %s: %w\n%s", entry.ID, err, output))
			}
			entry.Video = filepath.Base(videoPath)
			fmt.Printf("rendered %s\n", videoPath)
		} else {
			fmt.Printf("compiled and validated %s\n", planPath)
		}
	}
	manifest, err := json.MarshalIndent(struct {
		Schema  string   `json:"schema"`
		Family  string   `json:"family"`
		Canvas  string   `json:"canvas"`
		Phrase  string   `json:"phrase"`
		Entries []sample `json:"entries"`
	}{"renderinggen.web-white-page-gallery.v1", "web", "960x540 @ 24 fps; 3 seconds", phrase, samples}, "", "  ")
	check(err)
	check(os.WriteFile(filepath.Join(outDir, "manifest.json"), append(manifest, '\n'), 0o644))
	fmt.Printf("wrote %d white-page phrase samples to %s\n", len(samples), outDir)
}

func writePlan(path string, entry *sample) error {
	raw, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
		PlanID: "web-white-page-" + entry.ID,
		Width:  960, Height: 540, FPSNum: 24, FPSDen: 1, DurationMS: 3000,
		OutputProfileID: "preview",
		Background:      &renderbatch.Surface{Kind: "color", Color: []float64{1, 1, 1, 1}},
		Items: []renderbatch.PlanItem{{
			ID: "phrase", Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE",
			PresetID: "phrase_default", MotionID: entry.MotionID, Text: phrase,
			Params:  map[string]any{"font_family": "poppins", "font_size_px": 46.0, "glow_size": 0.0, "stroke_size": 0.0, "shadow_opacity": 0.0},
			StartMS: 0, EndMS: 3000,
		}},
	})
	if err != nil {
		return err
	}
	compiled, err := overlay.CompileSemantic(raw)
	if err != nil {
		return err
	}
	if compiled.Plan.Schema != "chronon.render-plan.v2" && compiled.Plan.Schema != "chronon.render-plan.v3" {
		return fmt.Errorf("compiler emitted unexpected Chronon schema %q", compiled.Plan.Schema)
	}
	for i := range compiled.Plan.Layers {
		layer := &compiled.Plan.Layers[i]
		if layer.Type == "text" && layer.Style != nil {
			layer.Style.Fill = "#17212B"
			layer.Style.Stroke = nil
			layer.Style.Shadow = nil
			layer.Style.Glow = nil
		}
	}
	data, err := json.MarshalIndent(compiled.Plan, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func repoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "RenderingGen", "renderinggen", "go.mod")); err == nil {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			return "", fmt.Errorf("cannot locate monorepo root from %s", cwd)
		}
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
