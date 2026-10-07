// certification_harness_test.go is the ALWAYS-COMPILED half of the
// certification suites: every runtime/GPU harness helper that the untagged
// overlay tests also use lives here, so the //go:build certification files can
// hold only their Test functions without hiding this code from the default loop.
//
// Keep new shared helpers here: a helper added to a tagged file is invisible to
// the untagged tests, and the build tag makes that failure a compile error rather
// than a silent coverage gap.

package overlay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func tailBytes(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 5000 {
		s = s[len(s)-5000:]
	}
	return s
}

// skipRuntimeCertificationEnv is the documented opt-out for the real-engine
// certification suite (named in the package comment above). Any non-empty
// value disables it; the value is not interpreted, so a stale "0" cannot
// silently re-enable GPU work in a context that asked not to do any.
const skipRuntimeCertificationEnv = "RENDERINGGEN_SKIP_GPU_E2E"

// engineBinEnv names the native engine binary for the real-engine suites. It is
// an ENV VAR on purpose: the path is machine-specific and must never be baked
// into the tests.
const engineBinEnv = "CHRONON_BIN"

// chrononBinFor returns the real binary or skips the calling test. The binary is
// never pinned to a machine-specific path and never DISCOVERED: the caller must
// name it with CHRONON_BIN.
//
// Why opt-in rather than opt-out: these tests drive a real engine render —
// minutes of GPU work, video written under the checkout, and a subprocess whose
// children can inherit the output pipe, so Cmd.Wait can outlive its own context
// deadline. When a build beside this repository was enough to enable all of that,
// a bare `go test ./...` on a development machine silently became a long render —
// and on a host without a usable GPU it did not even fail, it hung (observed:
// TestFinal_AllOfficialPresetsRender and the Apple-style golden render each held a
// package run past its timeout). Discovery is not consent, so it is no longer
// accepted; `CHRONON_BIN=/path/to/chronon3d_cli go test ./internal/overlay/` runs
// the suite, everything else skips it.
//
// The opt-out is still checked FIRST: CI and `make test-unit` set it, and that
// must keep working as belt and braces.
func chrononBinFor(t *testing.T) string {
	t.Helper()
	if strings.TrimSpace(os.Getenv(skipRuntimeCertificationEnv)) != "" {
		t.Skipf("%s is set: the real-engine runtime certification suite is disabled for this run", skipRuntimeCertificationEnv)
	}
	bin := strings.TrimSpace(os.Getenv(engineBinEnv))
	if bin == "" {
		t.Skipf("the real-engine suites are opt-in: set %s=/path/to/chronon3d_cli to run them (a build discovered beside this repository is no longer accepted, because it made a bare `go test ./...` render video)", engineBinEnv)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("%s=%s is not available: %v", engineBinEnv, bin, err)
	}
	return bin
}

// certificationSourceRoot is the checked-in fixture directory the runtime
// harness copies from (RENDERINGGEN_ASSETS_ROOT overrides it).
func certificationSourceRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("RENDERINGGEN_ASSETS_ROOT"); root != "" {
		if _, err := os.Stat(root); err != nil {
			t.Skipf("assets root not available (%s): %v", root, err)
		}
		return root
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate test source")
	}
	root := filepath.Join(filepath.Dir(source), "../../../testdata/golden")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("assets root not available (%s): %v", root, err)
	}
	return root
}

