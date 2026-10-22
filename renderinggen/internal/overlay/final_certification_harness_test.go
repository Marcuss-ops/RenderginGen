//go:build certification

// final_certification_harness_test.go owns the final certification lane's
// harness helpers: the fixture-image item builders, the Apple style/asset
// roots, the opt-in engine probe and the pixel-comparison primitives used by
// final_apple_style_test.go and final_certification_matrix_test.go. Split per
// concern out of the certification harness.
package overlay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// certificationImageItem is one asset-driven certification item in the
// semantic contract. It reuses the single fixture image identity so a plan can
// carry several image items without tripping the registry's collision guard.
func certificationImageItem(id, presetID string, assetID string) string {
	return fmt.Sprintf(
		`{"id":%q,"template_id":"IMAGE_OVERLAY","preset_id":%q,"start_ms":%d,"end_ms":%d,`+
			`"asset_refs":[{"asset_id":%q,"sha256":%q,"url":"https://store.example/%s.jpg","media_type":"image/jpeg"}]}`,
		id, presetID, certificationStartMS, certificationEndMS, assetID, certificationAssetSHA, assetID)
}

func certificationTextItem(id, presetID, text string) string {
	return fmt.Sprintf(
		`{"id":%q,"template_id":"IMPORTANT_PHRASE","preset_id":%q,"text":%q,"start_ms":%d,"end_ms":%d}`,
		id, presetID, text, certificationStartMS, certificationEndMS)
}

func appleStylesDir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	return filepath.Join(filepath.Dir(source), "../../../testdata/styles")
}

func appleAssetsRoot(t *testing.T) string {
	t.Helper()
	source := certificationSourceRoot(t)
	root := t.TempDir()

	fixtures := map[string]string{
		"assets/apple.png":              "apple.png",
		"assets/background.jpg":         "background.jpg",
		"assets/fonts/Poppins-Bold.ttf": "Poppins-Bold.ttf",
		"fonts/Poppins-Bold.ttf":        "Poppins-Bold.ttf",
		"Poppins-Bold.ttf":              "Poppins-Bold.ttf",
		"apple.png":                     "apple.png",
	}

	for logicalPath, fixture := range fixtures {
		data, err := os.ReadFile(filepath.Join(source, fixture))
		if err != nil {
			t.Skipf("fixture %s not available: %v", fixture, err)
		}
		target := filepath.Join(root, filepath.FromSlash(logicalPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatalf("write asset %s: %v", logicalPath, err)
		}
	}
	return root
}

// goldenEngineBinOrSkip gates the Apple-style golden render: the same opt-in
// engine policy as the certification suite (CHRONON_BIN must name the binary, see
// chrononBinFor) plus -short.
//
// Why it is opt-in at all: this test is a golden GENERATOR, not a certification.
// It drives the real engine for all three styles (minutes of render work) and
// writes the resulting MP4s into the checkout under testdata/ (see outDir below),
// so it must never start as a side effect of `go test ./...`.
func goldenEngineBinOrSkip(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("short mode: the Apple-style golden render drives the real engine and writes MP4s under testdata/")
	}
	return chrononBinFor(t)
}

func probeMP4StructuralWithFPS(t *testing.T, path string, wantFPS, wantFrames int, wantMinDuration float64) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,nb_frames,r_frame_rate",
		"-show_entries", "format=duration", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var probe struct {
		Streams []struct {
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			NBFrames   string `json:"nb_frames"`
			RFrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatalf("ffprobe JSON: %v", err)
	}
	if len(probe.Streams) != 1 {
		t.Fatalf("expected 1 video stream, got %d", len(probe.Streams))
	}
	s := probe.Streams[0]
	if s.Width != 1920 || s.Height != 1080 {
		t.Errorf("resolution %dx%d, want 1920x1080", s.Width, s.Height)
	}
	wantRFR := fmt.Sprintf("%d/1", wantFPS)
	if s.RFrameRate != wantRFR {
		t.Errorf("fps %q, want %s", s.RFrameRate, wantRFR)
	}
}

// goal3Geometry is the certified clip geometry: 5 s at the YouTube contract
// rate, long enough to prove the background survives past a short plate's end.
const (
	goal3DurationS = 5
)

func pixelDifference(t *testing.T, first, second []byte) int {
	t.Helper()
	a, err := png.Decode(bytes.NewReader(first))
	if err != nil {
		t.Fatalf("decode first certification frame: %v", err)
	}
	b, err := png.Decode(bytes.NewReader(second))
	if err != nil {
		t.Fatalf("decode second certification frame: %v", err)
	}
	if a.Bounds() != b.Bounds() {
		t.Fatalf("certification frame dimensions differ: %v vs %v", a.Bounds(), b.Bounds())
	}
	different := 0
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			if a.At(x, y) != b.At(x, y) {
				different++
			}
		}
	}
	return different
}

func assertPixelDifference(t *testing.T, first, second []byte, minDifferent int) {
	t.Helper()
	different := pixelDifference(t, first, second)
	if different < minDifferent {
		t.Errorf("Apple style frames differ in only %d pixels, want at least %d", different, minDifferent)
	}
}

