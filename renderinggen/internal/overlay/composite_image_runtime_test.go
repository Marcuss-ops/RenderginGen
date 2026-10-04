package overlay

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	compositeRuntimeWidth  = 960
	compositeRuntimeHeight = 540
	compositeRuntimeFPS    = 12
	compositeRuntimeEndMS  = 3000
)

// TestCompositeEntityImagesRenderDistinctAssetsSideBySideWithStaggeredEntrances
// is an opt-in end-to-end test through CompileSemantic and the real Chronon CLI.
// It proves two different image assets render in their assigned canvas halves,
// and that the later image is absent before its own start time, then visible.
func TestCompositeEntityImagesRenderDistinctAssetsSideBySideWithStaggeredEntrances(t *testing.T) {
	bin := chrononBinFor(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available for composite pixel checks: %v", err)
	}
	assetsRoot, compiled := compileDistinctCompositePlan(t)

	videoPath := filepath.Join(t.TempDir(), "composite-images.mp4")
	compiled.Plan.Output.Path = videoPath
	planPath := filepath.Join(t.TempDir(), "composite-images-plan.json")
	planBytes, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatalf("marshal Chronon plan: %v", err)
	}
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatalf("write Chronon plan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin,
		"render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "software", "--encoder-backend", "pipe",
		"--hardware", "none", "--gpu-hot-path-mode", "auto",
		"--encode-preset", "ultrafast", "-o", videoPath)
	cmd.Dir = assetsRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Chronon composite render failed: %v\n%s", err, tailBytes(output))
	}
	if _, err := os.Stat(videoPath); err != nil {
		t.Fatalf("Chronon render returned success without output: %v", err)
	}
	decodeFully(t, videoPath)

	background := goal3DecodePNG(t, goal3FramePNG(t, videoPath, 0), "composite background baseline")
	leftHalf := image.Rect(0, 0, compositeRuntimeWidth/2, compositeRuntimeHeight)
	rightImageRegion := image.Rect(600, 0, compositeRuntimeWidth, compositeRuntimeHeight)
	frameAtMS := func(ms int64, label string) image.Image {
		t.Helper()
		frameIndex := int(ms * compositeRuntimeFPS / 1000)
		return goal3DecodePNG(t, goal3FramePNG(t, videoPath, frameIndex), label)
	}
	beforeSecondMention := frameAtMS(300, "left image before second mention")
	if diff := goal3RegionDiffTol(t, background, beforeSecondMention, leftHalf, 16); diff < 20_000 {
		t.Fatalf("left image changed only %d pixels before second mention; expected visible first image", diff)
	}
	if diff := goal3RegionDiffTol(t, background, beforeSecondMention, rightImageRegion, 16); diff != 0 {
		t.Fatalf("right image region changed by %d pixels before its 750ms activation; the second image is not staggered", diff)
	}

	bothVisible := frameAtMS(1300, "both distinct composite images visible")
	if diff := goal3RegionDiffTol(t, background, bothVisible, leftHalf, 16); diff < 20_000 {
		t.Fatalf("left half changed only %d pixels when both images should be visible", diff)
	}
	if diff := goal3RegionDiffTol(t, background, bothVisible, rightImageRegion, 16); diff < 20_000 {
		t.Fatalf("right image region changed only %d pixels after its mention; second image was lost or misplaced", diff)
	}
	assertCompositeImageColor(t, bothVisible, image.Point{X: compositeRuntimeWidth/2 - 230, Y: compositeRuntimeHeight / 2}, color.RGBA{R: 220, G: 30, B: 30, A: 255}, "left image")
	assertCompositeImageColor(t, bothVisible, image.Point{X: compositeRuntimeWidth/2 + 230, Y: compositeRuntimeHeight / 2}, color.RGBA{R: 20, G: 80, B: 230, A: 255}, "right image")
	assertBrightCaptionPixels(t, bothVisible, image.Rect(100, 390, 400, 510), "left entity name")
	assertBrightCaptionPixels(t, bothVisible, image.Rect(560, 390, 860, 510), "right entity name")
	assertBrightCaptionPixels(t, bothVisible, image.Rect(380, 190, 580, 285), "single-image entity name")
}

func assertBrightCaptionPixels(t *testing.T, frame image.Image, region image.Rectangle, label string) {
	t.Helper()
	bright := 0
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			r, g, b, _ := frame.At(x, y).RGBA()
			if r > 0xA000 && g > 0xA000 && b > 0xA000 {
				bright++
			}
		}
	}
	if bright < 20 {
		t.Fatalf("%s has only %d bright pixels in caption area %v; expected rendered text", label, bright, region)
	}
}

func compositeRuntimeAssetsRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, fixture := range []struct {
		name  string
		color color.RGBA
	}{
		{name: "image-left.png", color: color.RGBA{R: 220, G: 30, B: 30, A: 255}},
		{name: "image-right.png", color: color.RGBA{R: 20, G: 80, B: 230, A: 255}},
		{name: "image-single.png", color: color.RGBA{R: 30, G: 190, B: 80, A: 255}},
	} {
		path := filepath.Join(root, "assets", "semantic", fixture.name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create composite fixture directory: %v", err)
		}
		imageData := image.NewRGBA(image.Rect(0, 0, 256, 256))
		for y := 0; y < imageData.Bounds().Dy(); y++ {
			for x := 0; x < imageData.Bounds().Dx(); x++ {
				imageData.SetRGBA(x, y, fixture.color)
			}
		}
		file, err := os.Create(path)
		if err != nil {
			t.Fatalf("create composite fixture %s: %v", fixture.name, err)
		}
		if err := png.Encode(file, imageData); err != nil {
			_ = file.Close()
			t.Fatalf("encode composite fixture %s: %v", fixture.name, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("close composite fixture %s: %v", fixture.name, err)
		}
	}
	// Entity captions use the official preset font. The worker normally stages
	// this bundled font before invoking Chronon; this direct CLI runtime test
	// mounts its own asset root, so stage the same font into the fixture.
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve runtime test source path for official font fixture")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	fontSource := filepath.Join(repoRoot, "renderinggen", "out", "editorial_v1", "assets", "fonts", "Poppins-Bold.ttf")
	fontBytes, err := os.ReadFile(fontSource)
	if err != nil {
		t.Fatalf("read official caption font fixture: %v", err)
	}
	fontTarget := filepath.Join(root, "assets", "fonts", "Poppins-Bold.ttf")
	if err := os.MkdirAll(filepath.Dir(fontTarget), 0o755); err != nil {
		t.Fatalf("create caption font fixture directory: %v", err)
	}
	if err := os.WriteFile(fontTarget, fontBytes, 0o600); err != nil {
		t.Fatalf("stage caption font fixture: %v", err)
	}
	return root
}

func assertCompositeImageColor(t *testing.T, frame image.Image, point image.Point, want color.RGBA, label string) {
	t.Helper()
	gotColor := color.RGBAModel.Convert(frame.At(point.X, point.Y)).(color.RGBA)
	const channelTolerance = 35
	if absInt(int(gotColor.R)-int(want.R)) > channelTolerance ||
		absInt(int(gotColor.G)-int(want.G)) > channelTolerance ||
		absInt(int(gotColor.B)-int(want.B)) > channelTolerance {
		t.Fatalf("%s center pixel at %v = %v, want source color near %v (±%d/channel)", label, point, gotColor, want, channelTolerance)
	}
}

// TestCompositeSemanticCompilerPreservesDistinctAssetsAndStaggeredWindows is
// the always-on compile-level half of the runtime test. It checks independent
// assets, timing, placement and motion tracks even when Chronon is unavailable.
func TestCompositeSemanticCompilerPreservesDistinctAssetsAndStaggeredWindows(t *testing.T) {
	_, compiled := compileDistinctCompositePlan(t)
	if len(compiled.Plan.Layers) != 7 {
		t.Fatalf("compiled layers=%d, want background, three images and three captions", len(compiled.Plan.Layers))
	}
	captionCount := 0
	captionMotions := map[string]bool{}
	captionAnimations := map[string]*LayerAnimation{}
	for _, layer := range compiled.Plan.Layers {
		if layer.Type == "text" && (layer.Text == "Ada Lovelace" || layer.Text == "Grace Hopper" || layer.Text == "Single Entity") {
			captionCount++
			if layer.Animation == nil || len(layer.Animation.Tracks) == 0 {
				t.Errorf("caption %q has no compiled motion tracks", layer.Text)
			}
			captionMotions[layer.Text] = true
			captionAnimations[layer.Text] = layer.Animation
		}
	}
	if captionCount != 3 {
		t.Fatalf("compiled entity captions=%d, want Ada Lovelace, Grace Hopper and Single Entity", captionCount)
	}
	if !captionMotions["Ada Lovelace"] || !captionMotions["Grace Hopper"] || !captionMotions["Single Entity"] {
		t.Fatalf("entity caption motion was not lowered for every caption: %+v", captionMotions)
	}
	if captionAnimations["Ada Lovelace"].Tracks[0].Property != "position_y" || captionAnimations["Grace Hopper"].Tracks[0].Property != "position_y" {
		t.Fatalf("composite caption-specific motions were not preserved: Ada=%+v Grace=%+v", captionAnimations["Ada Lovelace"], captionAnimations["Grace Hopper"])
	}
}