// certificationAssetsRoot materializes the fixture assets in the exact
// workspace layout the semantic plan references — assets/semantic/<id>.jpg
// for the image and the catalog font paths — and returns that root. Chronon is
// invoked with it, so a plan compiled by certificationPlan resolves every
// asset without running the worker's materialize stage.
func certificationAssetsRoot(t *testing.T) string {
	t.Helper()
	source := certificationSourceRoot(t)
	fixtures := map[string]string{
		"assets/semantic/" + certificationAssetID + ".jpg": "gerard_butler.jpg",
		officialFontPath: "Poppins-Bold.ttf",
	}
	root := t.TempDir()
	for logicalPath, fixture := range fixtures {
		data, err := os.ReadFile(filepath.Join(source, fixture))
		if err != nil {
			t.Skipf("fixture %s not available under %s: %v", fixture, source, err)
		}
		target := filepath.Join(root, filepath.FromSlash(logicalPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("create assets root: %v", err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatalf("materialize %s: %v", logicalPath, err)
		}
	}
	return root
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

// probeMP4Structural asserts the container contract on a rendered MP4.
func probeMP4Structural(t *testing.T, path string) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,nb_frames,r_frame_rate",
		"-show_entries", "format=duration", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
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
		t.Fatalf("expected exactly one video stream, got %d", len(probe.Streams))
	}
	s := probe.Streams[0]
	if s.Width != 1920 || s.Height != 1080 {
		t.Errorf("resolution %dx%d, want 1920x1080", s.Width, s.Height)
	}
	frames, err := strconv.ParseInt(s.NBFrames, 10, 64)
	if err != nil || frames < certificationDurationFrames {
		t.Errorf("frame count %q, want >= %d (exclusive-end contract)", s.NBFrames, certificationDurationFrames)
	}
	// The timebase can yield a non-trivial avg_frame_rate (e.g. 750/31) even
	// on valid output; the container frame rate contract is r_frame_rate.
	if s.RFrameRate != "24/1" {
		t.Errorf("fps %q, want 24/1", s.RFrameRate)
	}
	dur, err := strconv.ParseFloat(strings.TrimSpace(probe.Format.Duration), 64)
	if err != nil || dur < 5.0 {
		t.Errorf("duration %q, want >= 5.0s", probe.Format.Duration)
	}
}

// decodeFully asserts the whole bitstream decodes; ffprobe alone tolerates
// NAL corruption and truncated files that ffmpeg's decode does not.
func decodeFully(t *testing.T, path string) {
	t.Helper()
	if out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "null", "-").CombinedOutput(); err != nil {
		t.Fatalf("full decode failed: %v\n%s", err, tailBytes(out))
	}
}

// extractFramePNG decodes one frame of path into a PNG and returns its bytes.
func extractFramePNG(t *testing.T, path string, frame int) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), fmt.Sprintf("frame_%04d.png", frame))
	if b, err := exec.Command("ffmpeg", "-v", "error",
		"-i", path, "-vf", fmt.Sprintf("select=eq(n\\,%d)", frame),
		"-frames:v", "1", "-y", out).CombinedOutput(); err != nil {
		t.Fatalf("extract frame %d: %v\n%s", frame, err, tailBytes(b))
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read frame %d: %v", frame, err)
	}
	if len(data) < 1024 {
		t.Fatalf("frame %d suspiciously small (%d bytes)", frame, len(data))
	}
	return data
}

func assertPixelDifference(t *testing.T, first, second []byte, minDifferent int) {
	t.Helper()
	different := pixelDifference(t, first, second)
	if different < minDifferent {
		t.Errorf("Apple style frames differ in only %d pixels, want at least %d", different, minDifferent)
	}
}

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

// certificationDurationFrames is the certified composition length. The
// fixture starts at frame 10, so the entity's own window is 10..124 inclusive
// (exclusive end 125).
const certificationDurationFrames = int64(125)

// certificationMSRange is the millisecond range that lowers to the certified
// frame window at the fixture's 24 fps: nearest(start_ms·24/1000)=10 and
// nearest(end_ms·24/1000)=125.
const (
	certificationStartMS = int64(417)
	certificationEndMS   = int64(5208)
)

// certificationBackgroundRGBA is the Pale Olive Classic background, the color
// layer contract that keeps a compositor backend from rendering branded
// content as black.
const certificationBackgroundRGBA = "[0.9333333333333333,0.9450980392156862,0.9058823529411765,1]"

// certificationAssetID/SHA identify the single fixture image every
// certification plan references. At runtime the harness materializes the real
// bytes at the semantic logical path (assets/semantic/<id>.jpg).
const (
	certificationAssetID  = "certification-image"
	certificationAssetSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

const certificationText = "Pipeline Certificata — 450% più veloce, fino a 125 frame"

// certificationItemJSON builds one real semantic item for a registry preset,
// using the same template vocabulary PipelineGen emits: IMAGE_OVERLAY for the
// image family, IMPORTANT_PHRASE for the text family.
func certificationItemJSON(def PresetDefinition) (string, error) {
	if def.Family == PresetImage {
		return fmt.Sprintf(
			`{"id":%q,"template_id":"IMAGE_OVERLAY","preset_id":%q,"start_ms":%d,"end_ms":%d,`+
				`"asset_refs":[{"asset_id":%q,"sha256":%q,"url":"https://store.example/certification.jpg","media_type":"image/jpeg"}]}`,
			def.ID, def.ID, certificationStartMS, certificationEndMS, certificationAssetID, certificationAssetSHA), nil
	}
	return fmt.Sprintf(
		`{"id":%q,"template_id":"IMPORTANT_PHRASE","preset_id":%q,"text":%q,"start_ms":%d,"end_ms":%d}`,
		def.ID, def.ID, certificationText, certificationStartMS, certificationEndMS), nil
}

// certificationPlanRaw is the renderinggen.overlay-plan.v1 document the whole
// certification suite (compile and runtime) lowers and renders.
func certificationPlanRaw(def PresetDefinition) (string, error) {
	item, err := certificationItemJSON(def)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":%q,"video_id":"certification","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
			`"background":{"kind":"color","color":%s},"items":[%s]}`,
		"certification-"+def.ID, certificationBackgroundRGBA, item), nil
}

