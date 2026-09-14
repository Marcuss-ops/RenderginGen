// Goal 3 end-to-end harness — background (image + video) and text watermark.
//
// This file renders REAL clips with the real chronon3d_cli on the GPU-native
// lane (vulkan + nvenc native, no CPU fallback), from the exact semantic
// contract PipelineGen submits, and then certifies the artefacts:
//
//	structural  — ffprobe geometry/fps/frame count/duration
//	decode      — the whole bitstream decodes
//	A/V         — an audio stream is present and its duration matches the video
//	pixel       — the background plate is really composited at the canvas border,
//	              and the watermark really changes the frame
//	determinism — the same plan rendered twice produces identical frames
//
// It is opt-in like the rest of the runtime certification suite: it skips when
// chronon3d_cli is unavailable, and it needs the GPU (the strict native lane
// fails closed without it). The rendered MP4s are written to a STABLE directory
// so a human can inspect them:
//
//	GOAL3_RENDER_OUT_DIR overrides it; otherwise
//	<repo>/refactored/ops/benchmarks/goal3-e2e-<YYYYMMDD>/
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
	"strconv"
	"strings"
	"testing"
	"time"
)

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
	goal3PlateR      = 30
	goal3PlateG      = 90
	goal3PlateB      = 168
	goal3VideoPlateR = 168
	goal3VideoPlateG = 60
	goal3VideoPlateB = 30
	goal3Tol         = 30
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

	audioSource := filepath.Join(assetsRoot, filepath.FromSlash("assets/semantic/goal3-foreground/source.mp4"))
	args := []string{
		"render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "vulkan",
		"--hardware", "nvenc", "--encoder-backend", "native",
		"--gpu-hot-path-mode", "require_gpu_native",
		"--encode-preset", "p1",
		// The source audio is muxed by the native A/V path (the production
		// worker passes exactly this flag), so A/V sync is real, not implied.
		"--gop-source", audioSource,
		"--report",
		"-o", videoPath,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = assetsRoot
	started := time.Now()
	out, err := cmd.CombinedOutput()
	wall := time.Since(started)
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
		"--gop-source", audioSource,
		"--report", "-o", reference.Output.Path,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = assetsRoot
	out, err := cmd.CombinedOutput()
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

// TestGoal3_FinalClipBackgroundWatermark is the end-to-end Goal 3 proof.
func TestGoal3_FinalClipBackgroundWatermark(t *testing.T) {
	bin := chrononBinFor(t)
	outDir := goal3OutDir(t)
	assetsRoot := t.TempDir()
	fixtures := goal3Fixtures(t, assetsRoot)
	t.Logf("outputs: %s", outDir)

	// A font asset must exist at the logical path the watermark references.
	fontSource := filepath.Join(repoRootAt(t), "RenderingGen", "testdata", "golden", "Poppins-Bold.ttf")
	if data, err := os.ReadFile(fontSource); err == nil {
		fontTarget := filepath.Join(assetsRoot, "assets", "semantic", "goal3-font", "Poppins-Bold.ttf")
		if err := os.MkdirAll(filepath.Dir(fontTarget), 0o755); err != nil {
			t.Fatalf("font dir: %v", err)
		}
		if err := os.WriteFile(fontTarget, data, 0o644); err != nil {
			t.Fatalf("font asset: %v", err)
		}
	}

	watermark := goal3WatermarkBlock()
	imageBlock := goal3BackgroundBlock(fixtures["image"])
	videoBlock := goal3BackgroundBlock(fixtures["video"])

	// ── Render the matrix ────────────────────────────────────────────────
	// 01: image background + scaled foreground + text watermark (the Goal 3
	//     "complete clip": background + content + watermark).
	rawImage := goal3PlanRaw("goal3-image-bg", fixtures["foreground"], imageBlock, watermark, 70)
	imageClip, imagePlan := goal3Render(t, bin, assetsRoot, outDir, "01_image_background_watermark", rawImage)

	// 01b: the SAME composition without the watermark — the control that
	//      proves the watermark really changes pixels.
	rawImageNoWM := goal3PlanRaw("goal3-image-bg-nowm", fixtures["foreground"], imageBlock, "", 70)
	imageControl, _ := goal3Render(t, bin, assetsRoot, outDir, "01b_image_background_no_watermark", rawImageNoWM)

	// 01c: the same image composition rendered a second time, in a second
	//      process — the determinism probe for the fit/scaling contract.
	imageRepeat, _ := goal3Render(t, bin, assetsRoot, outDir, "01c_image_background_repeat", rawImage)

	// 02: video background (2 s plate, clip is 5 s) + scaled foreground +
	//     watermark. Two decoded sources: the multi-source compositor path.
	//     The plate is authored at the canvas geometry: the GPU-native
	//     compositor composites same-size video surfaces, and the loop flag
	//     must keep the plate alive for the 3 s the plate does not cover.
	rawVideo := goal3PlanRaw("goal3-video-bg", fixtures["foreground"], videoBlock, watermark, 70)
	videoClip, videoPlan := goal3Render(t, bin, assetsRoot, outDir, "02_video_background_watermark", rawVideo)

	// 03: full-canvas foreground + static text watermark. The cheapest shape:
	//     one source, one static overlay.
	rawSourceOnly := goal3PlanRaw("goal3-source-wm", fixtures["foreground"], "", watermark, 0)
	sourceClip, sourcePlan := goal3Render(t, bin, assetsRoot, outDir, "03_source_watermark", rawSourceOnly)

	// ── Plan-level contract (what was actually handed to the renderer) ───
	for _, tc := range []struct {
		name     string
		plan     *Plan
		wantKind string
	}{
		{"01_image_background_watermark", imagePlan, "image"},
		{"02_video_background_watermark", videoPlan, "video"},
	} {
		var bg *Layer
		for i := range tc.plan.Layers {
			if tc.plan.Layers[i].ID == "background" {
				bg = &tc.plan.Layers[i]
			}
		}
		if bg == nil {
			t.Fatalf("%s: compiled plan has no background layer", tc.name)
		}
		if bg.Type != tc.wantKind {
			t.Errorf("%s: background layer type %q, want %q", tc.name, bg.Type, tc.wantKind)
		}
		if bg.Fit != "cover" {
			t.Errorf("%s: background fit %q, want cover", tc.name, bg.Fit)
		}
		if bg.DurationFrames != tc.plan.Canvas.DurationFrames || bg.DurationFrames <= 0 {
			t.Errorf("%s: background spans %d frames, want the full canvas %d", tc.name, bg.DurationFrames, tc.plan.Canvas.DurationFrames)
		}
	}
	// The static watermark contract on the plan that really rendered.
	for _, tc := range []struct {
		name string
		plan *Plan
	}{{"01", imagePlan}, {"02", videoPlan}, {"03", sourcePlan}} {
		var wm *Layer
		for i := range tc.plan.Layers {
			if tc.plan.Layers[i].ID == "watermark" {
				wm = &tc.plan.Layers[i]
			}
		}
		if wm == nil {
			t.Fatalf("%s: compiled plan has no watermark layer", tc.name)
		}
		if wm.StartFrame != 0 || wm.DurationFrames != tc.plan.Canvas.DurationFrames {
			t.Errorf("%s: watermark window %d+%d, want the whole canvas %d", tc.name, wm.StartFrame, wm.DurationFrames, tc.plan.Canvas.DurationFrames)
		}
		if wm.Animation != nil || len(wm.TextAnimators) != 0 {
			t.Errorf("%s: watermark is not static (animation=%v animators=%d)", tc.name, wm.Animation != nil, len(wm.TextAnimators))
		}
	}

	// ── Structural + decode + A/V on every rendered clip ─────────────────
	clips := []struct {
		name string
		path string
	}{
		{"01_image_background_watermark", imageClip},
		{"01b_image_background_no_watermark", imageControl},
		{"01c_image_background_repeat", imageRepeat},
		{"02_video_background_watermark", videoClip},
		{"03_source_watermark", sourceClip},
	}
	for _, clip := range clips {
		t.Run("structural/"+clip.name, func(t *testing.T) {
			probe := goal3ProbeClip(t, clip.path)
			if probe.width != goal3Width || probe.height != goal3Height {
				t.Errorf("geometry %dx%d, want %dx%d", probe.width, probe.height, goal3Width, goal3Height)
			}
			if probe.videoFPS != "24/1" {
				t.Errorf("fps %q, want 24/1", probe.videoFPS)
			}
			if probe.videoFrames < goal3Frames {
				t.Errorf("frames %d, want >= %d", probe.videoFrames, goal3Frames)
			}
			// The exclusive-end contract: `duration_frames` frames at fps give a
			// container duration of (frames-1)/fps, so the last whole frame is
			// the assertion that matters — not a rounded-up second.
			if want := float64(goal3Frames-2) / goal3FPS; probe.videoDur < want {
				t.Errorf("duration %.3fs, want >= %.3fs (%d frames at %dfps)", probe.videoDur, want, goal3Frames, goal3FPS)
			}
			if !probe.hasAudio || probe.audioCodec != "aac" {
				t.Fatalf("audio stream missing or not AAC (has=%v codec=%q): the native A/V path dropped it", probe.hasAudio, probe.audioCodec)
			}
			// Sync: the muxed audio must cover the video, not stop short.
			if delta := probe.videoDur - probe.audioDur; delta > 0.15 || delta < -0.15 {
				t.Errorf("A/V duration drift %.3fs (video %.3f, audio %.3f)", delta, probe.videoDur, probe.audioDur)
			}
			if out, err := exec.Command("ffmpeg", "-v", "error", "-i", clip.path, "-f", "null", "-").CombinedOutput(); err != nil {
				t.Fatalf("full decode failed: %v\n%s", err, tailBytes(out))
			}
		})
	}

	// ── Cross-backend parity: the native lane against the CPU reference ───
	//
	// Every pixel assertion above is relative (this render vs that render).
	// These are absolute: the SAME compiled plan, rendered by the reference
	// lane, must agree with the GPU-native result. This is the oracle that
	// catches a whole family of defects the relative probes cannot see — a
	// shifted image quad, a dropped layer, a mis-scaled box, or a transfer
	// function applied by one backend and not the other.
	for _, tc := range []struct {
		name  string
		plan  *Plan
		clip  string
		frame int
	}{
		{"image_background_watermark", imagePlan, imageClip, 60},
		{"video_background_watermark", videoPlan, videoClip, 60},
		{"source_watermark", sourcePlan, sourceClip, 60},
	} {
		t.Run("parity/"+tc.name, func(t *testing.T) {
			reference := goal3RenderSoftware(t, bin, assetsRoot, outDir, tc.name, tc.plan)
			native := goal3DecodePNG(t, goal3FramePNG(t, tc.clip, tc.frame), "native lane")
			refFrame := goal3DecodePNG(t, goal3FramePNG(t, reference, tc.frame), "reference lane")
			goal3AssertNativeMatchesReference(t, native, refFrame, tc.name)
		})
	}

	// ── Engine boundary: a plate the GPU-native compositor cannot honour ──
	//
	// Goal 3 asks for a video background with deterministic scaling. On this
	// build the strict lane composites video surfaces 1:1, and the DirectYUV
	// lane — the one that DOES scale NV12 overlays — refuses a second video
	// source by design ("requires_full_frame_rgba"). So a video plate whose
	// decoded geometry is not the canvas is unreachable for now. What IS
	// reachable, and what this asserts, is the fail-closed half of the
	// requirement: the job must refuse the frame instead of rendering the
	// composition with the plate silently dropped, mis-placed, or replaced by
	// a black canvas.
	t.Run("boundary/non_canvas_video_plate_fails_closed", func(t *testing.T) {
		raw := goal3PlanRaw("goal3-video-bg-small", fixtures["foreground"], goal3BackgroundBlock(fixtures["videoSmall"]), watermark, 70)
		out := goal3RenderRefused(t, bin, assetsRoot, outDir, "02b_non_canvas_video_plate", raw)
		// The refusal must be the compositor's residency decision, not an
		// unrelated failure (a missing asset, a decode error, a bad flag):
		// that is what makes it a boundary rather than a bug.
		if !strings.Contains(out, "native residency violation") &&
			!strings.Contains(out, "not bound to the encode surface") {
			t.Errorf("non-canvas video plate was refused for an unexpected reason:\n%s", tailBytes([]byte(out)))
		}
		if _, err := os.Stat(filepath.Join(outDir, "02b_non_canvas_video_plate.mp4")); err == nil {
			t.Error("the refused composition still produced an artefact: fail-closed means no output file")
		}
	})

	// ── Pixel evidence ───────────────────────────────────────────────────
	t.Run("pixel/image_background_is_composited", func(t *testing.T) {
		frame := goal3DecodePNG(t, goal3FramePNG(t, imageClip, 60), "image bg")
		// 70% foreground: the outer 15% band is background on every side. The
		// probe is the plate's own colour, so a black canvas (the historical
		// missing-background regression) and a background that never made it
		// into the frame both fail — and so does a plate stretched from 800x800
		// instead of cover-cropped, because the sampled points are outside the
		// 16:9 crop the source would occupy.
		for _, pt := range []image.Point{{24, 24}, {goal3Width - 24, 24}, {24, goal3Height - 24}, {goal3Width - 24, goal3Height - 24}, {24, goal3Height / 2}} {
			r, g, b, _ := frame.At(pt.X, pt.Y).RGBA()
			gotR, gotG, gotB := int(r>>8), int(g>>8), int(b>>8)
			if absInt(gotR-goal3PlateR) > goal3Tol || absInt(gotG-goal3PlateG) > goal3Tol || absInt(gotB-goal3PlateB) > goal3Tol {
				t.Errorf("background pixel at %v = rgb(%d,%d,%d), want the plate rgb(%d,%d,%d) ±%d",
					pt, gotR, gotG, gotB, goal3PlateR, goal3PlateG, goal3PlateB, goal3Tol)
			}
		}
	})

	t.Run("pixel/watermark_changes_the_frame", func(t *testing.T) {
		with := goal3DecodePNG(t, goal3FramePNG(t, imageClip, 60), "watermarked")
		without := goal3DecodePNG(t, goal3FramePNG(t, imageControl, 60), "control")
		// The watermark is top-right with a 64 px margin and a ~48 px em; the
		// box below is deliberately generous around that placement.
		box := image.Rect(goal3Width-560, 40, goal3Width-40, 200)
		if different := goal3RegionDiffTol(t, with, without, box, 16); different < 1500 {
			t.Errorf("watermark band differs in only %d pixels: the overlay did not render", different)
		}
		// And it is LOCAL: a top-right watermark must not disturb the opposite
		// corner of the canvas. Two independent encodes differ by a little
		// encoder noise, so the probe ignores sub-threshold deltas; the bound is
		// still two orders of magnitude below the watermark band itself, so a
		// watermark composited over the whole frame fails here.
		quiet := image.Rect(40, goal3Height-260, 460, goal3Height-40)
		if different := goal3RegionDiffTol(t, with, without, quiet, 16); different > 200 {
			t.Errorf("bottom-left quadrant differs in %d pixels: the watermark bled outside its box", different)
		}
	})

	t.Run("pixel/video_background_is_composited", func(t *testing.T) {
		withBg := goal3DecodePNG(t, goal3FramePNG(t, videoClip, 60), "video bg")
		fullFrame := goal3DecodePNG(t, goal3FramePNG(t, sourceClip, 60), "source only")
		// Same foreground, same instant: with background=none the 1920x1080
		// source covers the border; with the video plate the border is the
		// plate. Identical border pixels would mean the second source was
		// dropped instead of composited.
		different := goal3RegionDiff(t, withBg, fullFrame, image.Rect(0, 0, goal3Width, 30))
		if different == 0 {
			t.Error("top strip identical to the source-only render: the video background was not composited")
		}
	})

	t.Run("pixel/video_background_is_held_to_the_last_frame", func(t *testing.T) {
		// The plate is 2 s, the clip is 5 s, and the plate is a SOLID colour.
		// The probe is deliberately self-referential: the border of the last
		// frame must carry the SAME colour as the border inside the plate's own
		// window. That is exactly the difference between "the background is
		// held/looped past its last frame" and "the background ended at 2 s and
		// the rest of the clip renders on black" — and, unlike an absolute
		// probe, it does not depend on how the fixture's YUV encode maps the
		// authored RGB (a limited-range round trip shifts the absolute value).
		border := func(frame int) (int, int, int) {
			img := goal3DecodePNG(t, goal3FramePNG(t, videoClip, frame), "video bg held")
			r, g, b, _ := img.At(goal3Width/2, 16).RGBA()
			return int(r >> 8), int(g >> 8), int(b >> 8)
		}
		insideR, insideG, insideB := border(24)
		if insideR < 40 && insideG < 40 && insideB < 40 {
			t.Fatalf("frame 24 border = rgb(%d,%d,%d): the video plate is not composited at all", insideR, insideG, insideB)
		}
		lastR, lastG, lastB := border(110)
		if absInt(lastR-insideR) > 8 || absInt(lastG-insideG) > 8 || absInt(lastB-insideB) > 8 {
			t.Errorf("border at frame 110 = rgb(%d,%d,%d) but rgb(%d,%d,%d) inside the plate's own window: the plate did not survive its last frame",
				lastR, lastG, lastB, insideR, insideG, insideB)
		}
	})

	t.Run("determinism/same_plan_same_frames", func(t *testing.T) {
		first := goal3DecodePNG(t, goal3FramePNG(t, imageClip, 30), "first render")
		second := goal3DecodePNG(t, goal3FramePNG(t, imageRepeat, 30), "repeat render")
		if different := goal3RegionDiff(t, first, second, image.Rect(0, 0, goal3Width, goal3Height)); different != 0 {
			t.Errorf("repeat render differs in %d pixels: the fit/scaling contract is not deterministic", different)
		}
	})
}
