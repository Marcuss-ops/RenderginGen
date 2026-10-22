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
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

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
		officialFontPath:                          "Poppins-Bold.ttf",
		"assets/fonts/Bricolage-Grotesque.ttf":    "Bricolage-Grotesque.ttf",
		"assets/fonts/PlayfairDisplay-Italic.ttf": "PlayfairDisplay-Italic.ttf",
		"assets/fonts/Inter.ttf":                  "Inter.ttf",
		"assets/fonts/Inter-Bold.ttf":             "Inter-Bold.ttf",
		officialCyrillicFontPath:                  "DejaVuSans.ttf",
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

// goal3Geometry is the certified clip geometry: 5 s at the YouTube contract
// rate, long enough to prove the background survives past a short plate's end.
const ()

// goal3PlateRGB is the same colour as the probe expects it back from the
// render: 8-bit channels with a tolerance that absorbs the RGB→YUV420→RGB
// round trip the encoder performs.
const ()

// absInt is the tolerance probe's distance helper.
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
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

// The Editorial Visual Motion V1 golden plans. Every project here is compiled
// through the full semantic pipeline (CompileSemantic → typed plan → wire
// JSON) exactly as a producer job would arrive, asserted against the V1
// contract, and written to out/editorial_v1/golden_plans/ for the render
// canaries. The golden plan is the deterministic artifact: the same compile
// twice must produce the same wire bytes.