// certificationPlan compiles the fixture for one preset through the single
// production compiler and returns the exact chronon.render-plan.v2 the
// runtime certification renders. It is deterministic, so pixel tests can read
// the entity's declared geometry from the same plan that produced the MP4
// instead of hard-coding sample points.
func certificationPlan(t *testing.T, presetID string) *Plan {
	t.Helper()
	def, err := ResolveOfficialPreset(presetID)
	if err != nil {
		t.Fatalf("resolve registry preset: %v", err)
	}
	raw, err := certificationPlanRaw(def)
	if err != nil {
		t.Fatalf("build certification plan: %v", err)
	}
	// CompileSemantic is the single compile entry point: a successful return
	// IS the proof that the plan went through the semantic lowering (there is
	// no "compiled but not semantic" outcome any more).
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("compile certification plan %s: %v", presetID, err)
	}
	return result.Plan
}

func certificationEntityLayer(plan *Plan, def PresetDefinition) *Layer {
	for i := range plan.Layers {
		layer := &plan.Layers[i]
		if def.Family == PresetImage && layer.Type == "image" {
			return layer
		}
		if def.Family == PresetText && layer.Type == "text" {
			return layer
		}
	}
	return nil
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
	goal3Width     = 1920
	goal3Height    = 1080
	goal3FPS       = 24
	goal3DurationS = 5
	goal3Frames    = goal3FPS * goal3DurationS
)

// goal3PlateHex is the image plate's colour (rgb 30/90/168), chosen far from
// black so a missing background layer is unmistakable in a pixel probe.
const goal3PlateHex = "0x1E5AA8"

// goal3VideoPlateHex is the VIDEO plate's colour (rgb 168/60/30): distinct from
// the image plate so the two background families can never be confused in a
// probe. The plate is a solid colour on purpose — a moving plate cannot prove
// the background is still on screen after its own last frame, while a solid
// plate makes "held/looped to the end of the clip" a hard pixel assertion.
const goal3VideoPlateHex = "0xA83C1E"

// goal3PlateRGB is the same colour as the probe expects it back from the
// render: 8-bit channels with a tolerance that absorbs the RGB→YUV420→RGB
// round trip the encoder performs.
const (
	goal3PlateR = 30
	goal3PlateG = 90
	goal3PlateB = 168
	goal3Tol    = 30
)

// repoRootAt walks up from this source file until a directory containing BOTH
// module roots is found, so the harness never hardcodes a machine path.
func repoRootAt(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate test source to resolve the repo root")
	}
	dir := filepath.Dir(source)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "refactored", "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "RenderingGen", "renderinggen", "go.mod")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("cannot locate the repository root from the test source")
	return ""
}

// goal3OutDir is the stable output directory the rendered clips land in.
// goal3AssetsRoot is the fixture root. It is a per-run temporary directory by
// default; GOAL3_ASSETS_DIR pins it so the CLI can be re-run by hand against
// the exact assets a recorded plan references (post-hoc graph diagnosis).
func goal3AssetsRoot(t *testing.T) string {
	t.Helper()
	if dir := strings.TrimSpace(os.Getenv("GOAL3_ASSETS_DIR")); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create assets directory %s: %v", dir, err)
		}
		return dir
	}
	return t.TempDir()
}

// goal3DiagnosticsArgs appends the engine's diagnostic switches when GOAL3_DIAG
// is set: the per-node trace plus the graph preflight report. Together they are
// the only way to see WHICH surface an encoded frame actually published, and
// they need no rebuild to turn on.
func goal3DiagnosticsArgs(args []string) []string {
	if strings.TrimSpace(os.Getenv("GOAL3_DIAG")) == "" {
		return args
	}
	return append(args, "--diagnostic", "--diagnostic-plan")
}

// goal3CLIEnv is the environment for a render invocation. With GOAL3_DIAG set
// it also enables the engine's native-surface promotion trace (composite
// handles, terminal handoff, residency decisions), which is env-gated inside
// the engine.
func goal3CLIEnv() []string {
	env := os.Environ()
	if strings.TrimSpace(os.Getenv("GOAL3_DIAG")) != "" {
		env = append(env, "CHRONON3D_NATIVE_SURFACE_PROMOTION_DIAG=1")
	}
	return env
}

