// Command editorial-map drives the vector editorial map lane:
// chrononplugin.editorial-map-scene.v1 -> ChrononTemplate RenderPlan -> MP4.
//
// The DTO already carries canvas-space rings, labels and effects. RenderingGen
// only orchestrates the two native binaries; it never parses GeoJSON or
// projects geography itself.
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
)

const sceneSchema = "chrononplugin.editorial-map-scene.v1"

const emitTimeout = 90 * time.Second

type sceneDocument struct {
	Schema     string    `json:"schema"`
	Style      string    `json:"style"`
	Canvas     []float64 `json:"canvas"`
	FPS        float64   `json:"fps"`
	FrameCount int       `json:"frame_count"`
	Title      string    `json:"title"`
	Subtitle   string    `json:"subtitle"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("editorial-map", flag.ContinueOnError)
	flags.SetOutput(stderr)
	scenePath := flags.String("scene", "", "chrononplugin.editorial-map-scene.v1 JSON")
	outDir := flags.String("out-dir", "", "output directory (default out/editorial-map)")
	emit := flags.String("emit", "", "chronontemplate_emit_editorial_map path (also CHRONONTEMPLATE_EMIT_EDITORIAL_MAP)")
	render := flags.String("render", "", "render the plan to this MP4")
	chronon := flags.String("chronon", "", "chronon3d_cli path (also CHRONON_BIN)")
	assetsRoot := flags.String("assets-root", "", "ChrononTemplate root used for fonts (also CHRONON_TEMPLATE_ROOT)")
	backend := flags.String("backend", "software", "Chronon3D backend: software or vulkan")
	jsonOut := flags.Bool("json", false, "emit one JSON result")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*scenePath) == "" {
		fmt.Fprintln(stderr, "editorial-map: -scene is required")
		return 2
	}
	document, err := readScene(*scenePath)
	if err != nil {
		fmt.Fprintf(stderr, "editorial-map: %v\n", err)
		return 1
	}
	directory := strings.TrimSpace(*outDir)
	if directory == "" {
		directory = filepath.Join("out", "editorial-map")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		fmt.Fprintf(stderr, "editorial-map: create output directory: %v\n", err)
		return 1
	}
	slug := slugify(document.Style)
	if slug == "" {
		slug = "editorial-map"
	}
	planPath := filepath.Join(directory, slug+".plan.json")
	emitter := resolveEmitter(*emit)
	if emitter == "" {
		fmt.Fprintln(stderr, "editorial-map: emitter not found; pass -emit or set CHRONONTEMPLATE_EMIT_EDITORIAL_MAP")
		return 1
	}
	if err := emitPlan(emitter, *scenePath, planPath); err != nil {
		fmt.Fprintf(stderr, "editorial-map: %v\n", err)
		return 1
	}
	if *render != "" {
		if err := renderPlan(planPath, *render, *chronon, *assetsRoot, *backend, document.FrameCount, stderr); err != nil {
			fmt.Fprintf(stderr, "editorial-map: %v\n", err)
			return 1
		}
	}
	result := struct {
		Style    string  `json:"style"`
		Title    string  `json:"title,omitempty"`
		Subtitle string  `json:"subtitle,omitempty"`
		Frames   int     `json:"frames"`
		FPS      float64 `json:"fps"`
		Scene    string  `json:"scene"`
		Plan     string  `json:"plan"`
		Video    string  `json:"video,omitempty"`
	}{Style: document.Style, Title: document.Title, Subtitle: document.Subtitle,
		Frames: document.FrameCount, FPS: document.FPS, Scene: *scenePath,
		Plan: planPath, Video: *render}
	if *jsonOut {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintf(stderr, "editorial-map: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "%s\t%d frames @ %.0f fps\tscene=%s\tplan=%s",
		result.Style, result.Frames, result.FPS, result.Scene, result.Plan)
	if *render != "" {
		fmt.Fprintf(stdout, "\tvideo=%s", *render)
	}
	fmt.Fprintln(stdout)
	return 0
}

func readScene(path string) (sceneDocument, error) {
	var document sceneDocument
	raw, err := os.ReadFile(path)
	if err != nil {
		return document, fmt.Errorf("read scene: %w", err)
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return document, fmt.Errorf("parse scene: %w", err)
	}
	if document.Schema != sceneSchema {
		return document, fmt.Errorf("scene schema %q, want %q", document.Schema, sceneSchema)
	}
	if strings.TrimSpace(document.Style) == "" {
		return document, fmt.Errorf("scene style is empty")
	}
	if len(document.Canvas) != 2 || document.Canvas[0] <= 0 || document.Canvas[1] <= 0 {
		return document, fmt.Errorf("scene canvas must be [width, height]")
	}
	if document.FrameCount < 2 {
		return document, fmt.Errorf("scene frame_count must be at least 2")
	}
	if document.FPS <= 0 {
		return document, fmt.Errorf("scene fps must be positive")
	}
	return document, nil
}

func emitPlan(emitter, scenePath, planPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), emitTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, emitter, scenePath, planPath)
	output, err := command.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("emitter: %w (%v): %s", ctxErr, err, strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("emitter: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if info, err := os.Stat(planPath); err != nil || info.Size() == 0 {
		return fmt.Errorf("emitter produced no plan at %s", planPath)
	}
	return nil
}

func renderPlan(planPath, renderPath, chrononBinary, assetsRoot, backend string, frames int, stderr io.Writer) error {
	if backend != "software" && backend != "vulkan" {
		return fmt.Errorf("backend %q, want software or vulkan", backend)
	}
	binary := cli.ResolveChronon(chrononBinary)
	if binary == "" {
		return fmt.Errorf("chronon3d_cli not found; pass -chronon or set CHRONON_BIN")
	}
	root := cli.ResolveAssetsRoot(assetsRoot)
	if root == "" {
		return fmt.Errorf("ChrononTemplate root not found; pass -assets-root or set CHRONON_TEMPLATE_ROOT")
	}
	if directory := filepath.Dir(renderPath); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create render directory: %w", err)
		}
	}
	command := exec.Command(binary, "render",
		"--plan", planPath,
		"--output", renderPath,
		"--assets-root", root,
		"--backend", backend,
		"--start-frame", "0",
		"--end-frame", strconv.Itoa(frames-1),
	)
	command.Stdout = stderr
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("render failed: %w", err)
	}
	if info, err := os.Stat(renderPath); err != nil || info.Size() == 0 {
		return fmt.Errorf("renderer produced no video at %s", renderPath)
	}
	return nil
}

func resolveEmitter(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("CHRONONTEMPLATE_EMIT_EDITORIAL_MAP"); env != "" {
		return env
	}
	for _, candidate := range []string{
		"../../ChrononTemplate/build/dev-fast/chronontemplate_emit_editorial_map",
		"../ChrononTemplate/build/dev-fast/chronontemplate_emit_editorial_map",
		"ChrononTemplate/build/dev-fast/chronontemplate_emit_editorial_map",
		"../../ChrononTemplate/build/release/chronontemplate_emit_editorial_map",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if absolute, err := filepath.Abs(candidate); err == nil {
				return absolute
			}
			return candidate
		}
	}
	if binary, err := exec.LookPath("chronontemplate_emit_editorial_map"); err == nil {
		return binary
	}
	return ""
}

func slugify(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
