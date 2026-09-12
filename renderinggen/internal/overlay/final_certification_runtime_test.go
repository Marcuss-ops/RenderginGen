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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// chrononBinFor returns the real binary or skips the calling test. The binary
// is never pinned to an absolute machine-specific path: the caller supplies
// CHRONON_BIN, otherwise a checked-out build artifact under the workspace is
// discovered, otherwise the test skips.
func chrononBinFor(t *testing.T) string {
	t.Helper()
	if bin := strings.TrimSpace(os.Getenv("CHRONON_BIN")); bin != "" {
		if _, err := os.Stat(bin); err != nil {
			t.Skipf("CHRONON_BIN=%s not available: %v", bin, err)
		}
		return bin
	}
	if bin := discoverBuiltChrononBinary(); bin != "" {
		return bin
	}
	t.Skip("chronon3d_cli not available; set CHRONON_BIN to a built chronon3d_cli")
	return ""
}

// discoverBuiltChrononBinary looks for a built chronon3d_cli under the
// workspace (Chronon3d/build/<preset>/apps/...), walking up from this test's
// source file. It never hardcodes a home directory.
func discoverBuiltChrononBinary() string {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	dir := filepath.Dir(source)
	for i := 0; i < 8; i++ {
		matches, _ := filepath.Glob(filepath.Join(dir, "Chronon3d", "build", "chronon", "linux-video-release", "apps", "chronon3d_cli", "chronon3d_cli"))
		if len(matches) == 0 {
			matches, _ = filepath.Glob(filepath.Join(dir, "Chronon3d", "build", "chronon", "*", "apps", "chronon3d_cli", "chronon3d_cli"))
		}
		if len(matches) == 0 {
			matches, _ = filepath.Glob(filepath.Join(dir, "Chronon3d", "build", "*", "apps", "chronon3d_cli", "chronon3d_cli"))
		}
		if len(matches) > 0 {
			return matches[0]
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
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

// TestFinal_AppleStylesPixelByPixel certifies the three checked-in Apple
// compositions at raster level. The image preset is rendered at an animation
// frame where the profiles are distinguishable, then every pixel is compared;
// this catches a catalog change that still compiles but collapses styles to
// the same visual output.
func TestFinal_AppleStylesPixelByPixel(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()
	profiles := []struct {
		name   string
		preset string
	}{
		{"crime", "image_scale_in"},
		{"discovery", "image_slide_right"},
		{"young", "modern_rounded_pop"},
	}
	frames := make([][]byte, 0, len(profiles))
	for _, profile := range profiles {
		video := renderCertificationPlan(t, bin, assetsRoot, outDir, profile.preset, "apple_style_"+profile.name)
		probeMP4Structural(t, video)
		decodeFully(t, video)
		// The fixture starts at frame 10 and the catalog image enter motion is
		// eight frames long; frame 12 is inside that active transition window.
		frames = append(frames, extractFramePNG(t, video, 12))
	}
	for i := 0; i < len(frames); i++ {
		for j := i + 1; j < len(frames); j++ {
			assertPixelDifference(t, frames[i], frames[j], 1000)
		}
	}
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