// goal3WriteCLILog keeps the full CLI output next to the artefacts. Diagnosis
// of a wrong frame must not depend on the process that produced it being alive:
// the trace travels with the render.
func goal3WriteCLILog(t *testing.T, outDir, name string, out []byte) {
	t.Helper()
	if len(out) == 0 {
		return
	}
	if err := os.WriteFile(filepath.Join(outDir, name+".log"), out, 0o644); err != nil {
		t.Logf("write CLI log for %s: %v", name, err)
	}
}

func goal3OutDir(t *testing.T) string {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("GOAL3_RENDER_OUT_DIR"))
	if dir == "" {
		dir = filepath.Join(repoRootAt(t), "refactored", "ops", "benchmarks", "goal3-e2e-"+time.Now().Format("20060102"))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create output directory %s: %v", dir, err)
	}
	return dir
}

// goal3SHA256 returns the real content address of a fixture, so the plan
// carries the hash of the bytes actually on disk (never a placeholder the
// materializer would reject).
func goal3SHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// goal3RunFFmpeg generates one deterministic fixture.
func goal3RunFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", append([]string{"-v", "error", "-y"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg unavailable or fixture generation failed: %v\n%s", err, tailBytes(out))
	}
}

// goal3Fixture is one materialized input of the rendered composition.
type goal3Fixture struct {
	assetID     string
	logicalPath string
	sha256      string
	kind        string // image | video
	mediaType   string
}