func compileDistinctCompositePlan(t *testing.T) (string, CompileResult) {
	t.Helper()
	assetsRoot := compositeRuntimeAssetsRoot(t)
	leftSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "image-left.png"))
	rightSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "image-right.png"))
	singleSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "image-single.png"))
	if leftSHA == rightSHA {
		t.Fatal("composite fixture assets must contain different image bytes")
	}
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"composite-image-runtime","video_id":"composite-image-runtime",
		"width":%d,"height":%d,"fps_num":%d,"fps_den":1,"duration_ms":%d,
		"background":{"kind":"color","color":[0.04,0.05,0.07,1]},
		"items":[{
			"id":"image-pair","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in",
			"start_ms":0,"end_ms":%d,"duration_ms":%d,
			"asset_refs":[
				{"asset_id":"image-left","sha256":%q,"url":"https://fixtures.example/image-left.png","media_type":"image/png"},
				{"asset_id":"image-right","sha256":%q,"url":"https://fixtures.example/image-right.png","media_type":"image/png"}
			],
			"image_layers":[
				{"id":"image-left","asset_id":"image-left","start_ms":0,"end_ms":2000,"preset_id":"image_focus_in","motion_id":"image_focus_reveal","motion_params":{"enter_frames":8},"caption":"Ada Lovelace","caption_motion_id":"text_word_rise","params":{"width":250,"height":230,"position_x":-230,"position_y":0,"fit":"contain"}},
				{"id":"image-right","asset_id":"image-right","start_ms":750,"end_ms":3000,"preset_id":"image_focus_in","motion_id":"image_scale_reveal","motion_params":{"enter_frames":8},"caption":"Grace Hopper","caption_motion_id":"text_word_stagger","params":{"width":250,"height":230,"position_x":230,"position_y":0,"fit":"contain"}}
			]
		},{
			"id":"single-image","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in",
			"start_ms":0,"end_ms":%d,"duration_ms":%d,"entity_caption":"Single Entity",
			"asset_refs":[{"asset_id":"image-single","sha256":%q,"url":"https://fixtures.example/image-single.png","media_type":"image/png"}],
			"params":{"width":160,"height":140,"position_x":0,"position_y":-150,"fit":"contain"}
		}]
	}`, compositeRuntimeWidth, compositeRuntimeHeight, compositeRuntimeFPS, compositeRuntimeEndMS, compositeRuntimeEndMS, compositeRuntimeEndMS, leftSHA, rightSHA, compositeRuntimeEndMS, compositeRuntimeEndMS, singleSHA))
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile composite semantic plan: %v", err)
	}
	if len(compiled.Plan.Layers) != 7 {
		t.Fatalf("composite compiled to %d layers, want background, three images and three captions: %+v", len(compiled.Plan.Layers), compiled.Plan.Layers)
	}
	var left, right Layer
	for _, layer := range compiled.Plan.Layers {
		if layer.Type != "image" {
			continue
		}
		if layer.Asset == "assets/semantic/image-left.png" && strings.HasPrefix(layer.ID, "image-pair:") {
			left = layer
		} else if layer.Asset == "assets/semantic/image-right.png" {
			right = layer
		}
	}
	if left.Asset != "assets/semantic/image-left.png" || right.Asset != "assets/semantic/image-right.png" {
		t.Fatalf("compiled assets = %q / %q", left.Asset, right.Asset)
	}
	if left.StartFrame != 0 || right.StartFrame != compositeRuntimeFPS*750/1000 {
		t.Fatalf("composite child starts = %d/%d, want 0/%d", left.StartFrame, right.StartFrame, compositeRuntimeFPS*750/1000)
	}
	if len(left.Position) != 2 || left.Position[0] != -230 || len(right.Position) != 2 || right.Position[0] != 230 {
		t.Fatalf("composite child positions = %v / %v", left.Position, right.Position)
	}
	if left.Animation == nil || right.Animation == nil || len(left.Animation.Tracks) == 0 || len(right.Animation.Tracks) == 0 {
		t.Fatalf("composite children did not retain animation tracks: %+v / %+v", left.Animation, right.Animation)
	}
	if left.Animation.Tracks[0].Property == right.Animation.Tracks[0].Property && left.Animation.Tracks[0].Keyframes[0].Value == right.Animation.Tracks[0].Keyframes[0].Value {
		t.Fatalf("composite children did not retain distinct motion tracks: %+v / %+v", left.Animation, right.Animation)
	}
	return assetsRoot, compiled
}
