// Command modernmap-demo builds one ModernMap1 semantic plan for a named place,
// compiles it through the worker's own compiler, and writes both the semantic
// input and the concrete Chronon render plan. It exists so a map can be
// verified end to end without hand-writing a plan, and so the same payload can
// be submitted to the queue unchanged.
//
// A logo is optional and never fetched: pass -logo with a local PNG and the
// command stages it as a content-addressed asset next to the plans.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

func main() {
	place := flag.String("place", "Italy", "Natural Earth selector (country name, country id, or admin1:id)")
	outDir := flag.String("out-dir", "/tmp/modernmap1", "output directory")
	width := flag.Int("width", 1920, "canvas width")
	height := flag.Int("height", 1080, "canvas height")
	fps := flag.Int("fps", 30, "frames per second")
	seconds := flag.Float64("seconds", 8, "clip duration in seconds")
	logo := flag.String("logo", "", "optional local PNG emblem placed inside the territory")
	flag.Parse()

	plan := buildPlan(*place, *width, *height, *fps, *seconds)
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fail("create output directory: %v", err)
	}
	if *logo != "" {
		if err := stageLogo(plan, *logo, *outDir); err != nil {
			fail("stage logo: %v", err)
		}
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		fail("encode semantic plan: %v", err)
	}
	result, err := overlay.CompileSemantic(raw)
	if err != nil {
		fail("compile semantic plan: %v", err)
	}
	slug := slugify(*place)
	semanticPath := filepath.Join(*outDir, slug+".semantic.json")
	renderPath := filepath.Join(*outDir, slug+".render-plan.json")
	if err := os.WriteFile(semanticPath, raw, 0o644); err != nil {
		fail("write semantic plan: %v", err)
	}
	compiled, err := result.Plan.MarshalIndent("", "  ")
	if err != nil {
		fail("encode render plan: %v", err)
	}
	if err := os.WriteFile(renderPath, compiled, 0o644); err != nil {
		fail("write render plan: %v", err)
	}
	output := struct {
		Place    string `json:"place"`
		Layers   int    `json:"layers"`
		Logo     string `json:"logo,omitempty"`
		Semantic string `json:"semantic_plan"`
		Render   string `json:"render_plan"`
	}{Place: *place, Layers: len(result.Plan.Layers), Logo: *logo, Semantic: semanticPath, Render: renderPath}
	encoded, _ := json.Marshal(output)
	fmt.Println(string(encoded))
}

func buildPlan(place string, width, height, fps int, seconds float64) map[string]any {
	durationMS := int64(seconds * 1000)
	return map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "modernmap1-" + slugify(place),
		"video_id":       "modernmap1-" + slugify(place),
		"width":          width, "height": height,
		"fps_num": fps, "fps_den": 1,
		"duration_ms": durationMS,
		"background":  map[string]any{"kind": "color", "color": []float64{0.03, 0.05, 0.08, 1}},
		"items": []any{map[string]any{
			"id": "shape-map", "kind": "map", "template_id": "MAP",
			"start_ms": 0, "end_ms": durationMS,
			"params": map[string]any{
				"map_type":  overlay.MapTypeModernMap1,
				"map_place": place,
			},
			"map": map[string]any{
				"provider": "local", "source_id": "natural-earth", "source_license": "public-domain",
				"center":      map[string]any{"latitude": 0.0, "longitude": 0.0},
				"zoom":        0,
				"width":       width,
				"height":      height,
				"attribution": "Natural Earth",
				"pins":        []any{},
			},
		}},
	}
}

// stageLogo copies the caller's PNG into the job assets root, hashes it, and
// declares it on the map item — the same content-addressed shape the worker
// materializes. Nothing is fetched and the original file is untouched.
func stageLogo(plan map[string]any, source, outDir string) error {
	bytes, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(bytes)
	assetsDir := filepath.Join(outDir, "assets", "logos")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return err
	}
	name := filepath.Base(source)
	logical := filepath.ToSlash(filepath.Join("assets", "logos", name))
	if err := os.WriteFile(filepath.Join(outDir, logical), bytes, 0o644); err != nil {
		return err
	}
	item := plan["items"].([]any)[0].(map[string]any)
	params := item["params"].(map[string]any)
	params["map_logo"] = "map-logo"
	item["asset_refs"] = []any{map[string]any{
		"asset_id":   "map-logo",
		"sha256":     hex.EncodeToString(digest[:]),
		"url":        logical,
		"media_type": "image/png",
	}}
	return nil
}

func slugify(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == ' ' || r == '_' || r == '-' || r == ':' || r == '/':
			builder.WriteRune('-')
		}
	}
	slug := strings.Trim(builder.String(), "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if slug == "" {
		slug = "place"
	}
	return slug
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "modernmap-demo: "+format+"\n", args...)
	os.Exit(1)
}