// goal3Fixtures builds the deterministic inputs under a workspace that mirrors
// the plan's semantic asset layout, and returns them addressed by the exact
// logical paths the plan references.
//
//	foreground — 1920x1080 testsrc2 + 440 Hz tone (5 s): the main content
//	image      — 800x800 rule plate (a NON-canvas square: cover must crop it)
//	video      — 1920x1080 solid round-rust plate, only 2 s, shorter than the
//	             clip: the background must be held/looped to the last frame
//	videoSmall — 1280x720 plate (NOT the canvas geometry): the boundary case
//	             the strict lane must refuse rather than silently drop
func goal3Fixtures(t *testing.T, assetsRoot string) map[string]goal3Fixture {
	t.Helper()
	mk := func(assetID, logicalPath string) string {
		target := filepath.Join(assetsRoot, filepath.FromSlash(logicalPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("create asset dir: %v", err)
		}
		return target
	}

	foregroundPath := mk("goal3-foreground", "assets/semantic/goal3-foreground/source.mp4")
	goal3RunFFmpeg(t,
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=%d:duration=%d", goal3Width, goal3Height, goal3FPS, goal3DurationS),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:sample_rate=48000:duration=%d", goal3DurationS),
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-shortest", foregroundPath)

	imagePath := mk("goal3-bg-image", "assets/semantic/goal3-bg-image/background.png")
	goal3RunFFmpeg(t,
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=%s:size=800x800:duration=1", goal3PlateHex),
		"-frames:v", "1", imagePath)

	videoPath := mk("goal3-bg-video", "assets/semantic/goal3-bg-video/background.mp4")
	goal3RunFFmpeg(t,
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=%s:size=%dx%d:rate=%d:duration=2", goal3VideoPlateHex, goal3Width, goal3Height, goal3FPS),
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-pix_fmt", "yuv420p", videoPath)

	smallPath := mk("goal3-bg-video-small", "assets/semantic/goal3-bg-video-small/background.mp4")
	goal3RunFFmpeg(t,
		"-f", "lavfi", "-i", fmt.Sprintf("color=c=0x781E8C:size=1280x720:rate=%d:duration=2", goal3FPS),
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-pix_fmt", "yuv420p", smallPath)

	return map[string]goal3Fixture{
		"foreground": {assetID: "goal3-foreground", logicalPath: "assets/semantic/goal3-foreground/source.mp4", sha256: goal3SHA256(t, foregroundPath)},
		"image":      {assetID: "goal3-bg-image", logicalPath: "assets/semantic/goal3-bg-image/background.png", sha256: goal3SHA256(t, imagePath), kind: "image", mediaType: "image/png"},
		"video":      {assetID: "goal3-bg-video", logicalPath: "assets/semantic/goal3-bg-video/background.mp4", sha256: goal3SHA256(t, videoPath), kind: "video", mediaType: "video/mp4"},
		"videoSmall": {assetID: "goal3-bg-video-small", logicalPath: "assets/semantic/goal3-bg-video-small/background.mp4", sha256: goal3SHA256(t, smallPath), kind: "video", mediaType: "video/mp4"},
	}
}

// goal3PlanRaw is the renderinggen.overlay-plan.v1 document for one case.
// background is the literal JSON block (or empty for none); watermark switches
// the text overlay; scale is the foreground scale percent.
func goal3PlanRaw(planID string, fg goal3Fixture, background, watermark string, scale int) string {
	backgroundBlock := ""
	if background != "" {
		backgroundBlock = `"background":` + background + `,`
	}
	scaleBlock := ""
	if scale > 0 && scale < 100 {
		scaleBlock = fmt.Sprintf(`"foreground_scale_percent":%d,`, scale)
	}
	watermarkBlock := ""
	if watermark != "" {
		watermarkBlock = `"watermark":` + watermark + `,`
	}
	return fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":%q,"video_id":%q,`+
			`"width":%d,"height":%d,"fps_num":%d,"fps_den":1,"duration_ms":%d,`+
			`"source":{"asset_id":%q,"sha256":%q,"path":%q},%s%s%s"items":[]}`,
		planID, fg.assetID, goal3Width, goal3Height, goal3FPS, goal3DurationS*1000,
		fg.assetID, fg.sha256, fg.logicalPath, backgroundBlock, scaleBlock, watermarkBlock)
}

// goal3BackgroundBlock renders the background block for a fixture family. The
// block is exactly what PipelineGen's mapper emits: the sealed kind, the
// resolved fit, and — for a video plate — the loop flag that keeps the
// background alive past the plate's own last frame.
func goal3BackgroundBlock(f goal3Fixture) string {
	loop := ""
	if f.kind == "video" {
		loop = `,"loop":true`
	}
	return fmt.Sprintf(
		`{"kind":%q,"fit":"cover"%s,"asset_refs":[{"asset_id":%q,"sha256":%q,"url":%q,"media_type":%q}]}`,
		f.kind, loop, f.assetID, f.sha256, f.logicalPath, f.mediaType)
}

// absInt is the tolerance probe's distance helper.
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// goal3WatermarkBlock is a static text watermark: fixed text, fixed position,
// no transition, spanning the whole clip — the shape the renderer's static
// analysis can prepare once.
func goal3WatermarkBlock() string {
	fontSHA := strings.Repeat("7", 64)
	return fmt.Sprintf(
		`{"text":"PipelineGen","position":"top_right","opacity":0.9,"margin_px":64,`+
			`"font_ref":{"asset_id":"goal3-font","sha256":%q,"url":%q,"media_type":"font/ttf"},`+
			`"style":{"font":"Montserrat","font_size_px":48,"color":"#FFFFFF","stroke":{"color":"#000000","width":3}}}`,
		fontSHA, "assets/semantic/goal3-font/Poppins-Bold.ttf")
}

// goal3RenderOnce compiles the semantic plan, writes it next to the artefact,
// and runs it through the strict GPU-native lane with exactly the flags the
// production worker uses. It returns the artefact path, the compiled plan, the
// combined CLI output and the process error (nil on success).
func goal3RenderOnce(t *testing.T, bin, assetsRoot, outDir, name, raw string) (string, *Plan, []byte, error) {
	t.Helper()
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("%s: compile: %v", name, err)
	}
	plan := result.Plan
	videoPath := filepath.Join(outDir, name+".mp4")
	plan.Output.Path = videoPath

	planPath := filepath.Join(outDir, name+"_plan.json")
	planBytes, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatalf("%s: marshal plan: %v", name, err)
	}
	if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
		t.Fatalf("%s: write plan: %v", name, err)
	}
	preparedPath := filepath.Join(outDir, name+"_prepared.json")
	preparedBytes, err := json.MarshalIndent(result.Prepared, "", "  ")
	if err != nil {
		t.Fatalf("%s: marshal prepared package: %v", name, err)
	}
	if err := os.WriteFile(preparedPath, preparedBytes, 0o644); err != nil {
		t.Fatalf("%s: write prepared package: %v", name, err)
	}

	audioSource := filepath.Join(assetsRoot, filepath.FromSlash("assets/semantic/goal3-foreground/source.mp4"))
	args := []string{
		"render", "--plan", planPath, "--assets-root", assetsRoot,
		"--prepared-package", preparedPath,
		"--backend", "vulkan",
		"--hardware", "nvenc", "--encoder-backend", "native",
		"--gpu-hot-path-mode", "require_gpu_native",
		"--encode-preset", "p1",
		// NVENC's driver-default rate control is explicitly not reproducible.
		// Goal3 compares pixels across processes, so pin the encoder contract
		// instead of letting the driver choose a session-dependent mode.
		"--rate-control", "qp", "--qp", "23",
		// The source audio is muxed by the native A/V path (the production
		// worker passes exactly this flag), so A/V sync is real, not implied.
		"--gop-source", audioSource,
		"--report",
		"-o", videoPath,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, goal3DiagnosticsArgs(args)...)
	cmd.Dir = assetsRoot
	cmd.Env = goal3CLIEnv()
	started := time.Now()
	out, err := cmd.CombinedOutput()
	wall := time.Since(started)
	goal3WriteCLILog(t, outDir, name, out)
	t.Logf("%s: wall=%s err=%v -> %s", name, wall.Round(time.Millisecond), err, videoPath)
	return videoPath, plan, out, err
}

