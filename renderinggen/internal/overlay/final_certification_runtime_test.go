// Final rendering certification — runtime level.
//
// These tests render real MP4s with the real chronon3d_cli binary through the
// same plans the compile-level suite certifies, then run the three validation
// layers on every output:
//
//	A. structural  — ffprobe (resolution, fps, frame count, duration)
//	B. decode      — full bitstream decode, pass only on ffmpeg exit 0
//	C. pixel       — background preservation outside the entity bbox,
//	                 no fully-black frame (the Vulkan black-output regression)
//
// The suite is opt-in and skips when the binary is unavailable: compile-level
// certification (final_certification_test.go) always runs, this one needs a
// GPU-capable build environment. Point CHRONON_BIN at the chronon3d_cli
// binary to enable it.
package overlay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const certificationDurationFrames = int64(125)

// chrononBinFor returns the real binary or skips the calling test.
func chrononBinFor(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("CHRONON_BIN")
	if bin == "" {
		bin = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("chronon3d_cli not available (%s): %v", bin, err)
	}
	return bin
}

func certificationAssetsRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("RENDERINGGEN_ASSETS_ROOT")
	if root == "" {
		root = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/RenderingGen/testdata/golden"
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("assets root not available (%s): %v", root, err)
	}
	return root
}

// certificationPlan compiles the registry fixture for one preset into the
// exact chronon.render-plan.v2 the runtime certification renders. It is
// deterministic, so pixel tests can read the entity's declared geometry from
// the same plan that produced the MP4 instead of hard-coding sample points.
func certificationPlan(t *testing.T, presetID string) *Plan {
	t.Helper()
	def, err := ResolveOfficialPreset(presetID)
	if err != nil {
		t.Fatalf("resolve registry preset: %v", err)
	}
	plan, err := CompileFastEntityOverlays(presetID, 1920, 1080, 24, 1, certificationDurationFrames,
		"color:#EEF1E7", []FastEntityOverlay{certificationFixture(def)})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return plan
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
		"-o", videoPath)
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
// belongs to the entity itself. Conventions match the engine (verified at
// runtime): image frames are centred at canvas_centre + position, text frames
// at position (canvas coordinates). The background color layer is ignored.
func entityCoverage(plan *Plan) [4]float64 {
	for _, layer := range plan.Layers {
		if (layer.Type != "image" && layer.Type != "text") || len(layer.Position) < 2 || len(layer.Size) < 2 {
			continue
		}
		cx, cy := layer.Position[0], layer.Position[1]
		if layer.Type == "image" {
			cx += float64(plan.Canvas.Width) / 2
			cy += float64(plan.Canvas.Height) / 2
		}
		return [4]float64{cx - layer.Size[0]/2, cy - layer.Size[1]/2, cx + layer.Size[0]/2, cy + layer.Size[1]/2}
	}
	return [4]float64{}
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

// TestFinal_AllOfficialPresetsRender is the runtime registry gate: every
// official preset must produce a structurally valid, fully decodable MP4
// with the background preserved. Skipped presets do not exist: registry
// coverage and rendering are asserted together, in one place.
func TestFinal_AllOfficialPresetsRender(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()

	for _, id := range OfficialPresetIDs() {
		t.Run(id, func(t *testing.T) {
			plan := certificationPlan(t, id)
			videoPath := renderCertificationPlan(t, bin, assetsRoot, outDir, id, id)

			// A. structural
			probeMP4Structural(t, videoPath)
			// B. decode
			decodeFully(t, videoPath)
			// C. pixel: first, middle and last frames must keep the
			// background and never come back black.
			assertBackgroundPreserved(t, plan, videoPath, []int{0, int(certificationDurationFrames) / 2, int(certificationDurationFrames) - 1})
		})
	}
}

// TestFinal_EntityCenterDiffersFromBackground proves the entity really drew:
// the pixel at the entity's center must differ from the untouched Pale Olive
// background in a mid-animation frame. This is the assertion that catches a
// silently-missing layer even when every structural check is green. The
// sample point is derived from the compiled plan (image frames are centred at
// canvas_centre + position), never hard-coded.
func TestFinal_EntityCenterDiffersFromBackground(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()

	plan := certificationPlan(t, "image_scale_in")
	videoPath := renderCertificationPlan(t, bin, assetsRoot, outDir, "image_scale_in", "image_scale_in")
	coverage := entityCoverage(plan)
	if coverage[2] <= coverage[0] || coverage[3] <= coverage[1] {
		t.Fatal("entity coverage missing: cannot derive the sample point")
	}
	cx := int((coverage[0] + coverage[2]) / 2)
	cy := int((coverage[1] + coverage[3]) / 2)
	mid := int(certificationDurationFrames) / 2
	centerLuma := sampleLuma(t, videoPath, mid, cx, cy)
	cornerLuma := sampleLuma(t, videoPath, mid, 50, 50)
	if diff := centerLuma - cornerLuma; diff > -20 && diff < 20 {
		t.Errorf("entity center (%d,%d) luma=%.1f vs background %.1f: entity layer is not visibly drawn", cx, cy, centerLuma, cornerLuma)
	}
}

// TestFinal_ExclusiveEndNoFrame125 checks the frame-count contract that bit
// the pipeline before: 125 declared frames must produce exactly 125 frames —
// neither 124 (lost tail) nor 126 (phantom frame from an inclusive end).
func TestFinal_ExclusiveEndNoFrame125(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()

	videoPath := renderCertificationPlan(t, bin, assetsRoot, outDir, "static_text_smoke", "static_text_smoke")
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-count_frames", "-show_entries", "stream=nb_read_frames", "-of",
		"csv=p=0", videoPath).Output()
	if err != nil {
		t.Fatalf("ffprobe count_frames: %v", err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatalf("parse frame count %q: %v", string(out), err)
	}
	if n != certificationDurationFrames {
		t.Errorf("decoded %d frames, want exactly %d (exclusive-end contract: last index %d)",
			n, certificationDurationFrames, certificationDurationFrames-1)
	}
}

// TestFinal_MissingAssetFailsClosed renders a plan whose image asset does
// not exist: Chronon must exit non-zero — a "successful" render of a missing
// entity is the fail-open bug this suite exists to prevent.
func TestFinal_MissingAssetFailsClosed(t *testing.T) {
	bin := chrononBinFor(t)
	outDir := t.TempDir()

	plan, err := CompileFastEntityOverlays("missing-asset", 1920, 1080, 24, 1, certificationDurationFrames,
		"color:#EEF1E7", []FastEntityOverlay{{
			Type: "image", StartFrame: 0, EndFrame: certificationDurationFrames,
			Asset: "does_not_exist.jpg", Position: "center", Size: 260, Animation: "fade_in",
		}})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	videoPath := filepath.Join(outDir, "missing.mp4")
	plan.Output.Path = videoPath
	planPath := filepath.Join(outDir, "plan.json")
	planBytes, _ := json.MarshalIndent(plan, "", "  ")
	if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "render", "--plan", planPath, "--assets-root", outDir,
		"--backend", "software", "--encoder-backend", "pipe", "--hardware", "none",
		"--gpu-hot-path-mode", "auto", "-o", videoPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("render of missing asset succeeded (exit 0): fail-open bug\n%s", tailBytes(out))
	}
	if _, statErr := os.Stat(videoPath); statErr == nil {
		t.Errorf("render of missing asset produced an output file: fail-open bug\n%s", tailBytes(out))
	}
}

