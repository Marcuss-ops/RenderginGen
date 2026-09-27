package overlay

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
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

// TestCompositeEntityImagesRenderSideBySideWithStaggeredEntrances is an
// opt-in end-to-end test through CompileSemantic and the real Chronon CLI. It
// proves both portrait assets render in their assigned canvas half and that
// the later portrait is absent before its own start time, then visible after.
func TestCompositeEntityImagesRenderSideBySideWithStaggeredEntrances(t *testing.T) {
	bin := chrononBinFor(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available for composite pixel checks: %v", err)
	}
	assetsRoot := compositeRuntimeAssetsRoot(t)
	leftSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "portrait-left.jpg"))
	rightSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "portrait-right.jpg"))
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"composite-portrait-runtime","video_id":"composite-portrait-runtime",
		"width":%d,"height":%d,"fps_num":%d,"fps_den":1,"duration_ms":%d,
		"background":{"kind":"color","color":[0.04,0.05,0.07,1]},
		"items":[{
			"id":"portrait-pair","kind":"entity_image","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in",
			"start_ms":0,"end_ms":%d,
			"asset_refs":[
				{"asset_id":"portrait-left","sha256":%q,"url":"https://fixtures.example/portrait-left.jpg","media_type":"image/jpeg"},
				{"asset_id":"portrait-right","sha256":%q,"url":"https://fixtures.example/portrait-right.jpg","media_type":"image/jpeg"}
			],
			"image_layers":[
				{"id":"portrait-left","asset_id":"portrait-left","start_ms":0,"end_ms":5000,"preset_id":"image_focus_in","motion_id":"image_focus_reveal","motion_params":{"enter_frames":12},"params":{"width":422,"height":453,"position_x":-460.8,"position_y":0,"fit":"contain"}},
				{"id":"portrait-right","asset_id":"portrait-right","start_ms":1500,"end_ms":6500,"preset_id":"image_focus_in","motion_id":"image_scale_reveal","motion_params":{"enter_frames":12},"params":{"width":422,"height":453,"position_x":460.8,"position_y":0,"fit":"contain"}}
			]
		}]
	}`, compositeRuntimeWidth, compositeRuntimeHeight, compositeRuntimeFPS, compositeRuntimeEndMS, compositeRuntimeEndMS, leftSHA, rightSHA))

	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile composite semantic plan: %v", err)
	}
	if len(compiled.Plan.Layers) != 2 {
		t.Fatalf("composite compiled to %d layers, want 2: %+v", len(compiled.Plan.Layers), compiled.Plan.Layers)
	}
	left, right := compiled.Plan.Layers[0], compiled.Plan.Layers[1]
	if left.Asset != "assets/semantic/portrait-left.jpg" || right.Asset != "assets/semantic/portrait-right.jpg" {
		t.Fatalf("compiled assets = %q / %q", left.Asset, right.Asset)
	}
	if left.StartFrame != 0 || right.StartFrame != compositeRuntimeFPS*1500/1000 {
		t.Fatalf("composite child starts = %d/%d, want 0/%d", left.StartFrame, right.StartFrame, compositeRuntimeFPS*1500/1000)
	}
	if len(left.Position) != 2 || left.Position[0] != -460.8 || len(right.Position) != 2 || right.Position[0] != 460.8 {
		t.Fatalf("composite child positions = %v / %v", left.Position, right.Position)
	}
	if left.Animation == nil || right.Animation == nil || left.Animation.Tracks[0].Property == right.Animation.Tracks[0].Property && left.Animation.Tracks[0].Keyframes[0].Value == right.Animation.Tracks[0].Keyframes[0].Value {
		t.Fatalf("composite children did not retain distinct motion tracks: %+v / %+v", left.Animation, right.Animation)
	}

	videoPath := filepath.Join(t.TempDir(), "composite-portraits.mp4")
	compiled.Plan.Output.Path = videoPath
	planPath := filepath.Join(t.TempDir(), "composite-portraits-plan.json")
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
	beforeSecondMention := frameAtMS(1000, "left portrait before second mention")
	if diff := goal3RegionDiffTol(t, background, beforeSecondMention, leftHalf, 16); diff < 20_000 {
		t.Fatalf("left portrait changed only %d pixels before second mention; expected visible first portrait", diff)
	}
	if diff := goal3RegionDiffTol(t, background, beforeSecondMention, rightHalf, 16); diff != 0 {
		t.Fatalf("right half changed by %d pixels before its 1500ms activation; the second portrait is not staggered", diff)
	}

	bothVisible := frameAtMS(2500, "both composite portraits visible")
	if diff := goal3RegionDiffTol(t, background, bothVisible, leftHalf, 16); diff < 20_000 {
		t.Fatalf("left half changed only %d pixels when both portraits should be visible", diff)
	}
	if diff := goal3RegionDiffTol(t, background, bothVisible, rightHalf, 16); diff < 20_000 {
		t.Fatalf("right half changed only %d pixels after its mention; second portrait was lost or misplaced", diff)
	}
}

func compositeRuntimeAssetsRoot(t *testing.T) string {
	t.Helper()
	source := certificationSourceRoot(t)
	root := t.TempDir()
	for _, name := range []string{"portrait-left.jpg", "portrait-right.jpg"} {
		copyFixture(t,
			filepath.Join(source, "gerard_butler.jpg"),
			filepath.Join(root, "assets", "semantic", name))
	}
	return root
}
