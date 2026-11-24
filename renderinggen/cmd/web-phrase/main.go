// Command web-phrase produces the RenderingGen web paragraph long/short phrase
// lane: it writes the semantic overlay plan, compiles it through the production
// worker compiler, and optionally renders the concrete Chronon plan.
//
// The lane exists because ChrononTemplate owns the native web-page recipes while
// RenderingGen had no producer that could express their long/short phrase
// grammar. It reuses the worker's existing text, shape and phrase primitives;
// there is no second renderer and no hand-written Chronon plan.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/cli"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/webphrase"
)

type artifact struct {
	ID       string `json:"id"`
	Mode     string `json:"mode"`
	Theme    string `json:"theme"`
	Frames   int    `json:"frames"`
	Duration string `json:"duration"`
	Plan     string `json:"plan"`
	Video    string `json:"video,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("web-phrase", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mode := flags.String("mode", "both", "long, short or both")
	theme := flags.String("theme", "light", "light or dark")
	outDir := flags.String("out-dir", filepath.Join("out", "web-phrase"), "directory for semantic plans, compiled plans and videos")
	render := flags.Bool("render", false, "render each compiled plan to MP4")
	chronon := flags.String("chronon", "", "chronon3d_cli path (also CHRONON_BIN)")
	assetsRoot := flags.String("assets-root", "", "ChrononTemplate root used for fonts (also CHRONON_TEMPLATE_ROOT)")
	list := flags.Bool("list", false, "list the available variants")
	jsonOut := flags.Bool("json", false, "emit one JSON result")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *list {
		fmt.Fprintln(stdout, "long\tweb article page, long highlighted sentence, 8 s")
		fmt.Fprintln(stdout, "short\tweb impact plate, short highlighted phrase, 7 s")
		return 0
	}

	scenes, err := selectScenes(*mode, *theme)
	if err != nil {
		fmt.Fprintf(stderr, "web-phrase: %v\n", err)
		return 2
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "web-phrase: create output directory: %v\n", err)
		return 1
	}

	results := make([]artifact, 0, len(scenes))
	for _, scene := range scenes {
		raw, err := webphrase.BuildPlan(scene)
		if err != nil {
			fmt.Fprintf(stderr, "web-phrase: build %s: %v\n", scene.ID, err)
			return 1
		}
		overlayPlanPath := filepath.Join(*outDir, scene.ID+".overlay-plan.json")
		if err := os.WriteFile(overlayPlanPath, raw, 0o644); err != nil {
			fmt.Fprintf(stderr, "web-phrase: write semantic plan: %v\n", err)
			return 1
		}

		compiled, err := overlay.CompileSemantic(raw)
		if err != nil {
			fmt.Fprintf(stderr, "web-phrase: compile %s: %v\n", scene.ID, err)
			return 1
		}
		videoPath := filepath.Join(*outDir, scene.ID+".mp4")
		if *render {
			compiled.Plan.Output.Path = videoPath
		}
		planBytes, err := compiled.Plan.MarshalIndent("", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "web-phrase: marshal %s: %v\n", scene.ID, err)
			return 1
		}
		planPath := filepath.Join(*outDir, scene.ID+".plan.json")
		if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
			fmt.Fprintf(stderr, "web-phrase: write compiled plan: %v\n", err)
			return 1
		}
		if *render {
			if err := renderPlan(planPath, videoPath, scene.Frames, *chronon, *assetsRoot, stderr); err != nil {
				fmt.Fprintf(stderr, "web-phrase: render %s: %v\n", scene.ID, err)
				return 1
			}
		}
		results = append(results, artifact{
			ID: scene.ID, Mode: scene.Mode, Theme: scene.Theme, Frames: scene.Frames,
			Duration: fmt.Sprintf("%.2fs", float64(scene.Frames)/webphrase.FPS),
			Plan:     planPath, Video: func() string {
				if *render {
					return videoPath
				}
				return ""
			}(),
		})
	}

	if *jsonOut {
		if err := json.NewEncoder(stdout).Encode(results); err != nil {
			fmt.Fprintf(stderr, "web-phrase: %v\n", err)
			return 1
		}
		return 0
	}
	for _, result := range results {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%d frames\t%s\tplan=%s", result.ID, result.Mode, result.Theme, result.Frames, result.Duration, result.Plan)
		if result.Video != "" {
			fmt.Fprintf(stdout, "\tvideo=%s", result.Video)
		}
		fmt.Fprintln(stdout)
	}
	return 0
}

func selectScenes(mode, theme string) ([]webphrase.Scene, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	theme = strings.ToLower(strings.TrimSpace(theme))
	long, short := webphrase.DefaultLong(), webphrase.DefaultShort()
	long.Theme, short.Theme = theme, theme
	switch mode {
	case "long":
		return []webphrase.Scene{long}, nil
	case "short":
		return []webphrase.Scene{short}, nil
	case "both", "":
		return []webphrase.Scene{long, short}, nil
	default:
		return nil, fmt.Errorf("mode %q must be long, short or both", mode)
	}
}

func renderPlan(planPath, renderPath string, frames int, chrononBinary, assetsRoot string, stderr io.Writer) error {
	binary := cli.ResolveChronon(chrononBinary)
	if binary == "" {
		return fmt.Errorf("chronon3d_cli not found; pass -chronon or set CHRONON_BIN")
	}
	root := cli.ResolveAssetsRoot(assetsRoot)
	if root == "" {
		return fmt.Errorf("ChrononTemplate root not found; pass -assets-root or set CHRONON_TEMPLATE_ROOT")
	}
	if err := os.MkdirAll(filepath.Dir(renderPath), 0o755); err != nil {
		return fmt.Errorf("create render directory: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "render",
		"--plan", planPath,
		"--output", renderPath,
		"--assets-root", root,
		"--backend", "software",
		"--encoder-backend", "pipe",
		"--hardware", "none",
		"--gpu-hot-path-mode", "auto",
		"--encode-preset", "ultrafast",
		"--start-frame", "0",
		"--end-frame", strconv.Itoa(frames-1),
	)
	// Renderer progress belongs on stderr so -json keeps stdout machine-readable.
	command.Stdout = stderr
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("renderer timed out: %w", ctxErr)
		}
		return fmt.Errorf("render failed: %w", err)
	}
	if info, err := os.Stat(renderPath); err != nil || info.Size() == 0 {
		return fmt.Errorf("renderer produced no video at %s", renderPath)
	}
	return nil
}
