// goal3_e2e_scenario_test.go is the Goal 3 end-to-end SCENARIO: one clip with a
// background plate (image and video families) and a text watermark, rendered on
// the strict GPU-native lane and certified structurally, by decode, by A/V
// agreement, by pixel probe and by determinism.
//
// It was one 882-line file together with a large helper library. The scenario is
// deliberately kept in ONE test function — it is a single render chain, and
// splitting it into "background tests" and "watermark tests" would either render
// the clip several times or share state between subtests, both worse than the
// long function. What the split buys is that the machinery it needs is now in
// goal3_harness_test.go, so the scenario reads as assertions on a rendered clip
// instead of arithmetic on file paths.
//
// It is opt-in: it skips when chronon3d_cli is unavailable (see the harness for
// the exact rules and the GOAL3_RENDER_OUT_DIR override).
package overlay

import (
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoal3_FinalClipBackgroundWatermark is the end-to-end Goal 3 proof.
func TestGoal3_FinalClipBackgroundWatermark(t *testing.T) {
	bin := chrononBinFor(t)
	outDir := goal3OutDir(t)
	assetsRoot := goal3AssetsRoot(t)
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