// renderCertificationPlan compiles the fixture for one preset, writes the
// plan, renders it with Chronon software backend, and returns the MP4 path.
// outName is the output base name so the same preset can be rendered several
// times within one run.
func renderCertificationPlan(t *testing.T, bin, assetsRoot, outDir, presetID, outName string) string {
	t.Helper()
	plan := certificationPlan(t, presetID)
	videoPath := filepath.Join(outDir, outName+"_24fps_1080p.mp4")
	plan.Output.Path = videoPath
	planPath := filepath.Join(outDir, outName+"_plan.json")
	planBytes, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin,
		"render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "software", "--encoder-backend", "pipe",
		"--hardware", "none", "--gpu-hot-path-mode", "auto",
		"--encode-preset", "ultrafast",
		"-o", videoPath)
	cmd.Dir = assetsRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		// Chronon must never "succeed" into a missing file, and a render
		// failure must reach the test as a failure, not a skip.
		t.Fatalf("chronon render %s: %v\n%s", presetID, err, tailBytes(out))
	}
	if _, err := os.Stat(videoPath); err != nil {
		t.Fatalf("render reported success but output is missing: %v", err)
	}
	return videoPath
}

// assertBackgroundPreserved checks the corners of every sampled frame that
// fall outside the entity's declared coverage: the Pale Olive background must
// survive untouched there. A dark corner is the black-background regression
// signature.
func assertBackgroundPreserved(t *testing.T, plan *Plan, path string, frames []int) {
	t.Helper()
	corners := [][2]int{{50, 50}, {1869, 50}, {50, 1029}, {1869, 1029}}
	coverage := entityCoverage(plan)
	for _, frame := range frames {
		checked := 0
		for _, c := range corners {
			if cornerInsideEntity(c, coverage) {
				continue
			}
			checked++
			luma := sampleLuma(t, path, frame, c[0], c[1])
			if luma < 180 {
				t.Errorf("frame %d corner (%d,%d) luma=%.1f: background replaced (black-frame regression?)",
					frame, c[0], c[1], luma)
			}
		}
		if checked == 0 {
			t.Errorf("frame %d: every corner is inside the entity coverage; background preservation is unverifiable", frame)
		}
	}
}

// cornerInsideEntity reports whether the 2x2 crop sampled at corner c is fully
// inside the entity's coverage region. A flush bottom/right card legitimately
// owns its corner of the canvas, so those corners must be excluded from the
// background-preservation check (the black regression is caught by the
// corners the entity does NOT cover).
func cornerInsideEntity(c [2]int, region [4]float64) bool {
	if region[2] <= region[0] || region[3] <= region[1] {
		return false
	}
	return float64(c[0]) >= region[0] && float64(c[0])+1 <= region[2] &&
		float64(c[1]) >= region[1] && float64(c[1])+1 <= region[3]
}

// entityCoverage returns the canvas region the compiled entity layer occupies
// at its resting placement, so background sampling never reads a pixel that
// belongs to the entity itself. The engine reads the position field per layer
// type: a text layer already carries the layer centre in absolute canvas
// coordinates (the engine subtracts canvas/2 itself), while an image layer
// carries an offset from the canvas centre. The background color layer is
// ignored.
func entityCoverage(plan *Plan) [4]float64 {
	for _, layer := range plan.Layers {
		if (layer.Type != "image" && layer.Type != "text") || len(layer.Position) < 2 || len(layer.Size) < 2 {
			continue
		}
		cx := layer.Position[0]
		cy := layer.Position[1]
		if layer.Type != "text" {
			cx += float64(plan.Canvas.Width) / 2
			cy += float64(plan.Canvas.Height) / 2
		}
		return [4]float64{cx - layer.Size[0]/2, cy - layer.Size[1]/2, cx + layer.Size[0]/2, cy + layer.Size[1]/2}
	}
	return [4]float64{}
}

// sampleLuma returns the luma (YAVG 0-255) of one pixel of one frame.
func sampleLuma(t *testing.T, path string, frame, x, y int) float64 {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-v", "info",
		"-i", path,
		"-vf", fmt.Sprintf("select=eq(n\\,%d),crop=2:2:%d:%d,signalstats,metadata=print:key=lavfi.signalstats.YAVG", frame, x, y),
		"-frames:v", "1", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("sample pixel frame %d (%d,%d): %v\n%s", frame, x, y, err, tailBytes(out))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if idx := strings.Index(line, "YAVG="); idx >= 0 {
			v, err := strconv.ParseFloat(strings.TrimSpace(line[idx+5:]), 64)
			if err != nil {
				t.Fatalf("parse YAVG from %q", line)
			}
			return v
		}
	}
	t.Fatalf("no YAVG in ffmpeg output: %s", tailBytes(out))
	return 0
}

