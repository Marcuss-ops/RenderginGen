// Command map-tactical-fill builds ChrononTemplate's native tactical territory
// fill for a named place. It resolves the Natural Earth feature, projects its
// outer rings into canvas space, writes the sample contract and lowers it
// through chronontemplate_emit_tactical_fill. Optionally it renders the plan.
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
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/tacticalfill"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("map-tactical-fill", flag.ContinueOnError)
	flags.SetOutput(stderr)
	place := flags.String("place", "", "Natural Earth name or country:XXX / admin1:country:name id")
	outDir := flags.String("out-dir", "", "output directory (default out/tactical-fill)")
	title := flags.String("title", "", "title override (default: place name in upper case)")
	subtitle := flags.String("subtitle", "", "subtitle override (default: parent country for admin-1)")
	padding := flags.Float64("padding", tacticalfill.DefaultPadding, "fit padding inside the territory box")
	emit := flags.String("emit", "", "chronontemplate_emit_tactical_fill path (also CHRONONTEMPLATE_EMIT_TACTICAL_FILL)")
	render := flags.String("render", "", "render the plan to this MP4")
	chronon := flags.String("chronon", "", "chronon3d_cli path (also CHRONON_BIN)")
	assetsRoot := flags.String("assets-root", "", "ChrononTemplate root used for fonts (also CHRONON_TEMPLATE_ROOT)")
	jsonOut := flags.Bool("json", false, "emit one JSON result")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*place) == "" {
		fmt.Fprintln(stderr, "map-tactical-fill: -place is required")
		return 2
	}
	registry, err := geofeatures.DefaultRegistry()
	if err != nil {
		fmt.Fprintf(stderr, "map-tactical-fill: %v\n", err)
		return 1
	}
	sample, feature, err := tacticalfill.Build(registry, *place, tacticalfill.Options{
		Title: *title, Subtitle: *subtitle, Padding: *padding,
	})
	if err != nil {
		fmt.Fprintf(stderr, "map-tactical-fill: %v\n", err)
		return 1
	}
	directory := strings.TrimSpace(*outDir)
	if directory == "" {
		directory = filepath.Join("out", "tactical-fill")
	}
	slug := tacticalfill.Slug(feature.Name)
	if slug == "" {
		slug = "place"
	}
	samplePath := filepath.Join(directory, slug+".sample.json")
	planPath := filepath.Join(directory, slug+".plan.json")
	if err := tacticalfill.Write(sample, samplePath); err != nil {
		fmt.Fprintf(stderr, "map-tactical-fill: %v\n", err)
		return 1
	}
	emitter := tacticalfill.ResolveEmitter(*emit)
	if emitter == "" {
		fmt.Fprintln(stderr, "map-tactical-fill: emitter not found; pass -emit or set CHRONONTEMPLATE_EMIT_TACTICAL_FILL")
		return 1
	}
	if err := tacticalfill.Emit(context.Background(), emitter, samplePath, planPath); err != nil {
		fmt.Fprintf(stderr, "map-tactical-fill: %v\n", err)
		return 1
	}
	if *render != "" {
		if err := renderPlan(planPath, *render, *chronon, *assetsRoot, stderr); err != nil {
			fmt.Fprintf(stderr, "map-tactical-fill: %v\n", err)
			return 1
		}
	}
	result := struct {
		Place    string `json:"place"`
		Feature  string `json:"feature_id"`
		Title    string `json:"title"`
		Subtitle string `json:"subtitle,omitempty"`
		Regions  int    `json:"regions"`
		Points   int    `json:"points"`
		Sample   string `json:"sample"`
		Plan     string `json:"plan"`
		Video    string `json:"video,omitempty"`
	}{Place: *place, Feature: feature.ID, Title: sample.Title, Subtitle: sample.Subtitle,
		Regions: len(sample.Regions), Points: pointCount(sample), Sample: samplePath, Plan: planPath, Video: *render}
	if *jsonOut {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintf(stderr, "map-tactical-fill: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "%s\t%s\t%d regions/%d points\tsample=%s\tplan=%s",
		feature.Name, result.Title, result.Regions, result.Points, samplePath, planPath)
	if *render != "" {
		fmt.Fprintf(stdout, "\tvideo=%s", *render)
	}
	fmt.Fprintln(stdout)
	return 0
}

func pointCount(sample tacticalfill.Sample) int {
	total := 0
	for _, region := range sample.Regions {
		total += len(region.Points)
	}
	return total
}

func renderPlan(planPath, renderPath, chrononBinary, assetsRoot string, stderr io.Writer) error {
	binary := resolveChronon(chrononBinary)
	if binary == "" {
		return fmt.Errorf("chronon3d_cli not found; pass -chronon or set CHRONON_BIN")
	}
	root := resolveAssetsRoot(assetsRoot)
	if root == "" {
		return fmt.Errorf("ChrononTemplate root not found; pass -assets-root or set CHRONON_TEMPLATE_ROOT")
	}
	if dir := filepath.Dir(renderPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create render directory: %w", err)
		}
	}
	command := exec.Command(binary, "render-plan",
		"--input", planPath,
		"--output", renderPath,
		"--assets-root", root,
		"--backend", "vulkan",
		"--rate-control", "crf",
		"--crf", "14",
		"--encode-preset", "slow",
	)
	command.Stdout = stderr
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("render failed: %w", err)
	}
	return nil
}

func resolveChronon(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("CHRONON_BIN"); env != "" {
		return env
	}
	for _, candidate := range []string{
		"../../Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli",
		"../Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli",
		"Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if absolute, err := filepath.Abs(candidate); err == nil {
				return absolute
			}
			return candidate
		}
	}
	if binary, err := exec.LookPath("chronon3d_cli"); err == nil {
		return binary
	}
	return ""
}

func resolveAssetsRoot(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("CHRONON_TEMPLATE_ROOT"); env != "" {
		return env
	}
	for _, candidate := range []string{"../../ChrononTemplate", "../ChrononTemplate", "ChrononTemplate"} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			if absolute, err := filepath.Abs(candidate); err == nil {
				return absolute
			}
			return candidate
		}
	}
	return ""
}
