// Command date-animation exposes the approved multi-date animation catalog and
// the stable random selector used when a plan carries more than one date. It
// can also materialize the selected style with the real dates and render it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/dateanim"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("date-animation", flag.ContinueOnError)
	flags.SetOutput(stderr)
	datesFlag := flags.String("dates", "", "comma-separated date sequence")
	textFlag := flags.String("text", "", "editorial text; four-digit years are extracted automatically")
	planID := flags.String("plan-id", "", "stable plan identity used by the selector")
	videoID := flags.String("video-id", "", "stable video identity used by the selector")
	groupID := flags.String("group-id", "dates", "date group identity used by the selector")
	list := flags.Bool("list", false, "list the approved styles")
	jsonOut := flags.Bool("json", false, "emit JSON")
	planOut := flags.String("plan-out", "", "write the materialized chronon.render-plan.v3 to this path")
	renderOut := flags.String("render", "", "render the materialized plan to this MP4")
	chrononBinary := flags.String("chronon", "", "chronon3d_cli path (also CHRONON_BIN)")
	assetsRoot := flags.String("assets-root", "", "ChrononTemplate root used for fonts (also CHRONON_TEMPLATE_ROOT)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *list {
		catalog, err := dateanim.Load()
		if err != nil {
			fmt.Fprintf(stderr, "date-animation: %v\n", err)
			return 1
		}
		if *jsonOut {
			if err := json.NewEncoder(stdout).Encode(catalog); err != nil {
				fmt.Fprintf(stderr, "date-animation: %v\n", err)
				return 1
			}
			return 0
		}
		for _, style := range catalog.Styles {
			fmt.Fprintf(stdout, "%s\t%s\tfont=%.0f\t%d/%d\n", style.ID, style.Title, style.FontSize, style.FPS, style.Frames)
		}
		return 0
	}
	if *textFlag != "" && *datesFlag != "" {
		fmt.Fprintln(stderr, "date-animation: use either -dates or -text, not both")
		return 2
	}

	if *planOut != "" || *renderOut != "" {
		return runMaterialized(*datesFlag, *textFlag, *planID, *videoID, *groupID,
			*planOut, *renderOut, *chrononBinary, *assetsRoot, *jsonOut, stdout, stderr)
	}

	var style dateanim.Style
	var err error
	if *textFlag != "" {
		style, err = dateanim.SelectFromText(*planID, *videoID, *groupID, *textFlag)
	} else {
		style, err = dateanim.Select(*planID, *videoID, *groupID, splitDates(*datesFlag))
	}
	if err != nil {
		fmt.Fprintf(stderr, "date-animation: %v\n", err)
		return 1
	}
	if *jsonOut {
		if err := json.NewEncoder(stdout).Encode(style); err != nil {
			fmt.Fprintf(stderr, "date-animation: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "%s\t%s\tfont=%.0f\t%d/%d\n", style.ID, style.Title, style.FontSize, style.FPS, style.Frames)
	return 0
}

func runMaterialized(datesFlag, textFlag, planID, videoID, groupID, planOut, renderOut, chrononBinary, assetsRoot string, jsonOut bool, stdout, stderr io.Writer) int {
	var materialized dateanim.Materialized
	var err error
	if textFlag != "" {
		materialized, err = dateanim.MaterializeFromText(planID, videoID, groupID, textFlag)
	} else {
		materialized, err = dateanim.Materialize(planID, videoID, groupID, splitDates(datesFlag))
	}
	if err != nil {
		fmt.Fprintf(stderr, "date-animation: %v\n", err)
		return 1
	}
	if renderOut != "" && planOut == "" {
		planOut = renderOut + ".plan.json"
	}
	if planOut == "" {
		fmt.Fprintln(stderr, "date-animation: -plan-out or -render is required")
		return 2
	}
	if dir := filepath.Dir(planOut); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "date-animation: create plan directory: %v\n", err)
			return 1
		}
	}
	if err := os.WriteFile(planOut, materialized.PlanBytes(), 0o644); err != nil {
		fmt.Fprintf(stderr, "date-animation: write plan: %v\n", err)
		return 1
	}
	if renderOut != "" {
		if err := renderPlan(materialized.PlanBytes(), planOut, renderOut, chrononBinary, assetsRoot, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "date-animation: %v\n", err)
			return 1
		}
	}
	if jsonOut {
		result := struct {
			Style dateanim.Style `json:"style"`
			Dates []string       `json:"dates"`
			Plan  string         `json:"plan"`
			Video string         `json:"video,omitempty"`
		}{Style: materialized.Style, Dates: materialized.Dates, Plan: planOut, Video: renderOut}
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintf(stderr, "date-animation: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "%s\t%s\tfont=%.0f\t%d/%d\tdates=%s\tplan=%s",
		materialized.Style.ID, materialized.Style.Title, materialized.Style.FontSize,
		materialized.Style.FPS, materialized.Style.Frames,
		strings.Join(materialized.Dates, ","), planOut)
	if renderOut != "" {
		fmt.Fprintf(stdout, "\tvideo=%s", renderOut)
	}
	fmt.Fprintln(stdout)
	return 0
}

func renderPlan(plan []byte, planPath, renderPath, chrononBinary, assetsRoot string, stdout, stderr io.Writer) error {
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
	// Renderer progress belongs on stderr: with -json, stdout must stay a
	// single machine-readable result for the job script that consumes it.
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

func splitDates(value string) []string {
	var dates []string
	for _, date := range strings.Split(value, ",") {
		date = strings.TrimSpace(date)
		if date != "" {
			dates = append(dates, date)
		}
	}
	return dates
}