func assertRenderFramesClose(t *testing.T, firstPath, secondPath string) {
	t.Helper()
	decode := func(path string) image.Image {
		file, err := os.Open(path)
		if err != nil {
			t.Fatalf("open rendered frame %s: %v", path, err)
		}
		defer file.Close()
		frame, _, err := image.Decode(file)
		if err != nil {
			t.Fatalf("decode rendered frame %s: %v", path, err)
		}
		return frame
	}
	first, second := decode(firstPath), decode(secondPath)
	if first.Bounds() != second.Bounds() {
		t.Fatalf("rendered frame bounds differ: %v vs %v", first.Bounds(), second.Bounds())
	}
	maxChannelError := make([]int, 0, first.Bounds().Dx()*first.Bounds().Dy())
	total := 0.0
	for y := first.Bounds().Min.Y; y < first.Bounds().Max.Y; y++ {
		for x := first.Bounds().Min.X; x < first.Bounds().Max.X; x++ {
			ar, ag, ab, _ := first.At(x, y).RGBA()
			br, bg, bb, _ := second.At(x, y).RGBA()
			delta := max(channelError(ar, br), channelError(ag, bg), channelError(ab, bb))
			maxChannelError = append(maxChannelError, delta)
			total += float64(delta)
		}
	}
	sort.Ints(maxChannelError)
	p99 := maxChannelError[(len(maxChannelError)*99-1)/100]
	mean := total / float64(len(maxChannelError))
	t.Logf("software/Vulkan pixel parity: mean max-channel error %.2f/255, p99 %d/255", mean, p99)
	if mean > 8 || p99 > 48 {
		t.Fatalf("software/Vulkan parity exceeded tolerance: mean %.2f/255 (max 8), p99 %d/255 (max 48)", mean, p99)
	}
}
func channelError(a, b uint32) int {
	delta := int(a>>8) - int(b>>8)
	if delta < 0 {
		return -delta
	}
	return delta
}

// Helper to compile a single-item plan
func compileSingleShape(t *testing.T, params map[string]any) (CompileResult, Layer, error) {
	t.Helper()
	planMap := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "test-shape-plan",
		"video_id":       "test-vid",
		"width":          1920,
		"height":         1080,
		"fps_num":        30,
		"fps_den":        1,
		"duration_ms":    5000,
		"items": []any{
			map[string]any{
				"id":          "test-shape-item",
				"kind":        "shape",
				"template_id": "shape_static",
				"start_ms":    0,
				"end_ms":      5000,
				"params":      params,
			},
		},
	}
	raw, err := json.Marshal(planMap)
	if err != nil {
		return CompileResult{}, Layer{}, fmt.Errorf("marshal plan: %w", err)
	}
	res, err := CompileSemantic(raw)
	if err != nil {
		return CompileResult{}, Layer{}, err
	}
	if len(res.Plan.Layers) == 0 {
		return res, Layer{}, fmt.Errorf("no layers produced")
	}
	return res, res.Plan.Layers[0], nil
}
func extractVideoFramePNG(t *testing.T, path string, frame int) []byte {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path,
		"-vf", fmt.Sprintf("select=eq(n\\,%d)", frame), "-frames:v", "1",
		"-f", "image2pipe", "-vcodec", "png", "pipe:1")
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("extract encoded frame %d from %s: %v", frame, path, err)
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("decode encoded frame %d from %s: %v", frame, path, err)
	}
	return data
}
func findChrononBin(t *testing.T) (string, string) {
	t.Helper()
	chrononBin := os.Getenv("CHRONON_BIN")
	assetsRoot := ""
	if wsRoot, ok := findWorkspaceRoot("."); ok {
		assetsRoot = filepath.Join(wsRoot, "Chronon3d")
		if chrononBin == "" {
			candidate := filepath.Join(wsRoot, "Chronon3d", "build", "chronon", "linux-video-release", "apps", "chronon3d_cli", "chronon3d_cli")
			if _, err := os.Stat(candidate); err == nil {
				chrononBin = candidate
			}
		}
	}
	if chrononBin == "" {
		cwd, _ := os.Getwd()
		candidateDirs := []string{
			"../../Chronon3d",
			"../../../Chronon3d",
			"../Chronon3d",
		}
		dir := cwd
		for i := 0; i < 5; i++ {
			c := filepath.Join(dir, "Chronon3d")
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				candidateDirs = append(candidateDirs, c)
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		for _, candidateDir := range candidateDirs {
			candidate := filepath.Join(candidateDir, "build", "chronon", "linux-video-release", "apps", "chronon3d_cli", "chronon3d_cli")
			if _, err := os.Stat(candidate); err == nil {
				chrononBin = candidate
				assetsRoot = candidateDir
				break
			}
		}
	}
	return chrononBin, assetsRoot
}
func resolveOutputFrame(target string) string {
	if info, err := os.Stat(target); err == nil && info.Size() > 0 {
		return target
	}
	ext := filepath.Ext(target)
	base := strings.TrimSuffix(target, ext)
	seqName := fmt.Sprintf("%s_0000%s", base, ext)
	if info, err := os.Stat(seqName); err == nil && info.Size() > 0 {
		return seqName
	}
	dir := filepath.Dir(target)
	out0 := filepath.Join(dir, "out_0000.png")
	if info, err := os.Stat(out0); err == nil && info.Size() > 0 {
		return out0
	}
	return target
}
