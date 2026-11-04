package overlay

// Six-second overlay animation fixture. It is the fast pre-flight for the
// full job: three real motions (word-lift phrase, calendar-flip date,
// fade-reveal image) plus a static text control, at the delivery geometry
// (1920x1080, 24 fps). The test renders with the real engine and fails closed
// when any item is frozen in its entrance window.
//
// Opt-in like every real-engine test in this package: CHRONON_BIN must name
// the engine. RENDERINGGEN_SKIP_GPU_E2E disables it. It is deliberately a
// pre-flight, not a substitute for checking the delivered final MP4.

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

const (
	overlayFixtureWidth  = 1920
	overlayFixtureHeight = 1080
	overlayFixtureFPS    = 24
	overlayFixtureMS     = 6000
	// A text item must visibly change in at least this many consecutive frame
	// pairs of its entrance. Six frames at 24 fps is 0.25 s, far below the
	// shortest entrance (16 frames) and above codec noise.
	overlayFixtureMinMovingFrames = 6
	overlayFixtureMinChangedRGB   = 300
	overlayFixtureChannelDelta    = 24
	// NVENC is lossy: a 1080p static card still shows a handful of pixels
	// crossing the channel delta at text edges. The bbox must stay identical;
	// this budget only tolerates that codec noise.
	overlayFixtureStaticNoiseBudget = 64
)

type overlayFixtureItem struct {
	id         string
	start, end int64
}

