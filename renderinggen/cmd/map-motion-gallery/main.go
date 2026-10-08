package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

const (
	canvasWidth    = 1280
	canvasHeight   = 720
	fps            = 24
	durationFrames = 72
	durationMS     = 3000
)

type gallery struct {
	Schema    string `json:"schema"`
	Version   int    `json:"version"`
	CatalogID string `json:"catalog_id"`
	Canvas    struct {
		Width          int   `json:"width"`
		Height         int   `json:"height"`
		FPSNum         int   `json:"fps_num"`
		FPSDen         int   `json:"fps_den"`
		DurationFrames int64 `json:"duration_frames"`
	} `json:"canvas"`
	Provenance map[string]any `json:"provenance"`
	Families   []family       `json:"families"`
}
type family struct {
	ID    string   `json:"id"`
	Items []motion `json:"items"`
}
type motion struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Source string `json:"source"`
	Note   string `json:"note,omitempty"`
}
type manifest struct {
	Schema     string  `json:"schema"`
	Version    int     `json:"version"`
	CatalogID  string  `json:"catalog_id"`
	RenderedAt string  `json:"rendered_at"`
	Entries    []entry `json:"entries"`
}
type entry struct {
	Family     string        `json:"family"`
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	Status     string        `json:"status"`
	Note       string        `json:"note,omitempty"`
	Plan       string        `json:"semantic_plan,omitempty"`
	RenderPlan string        `json:"render_plan,omitempty"`
	MP4        string        `json:"mp4,omitempty"`
	Frames     int64         `json:"frames,omitempty"`
	SHA256     string        `json:"sha256,omitempty"`
	Bytes      int64         `json:"bytes,omitempty"`
	Probe      *mediaSummary `json:"probe,omitempty"`
	Error      string        `json:"error,omitempty"`
}
type mediaSummary struct {
	DurationUS int64  `json:"duration_us"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	FPSNum     int    `json:"fps_num"`
	FPSDen     int    `json:"fps_den"`
	Frames     int    `json:"frames"`
	Codec      string `json:"codec"`
	Container  string `json:"container"`
	ClosedGOP  bool   `json:"closed_gop"`
}

func main() {
	catalogPath := flag.String("catalog", "ChrononTemplate/catalog/map_motion_v1.json", "map motion catalog JSON")
	outDir := flag.String("out", "RenderingGen/renderinggen/out/map_motion_v1", "gallery output directory")
	chrononBin := flag.String("chronon", "", "chronon3d_cli path (defaults to CHRONON3D_CLI or local fast-dev build)")
	familyFilter := flag.String("family", "", "optional family ID filter")
	limit := flag.Int("limit", 0, "optional maximum number of items (0 = all selected)")
	render := flag.Bool("render", true, "render MP4 outputs after compilation")
	backend := flag.String("backend", "software", "Chronon backend (software or vulkan)")
	flag.Parse()

	root, err := filepath.Abs("../..")
	if err != nil {
		log.Fatal(err)
	}
	resolve := func(value string) string {
		if filepath.IsAbs(value) {
			return value
		}
		return filepath.Clean(filepath.Join(root, value))
	}
	repoRoot = root
	catalogFile, outputRoot := resolve(*catalogPath), resolve(*outDir)
	bin := *chrononBin
	if bin == "" {
		bin = os.Getenv("CHRONON3D_CLI")
	}
	if bin == "" {
		bin = filepath.Join(root, "Chronon3d/.tmp/chronon-builds/linux-video-fast-dev/apps/chronon3d_cli/chronon3d_cli")
	}
	if *render {
		if info, statErr := os.Stat(bin); statErr != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			log.Fatalf("executable chronon3d_cli not found: %s", bin)
		}
	}
	if *backend != "software" && *backend != "vulkan" {
		log.Fatalf("unsupported backend %q", *backend)
	}
	for _, path := range []string{filepath.Join(outputRoot, "plans"), filepath.Join(outputRoot, "render-plans"), filepath.Join(outputRoot, "renders"), filepath.Join(outputRoot, "assets", "maps")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			log.Fatal(err)
		}
	}
	data, err := os.ReadFile(catalogFile)
	if err != nil {
		log.Fatal(err)
	}
	var cat gallery
	if err := json.Unmarshal(data, &cat); err != nil {
		log.Fatal(err)
	}
	if cat.Schema != "chronontemplate.map-motion-family.v1" || cat.Version != 1 || cat.CatalogID != "map_motion_v1" {
		log.Fatalf("unsupported map motion catalog: %s v%d %q", cat.Schema, cat.Version, cat.CatalogID)
	}
	if len(cat.Families) != 7 {
		log.Fatalf("catalog has %d families, want 7", len(cat.Families))
	}
	cat.Canvas.Width, cat.Canvas.Height, cat.Canvas.FPSNum, cat.Canvas.FPSDen, cat.Canvas.DurationFrames = canvasWidth, canvasHeight, fps, 1, durationFrames
	selected := make([]family, 0, len(cat.Families))
	total := 0
	for _, fam := range cat.Families {
		if *familyFilter != "" && fam.ID != *familyFilter {
			continue
		}
		if len(fam.Items) == 0 {
			log.Fatalf("family %s is empty", fam.ID)
		}
		selected = append(selected, fam)
		total += len(fam.Items)
	}
	if *familyFilter != "" && len(selected) == 0 {
		log.Fatalf("unknown family %q", *familyFilter)
	}
	if total != 45 && *familyFilter == "" {
		log.Fatalf("catalog declares %d motions, want 45", total)
	}
	if *limit > 0 && *limit < total {
		total = *limit
	}
	entries := make([]entry, 0, total)
	rendered := 0
	for _, fam := range selected {
		for _, item := range fam.Items {
			if *limit > 0 && rendered >= *limit {
				break
			}
			rendered++
			fmt.Printf("[%02d/%02d] %s / %s\n", rendered, total, fam.ID, item.ID)
			e := entry{Family: fam.ID, ID: item.ID, Title: item.Title, Note: item.Note, Status: "compiled"}
			planDir := filepath.Join(outputRoot, "plans", fam.ID)
			concreteDir := filepath.Join(outputRoot, "render-plans", fam.ID)
			renderDir := filepath.Join(outputRoot, "renders", fam.ID)
			for _, dir := range []string{planDir, concreteDir, renderDir} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					log.Fatal(err)
				}
			}
			semantic, plan, assets, err := buildPlan(cat, item, outputRoot, filepath.Join(outputRoot, "assets", "maps"))
			if err != nil {
				e.Status = "compile_failed"
				e.Error = err.Error()
				entries = append(entries, e)
				writeManifest(outputRoot, cat, entries)
				continue
			}
			semanticPath := filepath.Join(planDir, item.ID+".json")
			if err := writeJSON(semanticPath, semantic); err != nil {
				log.Fatal(err)
			}
			e.Plan = rel(outputRoot, semanticPath)
			concretePath := filepath.Join(concreteDir, item.ID+".render-plan.json")
			if err := writeJSON(concretePath, plan); err != nil {
				log.Fatal(err)
			}
			e.RenderPlan = rel(outputRoot, concretePath)
			if !*render {
				e.Status = "compiled_only"
				entries = append(entries, e)
				writeManifest(outputRoot, cat, entries)
				continue
			}
			assetRoot := outputRoot
			// Plans refer to assets/maps/... under their own gallery workspace. Copy once per source.
			for name, source := range assets {
				dest := filepath.Join(assetRoot, filepath.FromSlash(name))
				if _, statErr := os.Stat(dest); os.IsNotExist(statErr) {
					if err := copy(source, dest); err != nil {
						e.Status = "asset_failed"
						e.Error = err.Error()
						break
					}
				}
			}
			if e.Status == "asset_failed" {
				entries = append(entries, e)
				writeManifest(outputRoot, cat, entries)
				continue
			}
			video := filepath.Join(renderDir, item.ID+".mp4")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			cmd := exec.CommandContext(ctx, bin, "render", "--plan", concretePath, "--assets-root", assetRoot, "--backend", *backend, "-o", video)
			cmd.Dir = outputRoot
			cmd.Env = append(os.Environ(), "CHRONON_RECEIPT_VERIFY=normal")
			output, renderErr := cmd.CombinedOutput()
			cancel()
			if renderErr != nil {
				e.Status = "render_failed"
				e.Error = fmt.Sprintf("%v: %s", renderErr, tail(string(output), 2500))
				entries = append(entries, e)
				writeManifest(outputRoot, cat, entries)
				continue
			}
			probeCtx, probeCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			probe, probeErr := media.ProbeFile(probeCtx, video)
			if probeErr == nil {
				probeErr = probe.ValidateOverlay(canvasWidth, canvasHeight, fps, 1)
			}
			if probeErr == nil {
				probeErr = media.ValidateDecode(probeCtx, video)
			}
			if probeErr == nil {
				probeErr = probe.ValidateVisible(probeCtx, video)
			}
			probeCancel()
			if probeErr != nil {
				e.Status = "verification_failed"
				e.Error = probeErr.Error()
				entries = append(entries, e)
				writeManifest(outputRoot, cat, entries)
				continue
			}
			file, err := os.Open(video)
			if err != nil {
				log.Fatal(err)
			}
			h := sha256.New()
			info, err := file.Stat()
			if err != nil {
				log.Fatal(err)
			}
			_, err = io.Copy(h, file)
			_ = file.Close()
			if err != nil {
				log.Fatal(err)
			}
			e.Status = "rendered_verified"
			e.MP4 = rel(outputRoot, video)
			e.Frames = int64(probe.FrameCount)
			e.SHA256 = hex.EncodeToString(h.Sum(nil))
			e.Bytes = info.Size()
			e.Probe = &mediaSummary{DurationUS: probe.DurationUS, Width: probe.Width, Height: probe.Height, FPSNum: probe.FPSNum, FPSDen: probe.FPSDen, Frames: probe.FrameCount, Codec: probe.VideoCodec, Container: probe.Container, ClosedGOP: probe.ClosedGOP}
			entries = append(entries, e)
			writeManifest(outputRoot, cat, entries)
		}
	}
	writeManifest(outputRoot, cat, entries)
	fmt.Printf("gallery: %s (%d items; rendered verified=%d)\n", outputRoot, len(entries), count(entries, "rendered_verified"))
	if count(entries, "compile_failed")+count(entries, "render_failed")+count(entries, "verification_failed")+count(entries, "asset_failed") > 0 || *render && count(entries, "rendered_verified") != len(entries) {
		os.Exit(1)
	}
}

var repoRoot string

func buildPlan(cat gallery, item motion, outputRoot, generatedRoot string) ([]byte, *overlay.Plan, map[string]string, error) {
	baseName := "natural_earth_hypso_relief_water.jpg"
	if item.Source == "nasa" {
		baseName = "nasa_blue_marble_august.jpg"
	}
	abs := filepath.Join(repoRoot, "ChrononTemplate", "catalog", "maps", baseName)
	if _, err := os.Stat(abs); err != nil {
		return nil, nil, nil, fmt.Errorf("source asset %s: %w", abs, err)
	}
	canvas, err := loadCanvas(abs, generatedRoot)
	if err != nil {
		return nil, nil, nil, err
	}
	assetName := strings.TrimSuffix(baseName, filepath.Ext(baseName)) + ".png"
	assetPath := filepath.Join(generatedRoot, assetName)
	if err := savePNG(canvas, assetPath); err != nil {
		return nil, nil, nil, err
	}
	boundsAbs := filepath.Join(repoRoot, "ChrononTemplate", "catalog", "ne_50m_admin_0_countries.geojson")
	boundsName := "natural_earth_boundaries.png"
	boundsOut := filepath.Join(generatedRoot, boundsName)
	if err := makeBoundaries(boundsAbs, boundsOut); err != nil {
		return nil, nil, nil, err
	}
	assets := map[string]string{"assets/maps/" + assetName: assetPath, "assets/maps/" + boundsName: boundsOut}
	fontPath := filepath.Join(repoRoot, "Chronon3d", "assets", "fonts", "Poppins-Bold.ttf")
	if _, err := os.Stat(fontPath); err != nil {
		return nil, nil, nil, fmt.Errorf("font asset %s: %w", fontPath, err)
	}
	assets["assets/fonts/Poppins-Bold.ttf"] = fontPath
	baseSHA, err := hashFile(assetPath)
	if err != nil {
		return nil, nil, nil, err
	}
	boundsSHA, err := hashFile(boundsOut)
	if err != nil {
		return nil, nil, nil, err
	}
	baseURL := filepath.ToSlash(filepath.Join("assets", "maps", assetName))
	boundsURL := filepath.ToSlash(filepath.Join("assets", "maps", boundsName))
	motionID, motionParams := basemapMotion()
	attribution := "Natural Earth public domain · " + item.Source
	if item.Source == "nasa" {
		attribution = "NASA Blue Marble Next Generation · Aug 2004"
	}
	items := []any{
		map[string]any{"id": "basemap", "kind": "image", "template_id": "PRODUCT", "preset_id": "image_focus_in", "motion_id": motionID, "motion_params": motionParams, "start_ms": 0, "end_ms": durationMS, "asset_refs": []any{map[string]any{"asset_id": "basemap", "sha256": baseSHA, "url": baseURL, "media_type": "image/png"}}, "params": map[string]any{"width": canvasWidth, "height": canvasHeight, "fit": "contain", "position": "center"}},
		map[string]any{"id": "country_borders", "kind": "image", "template_id": "PRODUCT", "preset_id": "image_focus_in", "motion_id": "image_fade_reveal", "motion_params": map[string]any{"enter_frames": 34, "exit_frames": 8}, "start_ms": 260, "end_ms": durationMS, "asset_refs": []any{map[string]any{"asset_id": "boundaries", "sha256": boundsSHA, "url": boundsURL, "media_type": "image/png"}}, "params": map[string]any{"width": canvasWidth, "height": canvasHeight, "fit": "contain", "position": "center"}},
	}
	for _, spec := range overlaysFor(item.ID, item.Title, familyFor(cat, item.ID), attribution) {
		items = append(items, spec)
	}
	semantic := map[string]any{"schema_version": "renderinggen.overlay-plan.v1", "plan_id": item.ID, "video_id": "map_motion_v1_" + item.ID, "project_id": "map_motion_v1", "language": "en", "width": canvasWidth, "height": canvasHeight, "fps_num": fps, "fps_den": 1, "duration_ms": durationMS, "output_profile_id": "preview", "items": items}
	raw, err := json.MarshalIndent(semantic, "", "  ")
	if err != nil {
		return nil, nil, nil, err
	}
	result, err := overlay.CompileSemantic(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	result.Plan.Output.Path = filepath.Join("renders", item.ID+".mp4")
	return append(raw, '\n'), result.Plan, assets, nil
}

func basemapMotion() (string, map[string]any) {
	// The gallery's country borders, routes and pins are projected to fixed
	// canvas coordinates. Moving/scaling only the basemap makes them drift away
	// from the geography. Keep the raster transform stationary; animate it with
	// opacity only. True camera moves belong to SemanticMap, which projects the
	// raster, camera and georeferenced pins through one shared world transform.
	return "image_fade_reveal", map[string]any{"enter_frames": 28, "exit_frames": 8}
}

func familyFor(cat gallery, id string) string {
	for _, fam := range cat.Families {
		for _, item := range fam.Items {
			if item.ID == id {
				return fam.ID
			}
		}
	}
	return "map_motion_v1"
}

func overlaysFor(id, title, family, attribution string) []map[string]any {
	const duration = durationMS
	text := func(layerID, value, motionID string, start int, params map[string]any) map[string]any {
		return map[string]any{"id": layerID, "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE", "preset_id": "phrase_default", "motion_id": motionID, "motion_params": map[string]any{"enter_frames": 24, "exit_frames": 8}, "text": value, "start_ms": start, "end_ms": duration, "params": map[string]any{"width": params["width"], "height": params["height"], "font_size_px": params["font_size_px"], "position_x": params["position_x"], "position_y": params["position_y"], "fill": params["color"]}}
	}
	shape := func(layerID, typ string, position []any, width, height float64, fill string, start int, stroke map[string]any) map[string]any {
		if len(position) == 2 {
			if x, ok := position[0].(float64); ok {
				if y, ok := position[1].(float64); ok {
					position = []any{x - canvasWidth/2, y - canvasHeight/2}
				}
			}
		}
		params := map[string]any{"shape": typ, "width": width, "height": height, "position": position, "fill": fill}
		if stroke != nil {
			params["stroke"] = stroke
		}
		return map[string]any{"id": layerID, "kind": "shape", "template_id": "shape_static", "start_ms": start, "end_ms": duration, "params": params}
	}
	credit := "SOURCE: NATURAL EARTH"
	if strings.Contains(attribution, "NASA") {
		credit = "NASA BLUE MARBLE 2004"
	}
	layers := []map[string]any{text("title", title, "character_cascade", 180, map[string]any{"width": 900, "height": 64, "font_size_px": 30, "position_x": 640.0, "position_y": 54.0, "color": "#F6F3E9"}), text("family", strings.ToUpper(strings.ReplaceAll(family, "_", " ")), "fade_in", 500, map[string]any{"width": 650, "height": 34, "font_size_px": 18, "position_x": 640.0, "position_y": 658.0, "color": "#79D6CE"}), text("source_credit", credit, "fade_in", 600, map[string]any{"width": 440, "height": 28, "font_size_px": 14, "position_x": 640.0, "position_y": 695.0, "color": "#E8EEE5"})}
	marker := func(layerID, label string, x, y float64, start int, color string) {
		layers = append(layers, shape(layerID, "ellipse", []any{x, y}, 22, 22, color, start, map[string]any{"color": "#F5F1E8", "width": 2}))
		layers = append(layers, text(layerID+"_label", label, "fade_in", start+100, map[string]any{"width": 260, "height": 40, "font_size_px": 22, "position_x": x + 125, "position_y": y - 20, "color": color}))
	}
	path := func(layerID string, points [][]float64, start int, color string, width float64) {
		for i := 1; i < len(points); i++ {
			commands := []any{
				map[string]any{"type": "move_to", "point": []any{points[i-1][0] - canvasWidth/2, points[i-1][1] - canvasHeight/2}},
				map[string]any{"type": "line_to", "point": []any{points[i][0] - canvasWidth/2, points[i][1] - canvasHeight/2}},
			}
			layers = append(layers, map[string]any{"id": fmt.Sprintf("%s_segment_%02d", layerID, i), "kind": "shape", "template_id": "shape_static", "start_ms": start + (i-1)*150, "end_ms": duration, "params": map[string]any{"shape": "path", "width": canvasWidth, "height": canvasHeight, "position": []any{0.0, 0.0}, "fill": "#00000000", "stroke": map[string]any{"color": color, "width": width}, "path": commands}})
		}
	}
	switch id {
	case "map_world_to_country", "map_country_outline_draw", "map_country_pop_focus", "map_country_lift_25d", "map_country_pulse", "map_region_fill_reveal", "map_region_expand", "map_region_scan", "map_region_outline_glow", "map_region_morph_focus", "map_choropleth_rank", "map_metric_focus", "map_historical_year_transition", "map_historical_border_change":
		france := mapPixel(46.2, 2.2)
		if id == "map_country_lift_25d" {
			layers = append(layers, shape("france_shadow", "ellipse", []any{france[0] + 8, france[1] + 10}, 100, 78, "#05080BCC", 460, nil))
		}
		if id == "map_country_pulse" || id == "map_region_outline_glow" {
			for i, diameter := range []float64{92, 136, 184} {
				layers = append(layers, shape(fmt.Sprintf("focus_pulse_%d", i), "ellipse", []any{france[0], france[1]}, diameter, diameter*0.78, "#00000000", 520+i*250, map[string]any{"color": "#F3C76A", "width": 2}))
			}
		}
		focusColor := "#D7A84A"
		if id == "map_country_outline_draw" || id == "map_region_scan" || id == "map_historical_border_change" {
			focusColor = "#00000000"
		}
		layers = append(layers, shape("france_focus", "ellipse", []any{france[0], france[1]}, 94, 72, focusColor, 420, map[string]any{"color": "#F8E6B4", "width": 3}))
		if id == "map_region_scan" {
			layers = append(layers, shape("region_scan_line", "rect", []any{france[0], france[1]}, 110, 3, "#79D6CE", 700, nil))
		}
		if id == "map_choropleth_rank" || id == "map_historical_year_transition" {
			for i, year := range []string{"1945", "1960", "1990", "TODAY"} {
				layers = append(layers, text(fmt.Sprintf("year_%d", i), year, "fade_in", 500+i*520, map[string]any{"width": 160, "height": 60, "font_size_px": 30, "position_x": 430.0 + float64(i)*140, "position_y": 575.0, "color": []string{"#9BC6B8", "#E7BA6A", "#A7C7E3", "#F3F3EA"}[i]}))
			}
		}
		marker("paris", "PARIS · FRANCE", france[0], france[1], 800, "#F3C76A")
	case "map_city_fly_to", "map_city_pin_drop", "map_city_radar_ping", "map_city_spotlight", "map_city_label_track", "map_satellite_dolly_in", "map_satellite_tilt_in", "map_satellite_orbit_target", "map_satellite_altitude_drop", "map_satellite_pin_reveal", "map_terrain_peak_focus", "map_terrain_rise", "map_terrain_flyover", "map_terrain_depth_reveal":
		latitude, longitude := 51.5074, -0.1278
		label := "LONDON · UNITED KINGDOM"
		if id == "map_city_pin_drop" || id == "map_city_spotlight" {
			latitude, longitude, label = 48.8566, 2.3522, "PARIS · FRANCE"
		}
		if id == "map_terrain_peak_focus" || id == "map_terrain_rise" || id == "map_terrain_flyover" || id == "map_terrain_depth_reveal" {
			latitude, longitude, label = 46.8523, 9.5259, "ALPS · RELIEF STUDY"
		}
		target := mapPixel(latitude, longitude)
		marker("city_pin", label, target[0], target[1], 860, "#F3C76A")
		if id == "map_city_radar_ping" {
			for i, d := range []float64{70, 150, 250} {
				layers = append(layers, shape(fmt.Sprintf("radar_%d", i), "ellipse", []any{target[0], target[1]}, d, d, "#00000000", 600+i*220, map[string]any{"color": "#75D6CE", "width": 2}))
			}
		}
		if id == "map_city_spotlight" {
			layers = append(layers, shape("documentary_dim", "rect", []any{640.0, 360.0}, canvasWidth, canvasHeight, "#07121A80", 500, nil))
		}
		if id == "map_satellite_tilt_in" || id == "map_satellite_orbit_target" {
			// RenderPlan V3 has no primitive "line" shape; use a stroked
			// rectangle for the same restrained horizontal bearing indicator.
			layers = append(layers, shape("satellite_bearing", "rect", []any{target[0], target[1]}, 260, 2, "#00000000", 600, map[string]any{"color": "#79D6CE", "width": 2}))
		}
	case "map_route_draw", "map_route_camera_follow", "map_route_air_arc", "map_route_multi_destination", "map_route_reverse", "map_route_caption", "map_terrain_route", "map_historical_route_ink", "location_duo_route", "location_trio_route_chain", "location_penta_route_star":
		newYork, london := mapPixel(40.7128, -74.0060), mapPixel(51.5074, -0.1278)
		if id == "map_route_multi_destination" {
			marker("route_origin", "LONDON", london[0], london[1], 300, "#E9F0EA")
			for i, coord := range [][2]float64{{46.0, 2.3}, {41.9, 12.5}, {52.5, 13.4}} {
				dest := mapPixel(coord[0], coord[1])
				path(fmt.Sprintf("branch_%d", i), routeArc(london, dest, 4), 700+i*180, "#79D6CE", 3)
				marker(fmt.Sprintf("dest_%d", i), []string{"PARIS", "ROME", "BERLIN"}[i], dest[0], dest[1], 1450+i*160, "#79D6CE")
			}
		} else if id == "location_duo_route" || id == "location_trio_route_chain" {
			cities := [][2]float64{{51.5074, -0.1278}, {48.8566, 2.3522}, {41.9028, 12.4964}}
			labels := []string{"LONDON", "PARIS", "ROME"}
			count := 2
			if id == "location_trio_route_chain" {
				count = 3
			}
			for i := 0; i < count; i++ {
				point := mapPixel(cities[i][0], cities[i][1])
				marker(fmt.Sprintf("city_%d", i), labels[i], point[0], point[1], 300+i*500, []string{"#E9F0EA", "#75D6CE", "#E7BA6A"}[i])
				if i+1 < count {
					next := mapPixel(cities[i+1][0], cities[i+1][1])
					path(fmt.Sprintf("city_route_%d", i), routeArc(point, next, 4), 650+i*500, "#F3C76A", 4)
				}
			}
		} else {
			arc := routeArc(newYork, london, 6)
			path("route_main", arc, 500, "#F3C76A", 5)
			marker("origin", "NEW YORK", newYork[0], newYork[1], 300, "#E9F0EA")
			marker("destination", "LONDON", london[0], london[1], 1450, "#75D6CE")
		}
		if id == "map_route_camera_follow" {
			layers = append(layers, text("camera_follow_note", "CAMERA FOLLOW · EDITORIAL STUDY", "fade_in", 950, map[string]any{"width": 560, "height": 34, "font_size_px": 16, "position_x": 640.0, "position_y": 600.0, "color": "#79D6CE"}))
		}
		if id == "map_route_reverse" {
			reverse := routeArc(london, newYork, 6)
			path("route_return", reverse, 1650, "#79D6CE", 4)
		}
	case "map_multi_pin_stagger", "map_multi_focus_cycle", "map_cluster_expand", "map_city_comparison", "map_location_stack", "location_duo_split_map", "location_trio_pin_focus", "location_quad_focus_cycle", "location_multi_comparison", "location_multi_camera_tour", "location_quad_cluster", "location_penta_pin_stagger":
		for i, coord := range [][2]float64{{51.5074, -0.1278}, {48.8566, 2.3522}, {41.9028, 12.4964}, {52.52, 13.405}, {40.4168, -3.7038}} {
			p := mapPixel(coord[0], coord[1])
			marker(fmt.Sprintf("location_%d", i), []string{"LONDON", "PARIS", "ROME", "BERLIN", "MADRID"}[i], p[0], p[1], 300+i*210, []string{"#75D6CE", "#E7BA6A", "#A7C7E3", "#D9A7A5", "#9BC6B8"}[i])
		}
	case "map_metric_bubbles", "map_choropleth_reveal", "map_choropleth_wave":
		for i, coord := range [][2]float64{{51.5074, -0.1278}, {48.8566, 2.3522}, {41.9028, 12.4964}, {52.52, 13.405}} {
			p := mapPixel(coord[0], coord[1])
			d := float64(36 + i*18)
			layers = append(layers, shape(fmt.Sprintf("data_bubble_%d", i), "ellipse", []any{p[0], p[1]}, d, d, []string{"#79D6CE80", "#E7BA6A80", "#A7C7E380", "#D9A7A580"}[i], 380+i*180, map[string]any{"color": "#EDF2E8", "width": 2}))
		}
	case "map_historical_parchment_reveal", "map_historical_zoom":
		layers = append(layers, shape("paper_wash", "rect", []any{640.0, 360.0}, canvasWidth, canvasHeight, "#C89B5A24", 700, nil))
	}
	if family == "map_historical_v1" {
		layers = append(layers, text("historical_caveat", "EDITORIAL STUDY · MODERN GEOGRAPHY", "fade_in", 900, map[string]any{"width": 780, "height": 36, "font_size_px": 17, "position_x": 640.0, "position_y": 594.0, "color": "#F3C76A"}))
	}
	if family == "map_terrain_v1" {
		layers = append(layers, text("terrain_caveat", "SHADED RELIEF · NO DEM DATA", "fade_in", 900, map[string]any{"width": 650, "height": 32, "font_size_px": 15, "position_x": 640.0, "position_y": 606.0, "color": "#F3C76A"}))
	}
	if family == "map_data_v1" || strings.Contains(id, "choropleth") || strings.Contains(id, "metric") {
		layers = append(layers, text("illustrative_notice", "ILLUSTRATIVE · NO EMPIRICAL METRICS", "fade_in", 850, map[string]any{"width": 650, "height": 32, "font_size_px": 15, "position_x": 640.0, "position_y": 606.0, "color": "#F3C76A"}))
	}
	out := make([]map[string]any, 0, len(layers))
	for _, layer := range layers {
		out = append(out, layer)
	}
	return out
}
func mapPixel(latitude, longitude float64) [2]float64 {
	const lonMin, lonMax, latMin, latMax = -100.0, 50.0, 8.0, 72.0
	mapHeight := float64(canvasWidth * 630 / 1480)
	mapTop := float64(canvasHeight)/2 - mapHeight/2
	return [2]float64{
		(longitude - lonMin) / (lonMax - lonMin) * canvasWidth,
		mapTop + (latMax-latitude)/(latMax-latMin)*mapHeight,
	}
}

func routeArc(from, to [2]float64, points int) [][]float64 {
	if points < 2 {
		points = 2
	}
	result := make([][]float64, points)
	for i := range result {
		t := float64(i) / float64(points-1)
		arch := math.Sin(math.Pi*t) * 62
		result[i] = []float64{from[0] + (to[0]-from[0])*t, from[1] + (to[1]-from[1])*t - arch}
	}
	return result
}

func loadCanvas(source, generated string) (image.Image, error) {
	_ = generated
	f, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	im, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return resizeMapPlate(im, canvasWidth, canvasHeight), nil
}

// resizeMapPlate keeps the source raster's geographic aspect ratio and centers
// it on the 16:9 output canvas; the generated boundary layer uses the identical
// plate geometry so coastlines and overlays remain registered.
func resizeMapPlate(src image.Image, w, h int) *image.RGBA {
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	scale := min(float64(w)/float64(sw), float64(h)/float64(sh))
	nw, nh := int(float64(sw)*scale+0.5), int(float64(sh)*scale+0.5)
	offsetX, offsetY := (w-nw)/2, (h-nh)/2
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.SetRGBA(x, y, color.RGBA{R: 12, G: 22, B: 31, A: 255})
		}
	}
	for y := 0; y < nh; y++ {
		sy := bounds.Min.Y + min(sh-1, int(float64(y)/scale))
		for x := 0; x < nw; x++ {
			sx := bounds.Min.X + min(sw-1, int(float64(x)/scale))
			dst.Set(offsetX+x, offsetY+y, src.At(sx, sy))
		}
	}
	return dst
}
func makeBoundaries(geoJSON, out string) error {
	data, err := os.ReadFile(geoJSON)
	if err != nil {
		return err
	}
	var doc struct {
		Features []struct {
			Geometry struct {
				Type        string `json:"type"`
				Coordinates any    `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	img := image.NewRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))
	drawBoundaries(img, doc.Features)
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
func savePNG(im image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, im)
}
func drawBoundaries(img *image.RGBA, features []struct {
	Geometry struct {
		Type        string `json:"type"`
		Coordinates any    `json:"coordinates"`
	} `json:"geometry"`
}) {
	const lonMin, lonMax, latMin, latMax = -100.0, 50.0, 8.0, 72.0
	mapHeight := canvasWidth * 630 / 1480
	mapTop := (canvasHeight - mapHeight) / 2
	ink := color.RGBA{R: 226, G: 232, B: 218, A: 190}
	project := func(coord []any) (image.Point, bool) {
		if len(coord) < 2 {
			return image.Point{}, false
		}
		lon, lok := coord[0].(float64)
		lat, latok := coord[1].(float64)
		if !lok || !latok || lon < lonMin || lon > lonMax || lat < latMin || lat > latMax {
			return image.Point{}, false
		}
		return image.Pt(int((lon-lonMin)/(lonMax-lonMin)*canvasWidth), mapTop+int((latMax-lat)/(latMax-latMin)*float64(mapHeight))), true
	}
	var line func([]any)
	line = func(coords []any) {
		var previous image.Point
		havePrevious := false
		for _, entry := range coords {
			switch p := entry.(type) {
			case []any:
				if len(p) >= 2 {
					if _, ok := p[0].(float64); ok {
						point, valid := project(p)
						if valid && havePrevious {
							drawLine(img, previous, point, ink)
						}
						previous, havePrevious = point, valid
					} else {
						line(p)
						havePrevious = false
					}
				}
			case []float64:
				values := make([]any, len(p))
				for i, v := range p {
					values[i] = v
				}
				point, valid := project(values)
				if valid && havePrevious {
					drawLine(img, previous, point, ink)
				}
				previous, havePrevious = point, valid
			}
		}
	}
	for _, feature := range features {
		coordinates, ok := feature.Geometry.Coordinates.([]any)
		if ok {
			line(coordinates)
		}
	}
}
func drawLine(img *image.RGBA, a, b image.Point, ink color.RGBA) {
	dx, dy := int(math.Abs(float64(b.X-a.X))), int(math.Abs(float64(b.Y-a.Y)))
	sx, sy := -1, -1
	if a.X < b.X {
		sx = 1
	}
	if a.Y < b.Y {
		sy = 1
	}
	err := dx - dy
	for {
		img.SetRGBA(a.X, a.Y, ink)
		if a == b {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			a.X += sx
		}
		if e2 < dx {
			err += dx
			a.Y += sy
		}
	}
}
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
func copy(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = out.ReadFrom(in)
	return err
}
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
func writeManifest(root string, cat gallery, entries []entry) {
	doc := manifest{Schema: "chronontemplate.map-motion-gallery.v1", Version: 1, CatalogID: cat.CatalogID, RenderedAt: time.Now().UTC().Format(time.RFC3339), Entries: append([]entry(nil), entries...)}
	if err := writeJSON(filepath.Join(root, "manifest.json"), doc); err != nil {
		log.Printf("manifest write: %v", err)
	}
}
func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}
func count(entries []entry, status string) int {
	n := 0
	for _, e := range entries {
		if e.Status == status {
			n++
		}
	}
	return n
}
func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
