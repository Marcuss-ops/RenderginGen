package overlay

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestCompositeThreeNearbyImagesRenderAllThreeStaggered renders ONE composite
// entity_image item with three image_layers (the shape PipelineGen emits for a
// group of three nearby images) through the real Chronon CLI. Each image must
// be absent before its own start and visible in its own slot afterwards.
func TestCompositeThreeNearbyImagesRenderAllThreeStaggered(t *testing.T) {
	bin := chrononBinFor(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available for composite pixel checks: %v", err)
	}
	assetsRoot := compositeRuntimeAssetsRoot(t)
	leftSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "image-left.png"))
	rightSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "image-right.png"))
	singleSHA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "image-single.png"))

	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"composite-three-image-runtime","video_id":"composite-three-image-runtime",
		"width":%d,"height":%d,"fps_num":%d,"fps_den":1,"duration_ms":%d,
		"background":{"kind":"color","color":[0.04,0.05,0.07,1]},
		"items":[{
			"id":"image-trio","kind":"entity_image","template_id":"image_popup","preset_id":"image_focus_in",
			"start_ms":0,"end_ms":%d,"duration_ms":%d,
			"asset_refs":[
				{"asset_id":"image-left","sha256":%q,"url":"https://fixtures.example/image-left.png","media_type":"image/png"},
				{"asset_id":"image-right","sha256":%q,"url":"https://fixtures.example/image-right.png","media_type":"image/png"},
				{"asset_id":"image-single","sha256":%q,"url":"https://fixtures.example/image-single.png","media_type":"image/png"}
			],
			"image_layers":[
				{"id":"image-left","asset_id":"image-left","start_ms":0,"end_ms":3000,"preset_id":"image_focus_in","motion_id":"image_focus_reveal","motion_params":{"enter_frames":8},"params":{"width":250,"height":230,"position_x":-300,"position_y":0,"fit":"contain"}},
				{"id":"image-single","asset_id":"image-single","start_ms":750,"end_ms":3000,"preset_id":"image_focus_in","motion_id":"image_fade_reveal","motion_params":{"enter_frames":8},"params":{"width":250,"height":230,"position_x":0,"position_y":0,"fit":"contain"}},
				{"id":"image-right","asset_id":"image-right","start_ms":1500,"end_ms":3000,"preset_id":"image_focus_in","motion_id":"image_scale_reveal","motion_params":{"enter_frames":8},"params":{"width":250,"height":230,"position_x":300,"position_y":0,"fit":"contain"}}
			]
		}]
	}`, compositeRuntimeWidth, compositeRuntimeHeight, compositeRuntimeFPS, compositeRuntimeEndMS,
		compositeRuntimeEndMS, compositeRuntimeEndMS, leftSHA, rightSHA, singleSHA))

	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile three-image composite: %v", err)
	}

	videoPath := filepath.Join(t.TempDir(), "composite-three-images.mp4")
	compiled.Plan.Output.Path = videoPath
	planPath := filepath.Join(t.TempDir(), "composite-three-images-plan.json")
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
		t.Fatalf("Chronon three-image render failed: %v\n%s", err, tailBytes(output))
	}
	if _, err := os.Stat(videoPath); err != nil {
		t.Fatalf("Chronon render returned success without output: %v", err)
	}
	decodeFully(t, videoPath)

	background := goal3DecodePNG(t, goal3FramePNG(t, videoPath, 0), "three-image background baseline")
	frameAtMS := func(ms int64, label string) image.Image {
		t.Helper()
		frameIndex := int(ms * compositeRuntimeFPS / 1000)
		return goal3DecodePNG(t, goal3FramePNG(t, videoPath, frameIndex), label)
	}
	// Slots: left center x=180, middle x=480, right x=780 (canvas 960 wide, y=270).
	leftSlot := image.Rect(55, 155, 305, 385)
	middleSlot := image.Rect(355, 155, 605, 385)
	rightSlot := image.Rect(655, 155, 905, 385)

	// At 300 ms only the left image has started.
	early := frameAtMS(300, "only left image started")
	if diff := goal3RegionDiffTol(t, background, early, leftSlot, 16); diff < 20_000 {
		t.Fatalf("left slot changed only %d pixels at 300ms; expected first image visible", diff)
	}
	if diff := goal3RegionDiffTol(t, background, early, middleSlot, 16); diff > 1500 {
		t.Fatalf("middle slot changed by %d pixels before its 750ms start; expected only codec noise", diff)
	}
	if diff := goal3RegionDiffTol(t, background, early, rightSlot, 16); diff > 1500 {
		t.Fatalf("right slot changed by %d pixels before its 1500ms start; expected only codec noise", diff)
	}

	// At 1000 ms the second image has started but the third has not.
	mid := frameAtMS(1000, "two images started")
	if diff := goal3RegionDiffTol(t, background, mid, middleSlot, 16); diff < 20_000 {
		t.Fatalf("middle slot changed only %d pixels at 1000ms; expected second image visible", diff)
	}
	if diff := goal3RegionDiffTol(t, background, mid, rightSlot, 16); diff > 1500 {
		t.Fatalf("right slot changed by %d pixels before its 1500ms start at 1000ms", diff)
	}

	// All three have started and settled before the exit fade (~2.5 s):
	// each image sits in its own slot with its own color.
	all := frameAtMS(2200, "all three images visible")
	for _, slot := range []image.Rectangle{leftSlot, middleSlot, rightSlot} {
		if diff := goal3RegionDiffTol(t, background, all, slot, 16); diff < 20_000 {
			t.Fatalf("slot %v changed only %d pixels when all three images should be visible", slot, diff)
		}
	}
	assertCompositeImageColor(t, all, image.Point{X: 180, Y: 270}, color.RGBA{R: 220, G: 30, B: 30, A: 255}, "left image")
	assertCompositeImageColor(t, all, image.Point{X: 480, Y: 270}, color.RGBA{R: 30, G: 190, B: 80, A: 255}, "middle image")
	assertCompositeImageColor(t, all, image.Point{X: 780, Y: 270}, color.RGBA{R: 20, G: 80, B: 230, A: 255}, "right image")
}
