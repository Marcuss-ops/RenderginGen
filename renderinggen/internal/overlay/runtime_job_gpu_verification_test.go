package overlay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRuntimeJobCompositeExecutionOnStrictVulkanGPU executes an end-to-end
// multi-overlay composite job in runtime on the strict Vulkan GPU + native NVENC
// pipeline. It tests across multiple motion families on one continuous timeline.
func TestRuntimeJobCompositeExecutionOnStrictVulkanGPU(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)

	imageBytes := motionCanaryJPEG(t, color.RGBA{R: 32, G: 160, B: 220, A: 255})
	if err := os.MkdirAll(filepath.Join(assetsRoot, "assets", "semantic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsRoot, "assets", "semantic", certificationAssetID+".jpg"), imageBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	imageDigest := sha256.Sum256(imageBytes)
	imageSHA := hex.EncodeToString(imageDigest[:])

	// 5 distinct overlays sequenced over 10 seconds (240 frames @ 24fps)
	items := []any{
		// 1. Apple Clean Phrase (0 - 2000ms)
		map[string]any{
			"id": "phrase-apple-01", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
			"preset_id": PhraseDefaultPresetID, "motion_id": "phrase_apple_clean_07_slide_up_soft",
			"motion_params": map[string]any{"enter_frames": 24},
			"text":          "VELOX EDITING GPU RUNTIME", "start_ms": 0, "end_ms": 2000,
		},
		// 2. Short Phrase Style with Cascade (2000 - 4000ms)
		map[string]any{
			"id": "short-phrase-cascade", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
			"preset_id": PhraseDefaultPresetID, "motion_id": "short_phrase_character_cascade_shapes",
			"motion_params": map[string]any{"enter_frames": 24},
			"text":          "CHARACTER CASCADE ACTIVE", "start_ms": 2000, "end_ms": 4000,
		},
		// 3. Editorial Image (4000 - 6000ms)
		map[string]any{
			"id": "editorial-image-lift", "kind": "image", "template_id": "IMAGE_OVERLAY",
			"preset_id": ImageMotionCorpusPresetID, "motion_id": "image_depth_dolly",
			"start_ms": 4000, "end_ms": 6000,
			"entity_caption": "GPU Editorial Image",
			"asset_refs": []map[string]any{{
				"asset_id": certificationAssetID, "sha256": imageSHA,
				"url": "assets/semantic/" + certificationAssetID + ".jpg", "media_type": "image/jpeg",
			}},
		},
		// 4. Brush Accent (6000 - 8000ms)
		map[string]any{
			"id": "brush-arrow", "kind": "image", "template_id": "IMAGE_OVERLAY",
			"preset_id": ImageMotionCorpusPresetID, "motion_id": "brush_arrow_point",
			"start_ms": 6000, "end_ms": 8000,
			"entity_caption": "GPU Brush Accent",
			"asset_refs": []map[string]any{{
				"asset_id": certificationAssetID, "sha256": imageSHA,
				"url": "assets/semantic/" + certificationAssetID + ".jpg", "media_type": "image/jpeg",
			}},
		},
		// 5. Text 3D Push (8000 - 10000ms)
		map[string]any{
			"id": "text-3d-push", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
			"preset_id": PhraseDefaultPresetID, "motion_id": "text_3d_camera_push",
			"motion_params": map[string]any{"enter_frames": 24},
			"text":          "3D CAMERA GPU PIPELINE", "start_ms": 8000, "end_ms": 10000,
		},
	}

	semanticDocument := map[string]any{
		"schema_version": SemanticSchema,
		"plan_id":        "runtime-job-gpu-composite",
		"video_id":       "runtime-job-composite",
		"width":          1280,
		"height":         720,
		"fps_num":        24,
		"fps_den":        1,
		"duration_ms":    10000,
		"background": map[string]any{
			"kind":  "color",
			"color": []float64{0.06, 0.08, 0.12, 1.0},
		},
		"items": items,
	}

	semanticBytes, err := json.MarshalIndent(semanticDocument, "", "  ")
	if err != nil {
		t.Fatalf("marshal semantic document: %v", err)
	}

	compiled, err := CompileSemantic(semanticBytes)
	if err != nil {
		t.Fatalf("compile composite runtime semantic plan: %v", err)
	}

	outDir := filepath.Join("/tmp", "runtime_job_gpu")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create runtime job directory: %v", err)
	}

	videoPath := filepath.Join(outDir, "runtime_job_composite.mp4")
	_ = os.Remove(videoPath)
	compiled.Plan.Output.Path = videoPath

	planBytes, err := json.MarshalIndent(compiled.Plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(outDir, "plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	preparedBytes, err := json.MarshalIndent(compiled.Prepared, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	preparedPath := filepath.Join(outDir, "prepared.json")
	if err := os.WriteFile(preparedPath, preparedBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	renderStart := time.Now()
	cmd := exec.CommandContext(ctx, bin, "render",
		"--plan", planPath,
		"--prepared-package", preparedPath,
		"--assets-root", assetsRoot,
		"--backend", "vulkan",
		"--hardware", "nvenc",
		"--encoder-backend", "native",
		"--gpu-hot-path-mode", "require_gpu_native",
		"--encode-preset", "p1",
		"--rate-control", "qp",
		"--qp", "23",
		"-o", videoPath,
	)
	cmd.Dir = assetsRoot
	output, renderErr := cmd.CombinedOutput()
	durationSec := time.Since(renderStart).Seconds()
	if renderErr != nil {
		t.Fatalf("strict GPU runtime composite render failed: %v\nOutput:\n%s", renderErr, string(output))
	}

	stat, err := os.Stat(videoPath)
	if err != nil {
		t.Fatalf("output video missing: %v", err)
	}
	if stat.Size() == 0 {
		t.Fatalf("output video is empty")
	}

	probeOutput, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-count_frames", "-show_entries", "stream=codec_name,width,height,nb_read_frames", "-of", "json", videoPath).Output()
	if err != nil {
		t.Fatalf("ffprobe runtime job output: %v", err)
	}

	var probe struct {
		Streams []struct {
			CodecName    string `json:"codec_name"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			NbReadFrames string `json:"nb_read_frames"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(probeOutput, &probe); err != nil {
		t.Fatalf("unmarshal ffprobe output: %v", err)
	}
	if len(probe.Streams) == 0 {
		t.Fatalf("ffprobe found no video stream in %s", videoPath)
	}
	stream := probe.Streams[0]
	if stream.Width != 1280 || stream.Height != 720 {
		t.Fatalf("runtime job resolution = %dx%d, want 1280x720", stream.Width, stream.Height)
	}
	if stream.NbReadFrames != "240" {
		t.Fatalf("runtime job frames = %s, want 240 (10s @ 24fps)", stream.NbReadFrames)
	}

	report := struct {
		JobName         string   `json:"job_name"`
		Status          string   `json:"status"`
		Backend         string   `json:"backend"`
		HardwareEncoder string   `json:"hardware_encoder"`
		EncoderBackend  string   `json:"encoder_backend"`
		HotPathMode     string   `json:"hot_path_mode"`
		VideoPath       string   `json:"video_path"`
		FileSizeBytes   int64    `json:"file_size_bytes"`
		DurationSec     float64  `json:"render_duration_sec"`
		FPS             int      `json:"fps"`
		TotalFrames     int      `json:"total_frames"`
		Width           int      `json:"width"`
		Height          int      `json:"height"`
		Codec           string   `json:"codec"`
		Overlays        []string `json:"overlays"`
	}{
		JobName:         "runtime-job-composite",
		Status:          "passed",
		Backend:         "vulkan",
		HardwareEncoder: "nvenc",
		EncoderBackend:  "native",
		HotPathMode:     "require_gpu_native",
		VideoPath:       videoPath,
		FileSizeBytes:   stat.Size(),
		DurationSec:     durationSec,
		FPS:             24,
		TotalFrames:     240,
		Width:           stream.Width,
		Height:          stream.Height,
		Codec:           stream.CodecName,
		Overlays: []string{
			"phrase_apple_clean_01 (important_phrase)",
			"short_phrase_character_cascade_shapes (short_phrase_style)",
			"editorial_image_lift_shadow (editorial_image_v1)",
			"brush_arrow_point (brush_v1)",
			"text_3d_camera_push (text_3d_v1)",
		},
	}

	reportBytes, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		reportPath := filepath.Join(filepath.Dir(videoPath), "runtime-job-verification-report.json")
		_ = os.WriteFile(reportPath, reportBytes, 0o644)
	}

	t.Logf("Runtime job composite successfully rendered in %.2fs: %d frames, %d bytes, codec %s",
		durationSec, 240, stat.Size(), stream.CodecName)
}