// goal3Render runs a composition that must render, and fails the test if the
// strict lane refused it (a refusal here is a real regression: the shape is
// certified on this build).
func goal3Render(t *testing.T, bin, assetsRoot, outDir, name, raw string) (string, *Plan) {
	t.Helper()
	videoPath, plan, out, err := goal3RenderOnce(t, bin, assetsRoot, outDir, name, raw)
	if err != nil {
		t.Fatalf("%s: chronon render (GPU native): %v\n%s", name, err, tailBytes(out))
	}
	if _, err := os.Stat(videoPath); err != nil {
		t.Fatalf("%s: render reported success but the output is missing: %v", name, err)
	}
	return videoPath, plan
}

// goal3NativeSoftwareParityTolerance is the per-channel mean absolute error the
// GPU-native lane is allowed to show against the CPU reference lane for the
// SAME compiled plan. It absorbs the two different encoders (NVENC vs the pipe
// encoder) and their resampling, and nothing else: a wrong placement, a missing
// layer, a dropped overlay or a leaked transfer function all move a large part
// of the frame by tens of levels and blow past it (the two defects this harness
// found measured 92 and 110 before the fix).
const goal3NativeSoftwareParityTolerance = 12.0

// goal3AssertNativeMatchesReference is the pixel oracle: the strict GPU lane
// must agree with the CPU reference lane for the same plan. It is the only
// end-to-end reference available for "the final output is correct", and it is
// what makes the Goal 3 acceptance criterion machine-checkable.
func goal3AssertNativeMatchesReference(t *testing.T, native, reference image.Image, name string) {
	t.Helper()
	r, g, b := goal3MeanAbsDiff(t, native, reference)
	if r > goal3NativeSoftwareParityTolerance || g > goal3NativeSoftwareParityTolerance || b > goal3NativeSoftwareParityTolerance {
		t.Errorf("%s: native vs reference mean |delta| = (%.2f,%.2f,%.2f), want <= %.1f on every channel",
			name, r, g, b, goal3NativeSoftwareParityTolerance)
		return
	}
	t.Logf("%s: native vs reference mean |delta| = (%.2f,%.2f,%.2f)", name, r, g, b)
}

// goal3RenderRefused runs a composition the strict lane must REFUSE, and
// returns the CLI output. The contract it enforces is "fail closed or be
// faithful": a plate whose geometry the GPU-native compositor cannot honour
// must never be silently dropped, scaled by accident, or replaced by a black
// canvas — the job must fail with a residency error instead.
func goal3RenderRefused(t *testing.T, bin, assetsRoot, outDir, name, raw string) string {
	t.Helper()
	videoPath, _, out, err := goal3RenderOnce(t, bin, assetsRoot, outDir, name, raw)
	if err == nil {
		t.Fatalf("%s: the strict GPU-native lane accepted a non-canvas video plate (artefact %s); "+
			"if the engine gained GPU-side video box fitting this expectation must be replaced by a pixel "+
			"probe, not deleted\n%s", name, videoPath, tailBytes(out))
	}
	return string(out)
}

