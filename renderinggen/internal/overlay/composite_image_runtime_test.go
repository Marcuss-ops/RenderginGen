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
	"testing"
	"time"
)

const (
	compositeRuntimeWidth  = 1920
	compositeRuntimeHeight = 1080
	compositeRuntimeFPS    = 24
	compositeRuntimeEndMS  = 6500
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
	probeMP4Structural(t, videoPath)
	decodeFully(t, videoPath)

	background := goal3DecodePNG(t, goal3FramePNG(t, videoPath, 0), "composite background baseline")
	leftHalf := image.Rect(0, 0, compositeRuntimeWidth/2, compositeRuntimeHeight)
	rightHalf := image.Rect(compositeRuntimeWidth/2, 0, compositeRuntimeWidth, compositeRuntimeHeight)
	frameAtMS := func(ms int64, label string) image.Image {
		t.Helper()
		frameIndex := int(ms * compositeRuntimeFPS / 1000)
		return goal3DecodePNG(t, goal3FramePNG(t, videoPath, frameIndex), label)
	}
	beforeSecondMention := frameAtMS(1000, "left image before second mention")
	if diff := goal3RegionDiffTol(t, background, beforeSecondMention, leftHalf, 16); diff < 20_000 {
		t.Fatalf("left image changed only %d pixels before second mention; expected visible first image", diff)
	}
	if diff := goal3RegionDiffTol(t, background, beforeSecondMention, rightHalf, 16); diff != 0 {
		t.Fatalf("right half changed by %d pixels before its 1500ms activation; the second image is not staggered", diff)
	}

	bothVisible := frameAtMS(2500, "both distinct composite images visible")
	if diff := goal3RegionDiffTol(t, background, bothVisible, leftHalf, 16); diff < 20_000 {
		t.Fatalf("left half changed only %d pixels when both images should be visible", diff)
	}
	if diff := goal3RegionDiffTol(t, background, bothVisible, rightHalf, 16); diff < 20_000 {
		t.Fatalf("right half changed only %d pixels after its mention; second image was lost or misplaced", diff)
	}
	assertCompositeImageColor(t, bothVisible, image.Point{X: compositeRuntimeWidth/2 - 460, Y: compositeRuntimeHeight / 2}, color.RGBA{R: 220, G: 30, B: 30, A: 255}, "left image")
	assertCompositeImageColor(t, bothVisible, image.Point{X: compositeRuntimeWidth/2 + 460, Y: compositeRuntimeHeight / 2}, color.RGBA{R: 20, G: 80, B: 230, A: 255}, "right image")
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
	if len(compiled.Plan.Layers) != 3 {
		t.Fatalf("compiled layers=%d, want background plus two independent image layers", len(compiled.Plan.Layers))
	}
}

func compileDistinctCompositePlan(t *testing.T) (string, CompileResult) {
	t.Helper()
	assetsRoot := compositeRuntimeAssetsRoot(t)
	leftSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "image-left.png"))
	rightSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "image-right.png"))
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
				{"id":"image-left","asset_id":"image-left","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","motion_id":"image_focus_reveal","motion_params":{"enter_frames":12},"params":{"width":422,"height":453,"position_x":-460.8,"position_y":0,"fit":"contain"}},
				{"id":"image-right","asset_id":"image-right","start_ms":1500,"end_ms":6500,"preset_id":"image_focus_in","motion_id":"image_scale_reveal","motion_params":{"enter_frames":12},"params":{"width":422,"height":453,"position_x":460.8,"position_y":0,"fit":"contain"}}
			]
		}]
	}`, compositeRuntimeWidth, compositeRuntimeHeight, compositeRuntimeFPS, compositeRuntimeEndMS, compositeRuntimeEndMS, compositeRuntimeEndMS, leftSHA, rightSHA))
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile composite semantic plan: %v", err)
	}
	if len(compiled.Plan.Layers) != 3 {
		t.Fatalf("composite compiled to %d layers, want background plus two image layers: %+v", len(compiled.Plan.Layers), compiled.Plan.Layers)
	}
	left, right := compiled.Plan.Layers[1], compiled.Plan.Layers[2]
	if left.Asset != "assets/semantic/image-left.png" || right.Asset != "assets/semantic/image-right.png" {
		t.Fatalf("compiled assets = %q / %q", left.Asset, right.Asset)
	}
	if left.StartFrame != 0 || right.StartFrame != compositeRuntimeFPS*1500/1000 {
		t.Fatalf("composite child starts = %d/%d, want 0/%d", left.StartFrame, right.StartFrame, compositeRuntimeFPS*1500/1000)
	}
	if len(left.Position) != 2 || left.Position[0] != -460.8 || len(right.Position) != 2 || right.Position[0] != 460.8 {
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
