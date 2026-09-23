// mixed_overlay_scenario_test.go — runtime scenario: ONE plan carrying two
// phrase overlays and two entity-image overlays, rendered through the real
// production compiler and the real chronon3d_cli, then certified pixel by
// pixel.
//
// This is the mixed-content counterpart of the per-preset certification: that
// suite proves each preset renders alone, this one proves the four overlays
// coexist in one composition and that each of them really reaches the frame —
// the failure mode a per-preset suite cannot see (an item silently dropped,
// composited once, or attributed to the wrong window).
//
// Lane and flags are the certification lane (software raster + pipe encoder):
// deterministic, no GPU required, and the lane on which static_text_smoke plus
// the image presets are certified. phrase_default (the animated text preset) is
// deliberately NOT used.
package overlay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The mixed scenario geometry. 10 s at the production 24/1 rate gives every
// item a window long enough for its preset entrance plus a settled tail, and
// leaves an overlay-free gap between windows so a residual frame can be told
// apart from a held one.
const (
	mixedWidth  = 1920
	mixedHeight = 1080
	mixedFPS    = 24
	mixedDurMS  = 10000
)

// The four items, in timeline order. Each window is disjoint from its
// neighbours; midFraction is where inside the window the presence probe
// samples (never on the entrance edge, where a preset legitimately draws
// nothing yet).
var mixedScenarioItems = []struct {
	name          string
	startMS       int64
	endMS         int64
	preset        string
	midFraction   float64
	minDiffPixels int
	centerOnly    bool
}{
	{name: "phrase_a", startMS: 400, endMS: 2000, preset: StaticTextSmokePresetID, midFraction: 0.5, minDiffPixels: 300},
	{name: "entity_a", startMS: 2600, endMS: 5200, preset: "image_scale_in", midFraction: 0.75, minDiffPixels: 20000, centerOnly: true},
	{name: "phrase_b", startMS: 5600, endMS: 7200, preset: StaticTextSmokePresetID, midFraction: 0.5, minDiffPixels: 300},
	{name: "entity_b", startMS: 7600, endMS: 9800, preset: "image_slide_left", midFraction: 0.85, minDiffPixels: 20000, centerOnly: true},
}

// gapFramesMS are frames that fall between two item windows: only the
// background plate may be visible there.
var gapFramesMS = []int64{2200, 5400, 7400}

func msToFrame(ms int64) int { return int(ms * mixedFPS / 1000) }

func midFrameFor(startMS, endMS int64, fraction float64) int {
	return msToFrame(startMS + int64(float64(endMS-startMS)*fraction))
}

// mixedScenarioAssetsRoot materializes the two entity portraits and the
// official font at exactly the logical paths the plan references.
func mixedScenarioAssetsRoot(t *testing.T) string {
	t.Helper()
	source := certificationSourceRoot(t)
	root := t.TempDir()
	copyFixture(t, filepath.Join(source, "gerard_butler.jpg"), filepath.Join(root, "assets", "semantic", "ent-alpha.jpg"))
	copyFixture(t, filepath.Join(source, "gerard_butler.jpg"), filepath.Join(root, "assets", "semantic", "ent-beta.jpg"))
	copyFixture(t, filepath.Join(source, "Poppins-Bold.ttf"), filepath.Join(root, filepath.FromSlash(officialFontPath)))
	return root
}

func copyFixture(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Skipf("fixture %s not available: %v", from, err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", to, err)
	}
}

func fixtureSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// mixedScenarioPlan builds the renderinggen.overlay-plan.v1 document the
// scenario renders: a flat background plate, two IMPORTANT_PHRASE items and
// two entity_image items.
func mixedScenarioPlan(t *testing.T, assetsRoot string) string {
	t.Helper()
	alphaSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "ent-alpha.jpg"))
	betaSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "ent-beta.jpg"))
	entity := func(id, entityID, preset, sha, window string) string {
		return fmt.Sprintf(
			`{"id":%q,"entity_id":%q,"kind":"entity_image","template_id":"image_popup","preset_id":%q,%s,`+
				`"asset_refs":[{"asset_id":%q,"sha256":%q,"url":"https://store.example/objects/%s.jpg","media_type":"image/jpeg"}]}`,
			id, entityID, preset, window, id, sha, id)
	}
	phrase := func(id, text, window string) string {
		return fmt.Sprintf(
			`{"id":%q,"kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":%q,"text":%q,%s}`,
			id, StaticTextSmokePresetID, text, window)
	}
	return fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"mixed-scenario","video_id":"mixed-scenario",`+
			`"width":%d,"height":%d,"fps_num":%d,"fps_den":1,"duration_ms":%d,`+
			`"background":{"kind":"color","color":%s},"items":[%s,%s,%s,%s]}`,
		mixedWidth, mixedHeight, mixedFPS, mixedDurMS, certificationBackgroundRGBA,
		phrase("phrase_a", "Prima frase certificata", `"start_ms":400,"end_ms":2000`),
		entity("ent-alpha", "person:alpha", "image_scale_in", alphaSHA, `"start_ms":2600,"end_ms":5200`),
		phrase("phrase_b", "Seconda frase certificata", `"start_ms":5600,"end_ms":7200`),
		entity("ent-beta", "person:beta", "image_slide_left", betaSHA, `"start_ms":7600,"end_ms":9800`),
	)
}

// mixedScenarioRender lowers the semantic document and renders it on the
// certification lane, returning the artefact path and the compiled plan.
func mixedScenarioRender(t *testing.T, bin, assetsRoot, outDir, name, raw string) (string, *Plan) {
	t.Helper()
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("%s: compile: %v", name, err)
	}
	plan := result.Plan
	videoPath := filepath.Join(outDir, name+".mp4")
	plan.Output.Path = videoPath
	planBytes, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatalf("%s: marshal plan: %v", name, err)
	}
	planPath := filepath.Join(outDir, name+"_plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
		t.Fatalf("%s: write plan: %v", name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin,
		"render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "software", "--encoder-backend", "pipe",
		"--hardware", "none", "--gpu-hot-path-mode", "auto",
		"--encode-preset", "ultrafast",
		"-o", videoPath)
	cmd.Dir = assetsRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: chronon render: %v\n%s", name, err, tailBytes(out))
	}
	if _, err := os.Stat(videoPath); err != nil {
		t.Fatalf("%s: render reported success but the artefact is missing: %v", name, err)
	}
	return videoPath, plan
}

// mixedDiffBounds returns the bounding box and count of pixels that differ
// between two frames beyond tol, so a presence probe can report WHERE an item
// drew instead of only that something changed.
func mixedDiffBounds(t *testing.T, ref, img image.Image, tol int) (image.Rectangle, int) {
	t.Helper()
	bounds := ref.Bounds()
	if img.Bounds() != bounds {
		t.Fatalf("frame geometry mismatch: %v vs %v", bounds, img.Bounds())
	}
	box := image.Rect(bounds.Max.X, bounds.Max.Y, bounds.Min.X, bounds.Min.Y)
	count := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ar, ag, ab, _ := ref.At(x, y).RGBA()
			br, bg, bb, _ := img.At(x, y).RGBA()
			if absInt(int(ar>>8)-int(br>>8)) > tol ||
				absInt(int(ag>>8)-int(bg>>8)) > tol ||
				absInt(int(ab>>8)-int(bb>>8)) > tol {
				count++
				if x < box.Min.X {
					box.Min.X = x
				}
				if y < box.Min.Y {
					box.Min.Y = y
				}
				if x+1 > box.Max.X {
					box.Max.X = x + 1
				}
				if y+1 > box.Max.Y {
					box.Max.Y = y + 1
				}
			}
		}
	}
	if count == 0 {
		return image.Rectangle{}, 0
	}
	return box, count
}

// TestScenario_MixedPhrasesAndEntityImages is the mixed-content runtime proof:
// two phrases and two entity images in one plan, each drawn in its own window,
// with the background untouched between windows.
func TestScenario_MixedPhrasesAndEntityImages(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := mixedScenarioAssetsRoot(t)
	outDir := t.TempDir()

	videoPath, plan := mixedScenarioRender(t, bin, assetsRoot, outDir, "mixed-scenario", mixedScenarioPlan(t, assetsRoot))

	// ── What the compiler actually emitted ───────────────────────────────
	text, img, background, phraseItems, entityItems := 0, 0, 0, 0, 0
	for _, layer := range plan.Layers {
		switch layer.Type {
		case "text":
			text++
		case "image":
			img++
		}
		if layer.ID == "background" {
			background++
		}
	}
	for _, item := range mixedScenarioItems {
		if item.centerOnly {
			entityItems++
		} else {
			phraseItems++
		}
	}
	if text != phraseItems || img != entityItems {
		t.Fatalf("compiled layers: text=%d image=%d, want %d/%d (declared items: %+v)",
			text, img, phraseItems, entityItems, plan.Layers)
	}
	if background != 1 {
		t.Fatalf("compiled plan has %d background layers, want exactly 1", background)
	}
	// Entity images are centered portraits: the compiler owns that geometry,
	// so the presence probe can sample a canvas-centred box.
	for _, layer := range plan.Layers {
		if layer.Type != "image" {
			continue
		}
		if len(layer.Position) != 2 || layer.Position[0] != 0 || layer.Position[1] != 0 {
			t.Errorf("entity image %q is not centered: position=%v", layer.ID, layer.Position)
		}
	}

	// ── Structural contract ──────────────────────────────────────────────
	probe := goal3ProbeClip(t, videoPath)
	if probe.width != mixedWidth || probe.height != mixedHeight {
		t.Errorf("geometry %dx%d, want %dx%d", probe.width, probe.height, mixedWidth, mixedHeight)
	}
	if probe.videoFPS != "24/1" {
		t.Errorf("fps %q, want 24/1", probe.videoFPS)
	}
	if want := int64(mixedDurMS * mixedFPS / 1000); probe.videoFrames < want {
		t.Errorf("frames %d, want >= %d", probe.videoFrames, want)
	}

	// ── Background-only reference and the overlay-free gaps ──────────────
	ref := goal3DecodePNG(t, goal3FramePNG(t, videoPath, 0), "background-only")
	for _, ms := range gapFramesMS {
		gap := goal3DecodePNG(t, goal3FramePNG(t, videoPath, msToFrame(ms)), fmt.Sprintf("gap@%dms", ms))
		if different := goal3RegionDiffTol(t, ref, gap, image.Rect(0, 0, mixedWidth, mixedHeight), 16); different != 0 {
			t.Errorf("frame %d (%d ms) differs from the background-only frame in %d px: an overlay leaked past its window",
				msToFrame(ms), ms, different)
		}
	}

	// ── Every item really drew in its own window ─────────────────────────
	center := image.Rect(mixedWidth/2-330, mixedHeight/2-330, mixedWidth/2+330, mixedHeight/2+330)
	for _, item := range mixedScenarioItems {
		t.Run(item.name, func(t *testing.T) {
			frame := midFrameFor(item.startMS, item.endMS, item.midFraction)
			img := goal3DecodePNG(t, goal3FramePNG(t, videoPath, frame), item.name)
			region := image.Rect(0, 0, mixedWidth, mixedHeight)
			if item.centerOnly {
				region = center
			}
			different := goal3RegionDiffTol(t, ref, img, region, 16)
			if different < item.minDiffPixels {
				t.Errorf("%s (preset %s): only %d px changed in %v at frame %d — the overlay is not visibly drawn",
					item.name, item.preset, different, region, frame)
				return
			}
			bounds, count := mixedDiffBounds(t, ref, img, 16)
			t.Logf("%s: preset=%s frame=%d changed=%d bbox=%v", item.name, item.preset, frame, count, bounds)
			if bounds.Empty() {
				t.Errorf("%s: reported %d changed px but an empty bounding box", item.name, different)
			}
			if bounds.Min.X < 0 || bounds.Min.Y < 0 || bounds.Max.X > mixedWidth || bounds.Max.Y > mixedHeight {
				t.Errorf("%s: ink bbox %v escapes the canvas", item.name, bounds)
			}
		})
	}

	// ── The four windows really produced four different pictures ─────────
	// The probe samples EARLY in each window (25% in), not at the settled mid:
	// a settled portrait is the same picture whatever preset brought it in, so
	// the entrance is the part that identifies which preset actually ran.
	const entryFraction = 0.25
	for i := 0; i < len(mixedScenarioItems); i++ {
		for j := i + 1; j < len(mixedScenarioItems); j++ {
			a := mixedScenarioItems[i]
			b := mixedScenarioItems[j]
			fa := goal3DecodePNG(t, goal3FramePNG(t, videoPath, midFrameFor(a.startMS, a.endMS, entryFraction)), a.name+"-entry")
			fb := goal3DecodePNG(t, goal3FramePNG(t, videoPath, midFrameFor(b.startMS, b.endMS, entryFraction)), b.name+"-entry")
			if diff := goal3RegionDiffTol(t, fa, fb, image.Rect(0, 0, mixedWidth, mixedHeight), 16); diff == 0 {
				t.Errorf("entrance frames for %s and %s are identical: two items rendered the same picture", a.name, b.name)
			}
		}
	}
}
