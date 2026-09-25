package overlay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPhraseSlideMotionChangesRenderedPixels is an opt-in engine test. It
// checks two frames during a 16-frame entrance at 24 fps, so a structurally
// valid but visually static motion cannot pass merely because tracks exist.
func TestPhraseSlideMotionChangesRenderedPixels(t *testing.T) {
	bin := chrononBinFor(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available for pixel check: %v", err)
	}
	assetsRoot := certificationAssetsRoot(t)
	raw := []byte(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"phrase-slide-visibility","video_id":"v","width":640,"height":360,"fps_num":24,"fps_den":1,"background":{"kind":"color","color":[0.08,0.08,0.08,1]},"items":[{"id":"phrase","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default","motion_id":"phrase_apple_clean_07_slide_up_soft","motion_params":{"enter_frames":16},"text":"MOTION VISIBILITY","start_ms":0,"end_ms":1500}]}`)
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile phrase motion plan: %v", err)
	}
	outDir := t.TempDir()
	videoPath := filepath.Join(outDir, "phrase-motion.mp4")
	result.Plan.Output.Path = videoPath
	planPath := filepath.Join(outDir, "plan.json")
	planBytes, err := json.Marshal(result.Plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "software", "--encoder-backend", "pipe", "--hardware", "none",
		"--gpu-hot-path-mode", "auto", "--encode-preset", "ultrafast", "-o", videoPath)
	cmd.Dir = assetsRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render phrase motion: %v\n%s", err, tailBytes(out))
	}
	first := phraseMotionFrame(t, videoPath, 8)
	second := phraseMotionFrame(t, videoPath, 14)
	changed := 0
	for i := range first {
		if absByteDiff(first[i], second[i]) > 18 {
			changed++
		}
	}
	if changed < 300 {
		t.Fatalf("slide entrance changed only %d RGB channels between frames 8 and 14; want at least 300 to prove visible motion", changed)
	}
}

// TestImage25DMotionChangesRenderedPixels exercises the image path through a
// real Chronon render, including the 3D routing flag and measurable frame
// changes from a catalog-certified 2.5D motion.
func TestImage25DMotionChangesRenderedPixels(t *testing.T) {
	bin := chrononBinFor(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available for pixel check: %v", err)
	}
	assetsRoot := certificationAssetsRoot(t)
	raw := []byte(fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"image-25d-visibility","video_id":"v","width":640,"height":360,"fps_num":24,"fps_den":1,"background":{"kind":"color","color":[0.08,0.08,0.08,1]},"items":[{"id":"image","kind":"image","template_id":"PRODUCT","preset_id":%q,"motion_id":"image_25d_yaw_flip_in","motion_params":{"enter_frames":16},"start_ms":0,"end_ms":3000,"params":{"position":"center"},"asset_refs":[{"asset_id":%q,"sha256":%q,"url":"https://example.test/certification.jpg","media_type":"image/jpeg"}]}]}`, ImageMotionCorpusPresetID, certificationAssetID, certificationAssetSHA))
	result, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile image motion plan: %v", err)
	}
	if len(result.Plan.Layers) < 2 {
		t.Fatalf("compiled layers = %d, want background and image", len(result.Plan.Layers))
	}
	var imageLayer *Layer
	for i := range result.Plan.Layers {
		if result.Plan.Layers[i].Type == "image" {
			imageLayer = &result.Plan.Layers[i]
			break
		}
	}
	if imageLayer == nil || !imageLayer.Enable3D {
		t.Fatalf("image motion did not enable 3D routing: %+v", imageLayer)
	}
	if imageLayer.Animation == nil || len(imageLayer.Animation.Tracks) == 0 {
		t.Fatalf("image motion has no compiled tracks: %+v", imageLayer)
	}
	outDir := t.TempDir()
	videoPath := filepath.Join(outDir, "image-motion.mp4")
	result.Plan.Output.Path = videoPath
	planPath := filepath.Join(outDir, "plan.json")
	planBytes, err := json.Marshal(result.Plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "vulkan", "--encoder-backend", "native", "--hardware", "nvenc",
		"--gpu-hot-path-mode", "require_gpu_native", "--encode-preset", "p1",
		"--rate-control", "qp", "--qp", "23", "-o", videoPath)
	cmd.Dir = assetsRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render 2.5D image motion: %v\n%s", err, tailBytes(out))
	}
	base := phraseMotionFrame(t, videoPath, 0)
	settled := phraseMotionFrame(t, videoPath, 40)
	if changed := changedRGBChannels(base, settled); changed < 300 {
		t.Fatalf("2.5D render changed only %d RGB channels from frame 0 to settled frame 40; want at least 300", changed)
	}
	first := phraseMotionFrame(t, videoPath, 4)
	second := phraseMotionFrame(t, videoPath, 12)
	if changed := changedRGBChannels(first, second); changed < 300 {
		t.Fatalf("2.5D entrance changed only %d RGB channels between visible frames 4 and 12; want at least 300", changed)
	}
}

func phraseMotionFrame(t *testing.T, path string, frame int) []byte {
	t.Helper()
	filter := fmt.Sprintf("select=eq(n\\,%d)", frame)
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-vf", filter,
		"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatalf("decode frame %d: %v", frame, err)
	}
	if len(out) != 640*360*3 {
		t.Fatalf("decoded frame %d has %d RGB bytes, want %d", frame, len(out), 640*360*3)
	}
	return out
}

func absByteDiff(a, b byte) int {
	d := int(a) - int(b)
	if d < 0 {
		return -d
	}
	return d
}

func changedRGBChannels(a, b []byte) int {
	changed := 0
	for i := range a {
		if absByteDiff(a[i], b[i]) > 18 {
			changed++
		}
	}
	return changed
}