// goal3RenderSoftware renders the SAME compiled plan on the CPU reference lane
// (software raster + pipe encoder) and returns the artefact path. Nothing about
// the plan changes: only the backend does, which is what makes the comparison a
// parity check rather than a second opinion.
func goal3RenderSoftware(t *testing.T, bin, assetsRoot, outDir, name string, plan *Plan) string {
	t.Helper()
	reference := *plan
	reference.Output.Path = filepath.Join(outDir, name+"_software.mp4")
	planPath := filepath.Join(outDir, name+"_software_plan.json")
	data, err := json.MarshalIndent(&reference, "", "  ")
	if err != nil {
		t.Fatalf("%s: marshal reference plan: %v", name, err)
	}
	if err := os.WriteFile(planPath, data, 0o644); err != nil {
		t.Fatalf("%s: write reference plan: %v", name, err)
	}
	audioSource := filepath.Join(assetsRoot, filepath.FromSlash("assets/semantic/goal3-foreground/source.mp4"))
	args := []string{
		"render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "software", "--encoder-backend", "pipe", "--hardware", "none",
		"--gpu-hot-path-mode", "auto",
		// The software pipe resolver does not support constant-QP; pin its
		// supported quality mode explicitly so its reference is reproducible.
		"--rate-control", "crf", "--crf", "18",
		"--gop-source", audioSource,
		"--report", "-o", reference.Output.Path,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, goal3DiagnosticsArgs(args)...)
	cmd.Dir = assetsRoot
	cmd.Env = goal3CLIEnv()
	out, err := cmd.CombinedOutput()
	goal3WriteCLILog(t, outDir, name+"_software", out)
	if err != nil {
		t.Fatalf("%s: reference render (software): %v\n%s", name, err, tailBytes(out))
	}
	t.Logf("%s: reference (software) -> %s", name, reference.Output.Path)
	return reference.Output.Path
}

// goal3Probe is the structural + audio probe of a rendered clip.
type goal3Probe struct {
	videoFrames int64
	videoFPS    string
	width       int
	height      int
	videoDur    float64
	hasAudio    bool
	audioCodec  string
	audioDur    float64
}

func goal3ProbeClip(t *testing.T, path string) goal3Probe {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "stream=codec_type,codec_name,width,height,nb_frames,r_frame_rate,duration",
		"-show_entries", "format=duration", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var doc struct {
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			NBFrames   string `json:"nb_frames"`
			RFrameRate string `json:"r_frame_rate"`
			Duration   string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("ffprobe JSON %s: %v", path, err)
	}
	var probe goal3Probe
	for _, s := range doc.Streams {
		switch s.CodecType {
		case "video":
			probe.width, probe.height = s.Width, s.Height
			probe.videoFPS = s.RFrameRate
			probe.videoFrames, _ = strconv.ParseInt(s.NBFrames, 10, 64)
			probe.videoDur, _ = strconv.ParseFloat(strings.TrimSpace(s.Duration), 64)
		case "audio":
			probe.hasAudio = true
			probe.audioCodec = s.CodecName
			probe.audioDur, _ = strconv.ParseFloat(strings.TrimSpace(s.Duration), 64)
		}
	}
	if probe.videoDur == 0 {
		probe.videoDur, _ = strconv.ParseFloat(strings.TrimSpace(doc.Format.Duration), 64)
	}
	return probe
}

// goal3FramePNG extracts one frame as raw PNG bytes.
func goal3FramePNG(t *testing.T, path string, frame int) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), fmt.Sprintf("frame_%04d.png", frame))
	if b, err := exec.Command("ffmpeg", "-v", "error", "-i", path,
		"-vf", fmt.Sprintf("select=eq(n\\,%d)", frame),
		"-frames:v", "1", "-y", out).CombinedOutput(); err != nil {
		t.Fatalf("extract frame %d of %s: %v\n%s", frame, path, err, tailBytes(b))
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read frame %d: %v", frame, err)
	}
	return data
}

func goal3DecodePNG(t *testing.T, data []byte, context string) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode %s frame PNG: %v", context, err)
	}
	return img
}

// goal3RegionDiff counts pixels differing inside a rectangle between two frames.
func goal3RegionDiff(t *testing.T, first, second image.Image, rect image.Rectangle) int {
	t.Helper()
	different := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if first.At(x, y) != second.At(x, y) {
				different++
			}
		}
	}
	return different
}

// goal3RegionDiffTol counts pixels inside a rectangle whose largest channel
// delta exceeds tol. Two INDEPENDENT encodes of the same content are never
// bit-identical — rate control and per-frame decisions differ — so a structural
// probe must ignore sub-threshold noise instead of claiming a bleed. A real
// bleed (an overlay composited outside its box) moves pixels by far more than
// tol, which is why the bound stays meaningful.
func goal3RegionDiffTol(t *testing.T, first, second image.Image, rect image.Rectangle, tol int) int {
	t.Helper()
	different := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			ar, ag, ab, _ := first.At(x, y).RGBA()
			br, bg, bb, _ := second.At(x, y).RGBA()
			if absInt(int(ar>>8)-int(br>>8)) > tol ||
				absInt(int(ag>>8)-int(bg>>8)) > tol ||
				absInt(int(ab>>8)-int(bb>>8)) > tol {
				different++
			}
		}
	}
	return different
}