// TestFinal_DeterministicPlanRendered proves same input → same plan JSON for
// a registry preset (byte-identical), the deterministic-plan certification gate.
func TestFinal_DeterministicPlanRendered(t *testing.T) {
	def, err := ResolveOfficialPreset("image_scale_in")
	if err != nil {
		t.Fatal(err)
	}
	build := func() []byte {
		plan, err := CompileFastEntityOverlays("determinism", 1920, 1080, 24, 1,
			certificationDurationFrames, "color:#EEF1E7", []FastEntityOverlay{certificationFixture(def)})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		plan.Output.Path = "out.mp4"
		data, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first, second := build(), build()
	if string(first) != string(second) {
		t.Error("same input produced different plan JSON: deterministic-plan gate failed")
	}
}

// TestFinal_RepeatedRenderSameProcess renders the same preset several times
// in one test process and asserts every output is structurally valid — the
// repeated-render stability gate (deeper memory assertions live in the
// Chronon daemon stability suite).
func TestFinal_RepeatedRenderSameProcess(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()

	for i := 0; i < 3; i++ {
		videoPath := renderCertificationPlan(t, bin, assetsRoot, outDir,
			"static_text_smoke", fmt.Sprintf("static_text_smoke_repeat%d", i))
		probeMP4Structural(t, videoPath)
		decodeFully(t, videoPath)
	}
}

func tailBytes(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 800 {
		s = s[len(s)-800:]
	}
	return s
}
