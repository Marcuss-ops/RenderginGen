// Command render-brush-phrase-gallery compiles the phrase-ready Brush V1
// catalog variants through RenderingGen, then renders short and wrapped-long
// examples with Chronon3D.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/renderbatch"
)

const (
	shortPhrase = "Anche 3.000€ al mese"
	longPhrase  = "Anche 3.000€ al mese: questa frase lunga si distribuisce su più righe e mantiene il testo chiaro, centrato e accompagnato dal tratto Brush."
)

type galleryEntry struct {
	MotionID string `json:"motion_id"`
	Phrase   string `json:"phrase"`
	Plan     string `json:"plan"`
	Video    string `json:"video"`
}

func main() {
	render := flag.Bool("render", false, "render the generated plans to MP4 as well as compiling and validating them")
	flag.Parse()
	root, err := repoRoot()
	check(err)
	outDir := filepath.Join(root, "RenderingGen", "out", "brush_v1_phrase_gallery")
	assetsRoot := filepath.Join(root, "Chronon3d")
	chronon := os.Getenv("CHRONON3D_CLI")
	if chronon == "" {
		chronon = filepath.Join(root, "Chronon3d", "build", "chronon", "linux-video-fast-dev", "apps", "chronon3d_cli", "chronon3d_cli")
	}
	if _, err := os.Stat(chronon); err != nil {
		check(fmt.Errorf("chronon3d_cli unavailable at %s: set CHRONON3D_CLI to a built CLI", chronon))
	}
	check(os.MkdirAll(outDir, 0o755))

	var entries []galleryEntry
	for _, id := range motion.Registry.VisualAccentsV1MotionIDs("brush_v1") {
		if !strings.HasPrefix(id, "brush_phrase_") {
			continue
		}
		for _, sample := range []struct{ suffix, phrase string }{{"short", shortPhrase}, {"long", longPhrase}} {
			stem := id + "_" + sample.suffix
			planPath := filepath.Join(outDir, stem+".plan.json")
			videoPath := filepath.Join(outDir, stem+".mp4")
			check(writePlan(planPath, id, sample.phrase))
			args := []string{"validate", "--plan", planPath, "--assets-root", assetsRoot}
			cmd := exec.Command(chronon, args...)
			cmd.Dir = root
			output, err := cmd.CombinedOutput()
			if err != nil {
				check(fmt.Errorf("validate %s: %w\n%s", stem, err, output))
			}
			if *render {
				args = []string{"render", "--plan", planPath, "--assets-root", assetsRoot, "--backend", "software", "--profile", "preview", "--hardware", "none", "-o", videoPath}
				cmd := exec.Command(chronon, args...)
				cmd.Dir = root
				output, err = cmd.CombinedOutput()
				if err != nil {
					check(fmt.Errorf("render %s: %w\n%s", stem, err, output))
				}
			}
			entry := galleryEntry{MotionID: id, Phrase: sample.phrase, Plan: filepath.Base(planPath)}
			if *render {
				entry.Video = filepath.Base(videoPath)
				fmt.Printf("rendered %s\n", videoPath)
			} else {
				fmt.Printf("compiled and validated %s\n", planPath)
			}
			entries = append(entries, entry)
		}
	}
	manifest, err := json.MarshalIndent(struct {
		Schema  string         `json:"schema"`
		Family  string         `json:"family"`
		Entries []galleryEntry `json:"entries"`
	}{"renderinggen.brush-phrase-gallery.v1", "brush_v1", entries}, "", "  ")
	check(err)
	check(os.WriteFile(filepath.Join(outDir, "manifest.json"), append(manifest, '\n'), 0o644))
	fmt.Printf("wrote %d Brush V1 phrase examples to %s\n", len(entries), outDir)
}

func writePlan(path, motionID, phrase string) error {
	raw, err := renderbatch.BuildPlan(renderbatch.PlanSpec{
		PlanID: "brush-phrase-gallery-" + motionID + "-" + suffixFor(phrase),
		Width:  1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationMS: 5000,
		OutputProfileID: "preview",
		Background:      &renderbatch.Surface{Kind: "color", Color: []float64{0.02, 0.02, 0.025, 1}},
		Items: []renderbatch.PlanItem{{
			ID: "phrase", Kind: "important_phrase", TemplateID: "IMPORTANT_PHRASE",
			PresetID: "phrase_default", MotionID: motionID, Text: phrase,
			Params:  map[string]any{"font_family": "poppins", "font_size_px": 112.0, "glow_size": 0.0, "stroke_size": 0.0},
			StartMS: 0, EndMS: 5000,
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
	data, err := json.MarshalIndent(compiled.Plan, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func suffixFor(phrase string) string {
	if phrase == longPhrase {
		return "long"
	}
	return "short"
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