// goal3MeanAbsDiff is the per-channel mean absolute difference between two
// frames of identical geometry. It is the cross-backend parity metric.
func goal3MeanAbsDiff(t *testing.T, first, second image.Image) (float64, float64, float64) {
	t.Helper()
	bounds := first.Bounds()
	if second.Bounds() != bounds {
		t.Fatalf("frame geometry mismatch: %v vs %v", bounds, second.Bounds())
	}
	var sumR, sumG, sumB float64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ar, ag, ab, _ := first.At(x, y).RGBA()
			br, bg, bb, _ := second.At(x, y).RGBA()
			sumR += float64(absInt(int(ar>>8) - int(br>>8)))
			sumG += float64(absInt(int(ag>>8) - int(bg>>8)))
			sumB += float64(absInt(int(ab>>8) - int(bb>>8)))
		}
	}
	n := float64(bounds.Dx() * bounds.Dy())
	return sumR / n, sumG / n, sumB / n
}

// The Editorial Visual Motion V1 golden plans. Every project here is compiled
// through the full semantic pipeline (CompileSemantic → typed plan → wire
// JSON) exactly as a producer job would arrive, asserted against the V1
// contract, and written to out/editorial_v1/golden_plans/ for the render
// canaries. The golden plan is the deterministic artifact: the same compile
// twice must produce the same wire bytes.

const goldenOutDir = "../../out/editorial_v1/golden_plans"

func compileV1Plan(t *testing.T, planMap map[string]any) CompileResult {
	t.Helper()
	raw, err := json.Marshal(planMap)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("CompileSemantic: %v", err)
	}
	return result
}

func writeGoldenPlan(t *testing.T, result CompileResult, name string) string {
	t.Helper()
	dir := goldenOutDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wire, err := result.Plan.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".plan.json")
	if err := os.WriteFile(path, wire, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// uniquePortraitAsset gives every item its own logical path so the asset
// registry's one-path-one-identity rule is satisfied.
func uniquePortraitAsset(id string) map[string]any {
	return map[string]any{
		"asset_id":   id,
		"sha256":     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"url":        "assets/canary/" + id + ".png",
		"media_type": "image/png",
	}
}

func uniqueBrowserAsset(id string) map[string]any {
	return map[string]any{
		"asset_id":   id,
		"sha256":     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"url":        "assets/canary/" + id + ".png",
		"media_type": "image/png",
	}
}

func v1BasePlan(planID string, durationMS int64, items []any) map[string]any {
	return map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        planID,
		"video_id":       planID,
		"project_id":     "editorial-visual-motion-v1",
		"width":          1280, "height": 720,
		"fps_num": 24, "fps_den": 1,
		"duration_ms": durationMS,
		"background": map[string]any{
			"kind": "color", "color": []any{0.071, 0.078, 0.098, 1.0},
		},
		"items": items,
	}
}

func entityCardItem(id, name string, startMS, endMS int64, motionID string) map[string]any {
	item := map[string]any{
		"id": id, "entity_id": "person:" + id, "kind": "entity_card",
		"template_id": "PERSON", "preset_id": "phrase_default",
		"text": name, "entity_caption": name,
		"image_preset_id": "image_scale_in",
		"motion_id":       motionID,
		"params":          map[string]any{"box_width": 420.0, "box_height": 420.0},
		"start_ms":        startMS, "end_ms": endMS, "duration_ms": endMS - startMS,
		"asset_refs": []any{uniquePortraitAsset(id)},
	}
	return item
}

// contentLayers drops the leading background layer the compiler emits from
// the plan background; the V1 canaries assert on item layers only.
func contentLayers(result CompileResult) []Layer {
	layers := result.Plan.Layers
	if len(layers) > 0 && layers[0].Type == "color" {
		return layers[1:]
	}
	return layers
}

func webFanCard(id string, x float64, motionID string) map[string]any {
	return map[string]any{
		"id": id, "kind": "image",
		"template_id": "IMAGE_OVERLAY", "preset_id": "image_scale_in",
		"motion_id": motionID,
		"params":    map[string]any{"box_width": 340.0, "box_height": 220.0, "position_x": x, "position_y": 0.0},
		"start_ms":  0, "end_ms": 4000, "duration_ms": 4000,
		"asset_refs": []any{uniqueBrowserAsset(id)},
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
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

func channelError(a, b uint32) int {
	delta := int(a>>8) - int(b>>8)
	if delta < 0 {
		return -delta
	}
	return delta
}