func TestOverlayAnimationSixSecondFixture(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available for pixel gate: %v", err)
	}

	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"overlay-animation-6s",
		"video_id":"overlay-animation-6s",
		"width":%d,"height":%d,"fps_num":%d,"fps_den":1,
		"background":{"kind":"color","color":[0.05,0.05,0.08,1]},
		"items":[
			{"id":"fixture-phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE",
			 "preset_id":"phrase_default","motion_id":"classic_playfair_word_lift",
			 "motion_params":{"enter_frames":16},
			 "text":"LA SENTENZA POTEVA ANCORA ESSERE IMPUGNATA",
			 "start_ms":0,"end_ms":1900,
			 "params":{"position_x":0,"position_y":0,"font_size_px":96,"fill":"#FFFFFF"}},
			{"id":"fixture-date","kind":"number","template_id":"TIMELINE_DATE_CARD",
			 "preset_id":"phrase_default","motion_id":"date_calendar_flip",
			 "motion_params":{"enter_frames":16},
			 "text":"6 DICEMBRE 2011",
			 "start_ms":2000,"end_ms":3900,
			 "params":{"position_x":0,"position_y":0,"font_size_px":128,"fill":"#FFFFFF"}},
			{"id":"fixture-image","kind":"image","template_id":"IMAGE_OVERLAY",
			 "preset_id":"image_fade_in","motion_id":"image_fade_reveal",
			 "motion_params":{"enter_frames":36},
			 "start_ms":4000,"end_ms":5900,
			 "params":{"width":480,"height":480,"position_x":0,"position_y":0,"fit":"contain"},
			 "asset_refs":[{"asset_id":%q,"sha256":%q,
			   "url":"https://store.example/certification.jpg","media_type":"image/jpeg"}]},
			{"id":"fixture-static","kind":"important_phrase","template_id":"IMPORTANT_PHRASE",
			 "preset_id":"static_text_smoke","text":"CONTROLLO STATICO",
			 "start_ms":0,"end_ms":6000,
			 "params":{"position_x":700,"position_y":430,"font_size_px":28,
			   "width":360,"height":80,"fill":"#FFFFFF"}}
		]
	}`, overlayFixtureWidth, overlayFixtureHeight, overlayFixtureFPS,
		certificationAssetID, certificationAssetSHA))

	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile six-second fixture: %v", err)
	}
	plan := compiled.Plan
	if len(plan.ItemWindows) != 4 {
		t.Fatalf("fixture compiled %d item windows, want 4", len(plan.ItemWindows))
	}

	outDir := t.TempDir()
	videoPath := filepath.Join(outDir, "overlay-animation-6s.mp4")
	plan.Output.Path = videoPath
	planPath := filepath.Join(outDir, "plan.json")
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal fixture plan: %v", err)
	}
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatalf("write fixture plan: %v", err)
	}

	// Render on the delivery path: Vulkan + NVENC with pinned rate control,
	// because these are the pixels the final MP4 will contain. Software
	// rasterization of the same 144 frames takes minutes and hides GPU-only
	// motion bugs.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "vulkan", "--hardware", "nvenc", "--encoder-backend", "native",
		"--gpu-hot-path-mode", "require_gpu_native",
		"--encode-preset", "p1", "--rate-control", "qp", "--qp", "23", "-o", videoPath)
	cmd.Dir = assetsRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render six-second fixture: %v\n%s", err, tailBytes(out))
	}
	assertFixtureVideoContract(t, videoPath)

	// Static control: find the ink once, then require the same ink bbox and
	// zero material pixel changes at the sampled frames.
	first := frameRGBAt(t, videoPath, 0, overlayFixtureWidth, overlayFixtureHeight)
	staticInk, ok := inkBounds(first, overlayFixtureWidth, overlayFixtureHeight, 1280, 800, 1919, 1079, 60)
	if !ok {
		t.Fatalf("static control has no ink in its bottom-right region")
	}
	staticRegion := expandRegion(staticInk, 4)
	for _, frame := range []int{0, overlayFixtureMS / 2 * overlayFixtureFPS / 1000, overlayFixtureMS*overlayFixtureFPS/1000 - 1} {
		if frame == 0 {
			continue
		}
		rgb := frameRGBAt(t, videoPath, frame, overlayFixtureWidth, overlayFixtureHeight)
		if changed := changedPixelsInRegion(first, rgb, overlayFixtureWidth, staticRegion, overlayFixtureChannelDelta); changed > overlayFixtureStaticNoiseBudget {
			t.Errorf("static control changed %d pixels in frame %d (budget %d)", changed, frame, overlayFixtureStaticNoiseBudget)
		}
		if bounds, ok := inkBounds(rgb, overlayFixtureWidth, overlayFixtureHeight,
			staticRegion.x0, staticRegion.y0, staticRegion.x1, staticRegion.y1, 60); !ok || bounds != staticInk {
			t.Errorf("static control bbox changed in frame %d: got %+v (ok=%v), want %+v", frame, bounds, ok, staticInk)
		}
	}

	// Each item gets its own compiled window. Only that item is active there,
	// so pixel motion outside the static control belongs to the item.
	byID := map[string]ItemWindow{}
	for _, window := range plan.ItemWindows {
		byID[window.ItemID] = window
	}
	items := []overlayFixtureItem{
		{id: "fixture-phrase", start: 0, end: 1900},
		{id: "fixture-date", start: 2000, end: 3900},
		{id: "fixture-image", start: 4000, end: 5900},
	}
	for _, item := range items {
		window, ok := byID[item.id]
		if !ok {
			t.Fatalf("item %s has no compiled frame window", item.id)
		}
		// Compare each entrance frame against the item's first frame instead
		// of against its immediate predecessor. A slow 35-frame opacity fade
		// changes only ~3 RGB levels per frame, which lossy NVENC plus the
		// channel delta cannot resolve, while a pop-in still registers in a
		// single pair. Cumulative distance from the entrance start measures
		// "this layer animates", which is what the pre-flight must fail on.
		moving, sampled := 0, 0
		var reference []byte
		for i := 0; i < 12; i++ {
			frame := int(window.StartFrame) + i
			if frame >= int(window.EndFrame) {
				break
			}
			rgb := frameRGBAt(t, videoPath, frame, overlayFixtureWidth, overlayFixtureHeight)
			if reference == nil {
				reference = rgb
				continue
			}
			sampled++
			if changed := changedRGBOutsideStatic(reference, rgb, overlayFixtureWidth, overlayFixtureHeight, staticRegion); changed >= overlayFixtureMinChangedRGB {
				moving++
			}
		}
		if moving < overlayFixtureMinMovingFrames {
			t.Errorf("%s moved in only %d of %d entrance frames (want >= %d): the overlay is frozen",
				item.id, moving, sampled, overlayFixtureMinMovingFrames)
		}
	}

	// The fixture is only a pre-flight; it must also leave a decodable file.
	decode := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", videoPath, "-f", "null", "-")
	if out, err := decode.CombinedOutput(); err != nil {
		t.Fatalf("fixture MP4 does not decode fully: %v\n%s", err, tailBytes(out))
	}
}

type fixtureRegion struct{ x0, y0, x1, y1 int }

func inkBounds(rgb []byte, w, h, x0, y0, x1, y1, threshold int) (fixtureRegion, bool) {
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 >= w {
		x1 = w - 1
	}
	if y1 >= h {
		y1 = h - 1
	}
	best := fixtureRegion{x0: w, y0: h, x1: -1, y1: -1}
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			i := (y*w + x) * 3
			if (int(rgb[i])+int(rgb[i+1])+int(rgb[i+2]))/3 < threshold {
				continue
			}
			if x < best.x0 {
				best.x0 = x
			}
			if y < best.y0 {
				best.y0 = y
			}
			if x > best.x1 {
				best.x1 = x
			}
			if y > best.y1 {
				best.y1 = y
			}
		}
	}
	return best, best.x1 >= best.x0
}

func expandRegion(r fixtureRegion, px int) fixtureRegion {
	return fixtureRegion{r.x0 - px, r.y0 - px, r.x1 + px, r.y1 + px}
}

func changedPixelsInRegion(a, b []byte, w int, region fixtureRegion, threshold int) int {
	changed := 0
	for y := region.y0; y <= region.y1; y++ {
		for x := region.x0; x <= region.x1; x++ {
			i := (y*w + x) * 3
			if absByteDiff(a[i], b[i]) > threshold ||
				absByteDiff(a[i+1], b[i+1]) > threshold ||
				absByteDiff(a[i+2], b[i+2]) > threshold {
				changed++
			}
		}
	}
	return changed
}

func changedRGBOutsideStatic(a, b []byte, w, h int, static fixtureRegion) int {
	changed := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x >= static.x0 && x <= static.x1 && y >= static.y0 && y <= static.y1 {
				continue
			}
			i := (y*w + x) * 3
			if absByteDiff(a[i], b[i]) > overlayFixtureChannelDelta ||
				absByteDiff(a[i+1], b[i+1]) > overlayFixtureChannelDelta ||
				absByteDiff(a[i+2], b[i+2]) > overlayFixtureChannelDelta {
				changed++
			}
		}
	}
	return changed
}

func assertFixtureVideoContract(t *testing.T, path string) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,nb_frames,r_frame_rate",
		"-show_entries", "format=duration", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe fixture: %v", err)
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
		t.Fatalf("decode ffprobe fixture JSON: %v", err)
	}
	if len(probe.Streams) != 1 {
		t.Fatalf("fixture has %d video streams, want 1", len(probe.Streams))
	}
	s := probe.Streams[0]
	if s.Width != overlayFixtureWidth || s.Height != overlayFixtureHeight {
		t.Errorf("fixture resolution %dx%d, want %dx%d", s.Width, s.Height, overlayFixtureWidth, overlayFixtureHeight)
	}
	if s.RFrameRate != "24/1" {
		t.Errorf("fixture fps %q, want 24/1", s.RFrameRate)
	}
	frames, err := strconv.Atoi(strings.TrimSpace(s.NBFrames))
	if err != nil || frames < overlayFixtureMS*overlayFixtureFPS/1000 {
		t.Errorf("fixture frame count %q, want >= %d", s.NBFrames, overlayFixtureMS*overlayFixtureFPS/1000)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(probe.Format.Duration), 64)
	if err != nil || duration < 5.9 {
		t.Errorf("fixture duration %q, want >= 5.9 s", probe.Format.Duration)
	}
}
