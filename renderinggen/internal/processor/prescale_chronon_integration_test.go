package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// TestPrescaleChrononIntegration proves the derived image is accepted by a real
// Chronon render, including the prepared-package hash check. It is opt-in
// because it needs the local engine binary and a real oversized source.
//
//	CHRONON_BIN=... PRESCALE_TEST_IMAGE=... \
//	  go test ./internal/processor -run TestPrescaleChrononIntegration -count=1 -v
func TestPrescaleChrononIntegration(t *testing.T) {
	bin := os.Getenv("CHRONON_BIN")
	imagePath := os.Getenv("PRESCALE_TEST_IMAGE")
	if bin == "" || imagePath == "" {
		t.Skip("set CHRONON_BIN and PRESCALE_TEST_IMAGE to run the real-engine prescale check")
	}
	imageBytes, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatalf("read source image: %v", err)
	}
	assetHash := storage.Hash(imageBytes)
	proc, store, _ := newProcessorWith(t, Options{})
	if err := store.Put(context.Background(), assetHash, imageBytes); err != nil {
		t.Fatalf("put source image: %v", err)
	}
	job := &queue.Job{
		ID: "prescale-chronon-1", Schema: queue.JobSchemaV1, Version: queue.JobSchemaVersionV1,
		RenderPlan: json.RawMessage(fmt.Sprintf(`{
          "schema_version":"renderinggen.overlay-plan.v1",
          "plan_id":"prescale-chronon-1","video_id":"prescale-chronon-1",
          "width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_ms":5000,
          "background":{"kind":"color","color":[0.03,0.03,0.03,1]},
          "items":[
            {"id":"photo","kind":"image","template_id":"IMAGE_OVERLAY","preset_id":"image_focus_in",
             "motion_id":"image_focus_reveal","start_ms":0,"end_ms":5000,
             "asset_refs":[{"asset_id":"photo","sha256":"%s","url":"https://store.example/objects/photo.jpg","media_type":"image/jpeg"}]}
          ]
        }`, assetHash)),
	}

	prepareStart := time.Now()
	prepared, err := proc.PrepareJob(context.Background(), job)
	if err != nil {
		t.Fatalf("PrepareJob: %v", err)
	}
	t.Cleanup(func() { _ = prepared.Workspace.Cleanup() })
	prepareDuration := time.Since(prepareStart)

	var imageAsset, sourceAsset string
	var imageWidth, imageHeight, sourceWidth, sourceHeight int
	for _, layer := range prepared.Plan.Layers {
		if layer.Type == "image" {
			imageAsset = layer.Asset
			path, pathErr := prepared.Workspace.AssetPath(layer.Asset)
			if pathErr != nil {
				t.Fatalf("resolve image asset: %v", pathErr)
			}
			imageWidth, imageHeight = imageDimensions(t, path)
			break
		}
	}
	for _, asset := range prepared.ResolvedAssets {
		if strings.Contains(asset.LogicalPath, "prescaled") {
			continue
		}
		if strings.HasSuffix(strings.ToLower(asset.LogicalPath), ".jpg") || strings.HasSuffix(strings.ToLower(asset.LogicalPath), ".png") {
			sourceAsset = asset.LogicalPath
			break
		}
	}
	if imageAsset == "" || imageWidth > 2048 || imageHeight > 2048 {
		t.Fatalf("prepared image asset %q is %dx%d, want a display-bound derivative", imageAsset, imageWidth, imageHeight)
	}
	if sourceAsset == "" {
		t.Fatal("prepared job lost the original source asset")
	}
	sourcePath, err := prepared.Workspace.AssetPath(sourceAsset)
	if err != nil {
		t.Fatalf("resolve source asset: %v", err)
	}
	sourceWidth, sourceHeight = imageDimensions(t, sourcePath)

	// A second identical job must hit the cross-job derivative cache instead
	// of decoding the 24-megapixel source again.
	cachedJob := *job
	cachedJob.ID = "prescale-chronon-2"
	cachedStart := time.Now()
	cachedPrepared, err := proc.PrepareJob(context.Background(), &cachedJob)
	if err != nil {
		t.Fatalf("PrepareJob(cached): %v", err)
	}
	cachedDuration := time.Since(cachedStart)
	_ = cachedPrepared.Workspace.Cleanup()

	outputPath := filepath.Join(t.TempDir(), "prescaled.mp4")
	prepared.Plan.Output.Path = outputPath
	planBytes, err := json.Marshal(prepared.Plan)
	if err != nil {
		t.Fatalf("marshal final plan: %v", err)
	}
	if err := os.WriteFile(prepared.Workspace.PlanPath(), planBytes, 0o644); err != nil {
		t.Fatalf("write final plan: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "render",
		"--plan", prepared.Workspace.PlanPath(),
		"--prepared-package", prepared.Workspace.PreparedPackagePath(),
		"--assets-root", prepared.Workspace.Root(),
		"--backend", "vulkan", "--hardware", "nvenc", "--encoder-backend", "native",
		"--gpu-hot-path-mode", "require_gpu_native",
		"--encode-preset", "p1", "--rate-control", "qp", "--qp", "23",
		"-o", outputPath)
	cmd.Dir = prepared.Workspace.Root()
	renderStart := time.Now()
	output, renderErr := cmd.CombinedOutput()
	renderDuration := time.Since(renderStart)
	if renderErr != nil {
		t.Fatalf("Chronon rejected the prescaled plan after %s: %v\n%s", renderDuration, renderErr, string(output))
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("Chronon produced no output: stat=%v err=%v", info, err)
	}
	sidecarBytes, err := os.ReadFile(outputPath + ".timing.json")
	if err != nil {
		t.Fatalf("read Chronon timing sidecar: %v", err)
	}
	var sidecar struct {
		Job struct {
			Image struct {
				DecodeCount              *uint64  `json:"decode_count"`
				PostDecodeDownscaleCount *uint64  `json:"post_decode_downscale_count"`
				DecodeMS                 *float64 `json:"decode_ms"`
			} `json:"image"`
		} `json:"job"`
	}
	if err := json.Unmarshal(sidecarBytes, &sidecar); err != nil {
		t.Fatalf("decode Chronon timing sidecar: %v", err)
	}
	var decodeCount, downscaleCount uint64
	if sidecar.Job.Image.DecodeCount != nil {
		decodeCount = *sidecar.Job.Image.DecodeCount
	}
	if sidecar.Job.Image.PostDecodeDownscaleCount == nil {
		t.Fatal("Chronon sidecar omitted post_decode_downscale_count")
	}
	downscaleCount = *sidecar.Job.Image.PostDecodeDownscaleCount
	t.Logf("chronon sidecar image: decode_count=%d post_decode_downscale_count=%d", decodeCount, downscaleCount)
	if downscaleCount != 0 {
		t.Fatalf("Chronon downscaled after decode %d time(s), want 0 for a display-bound derivative", downscaleCount)
	}
	t.Logf("prepared=%s cached_prepare=%s render=%s source=%dx%d derived=%dx%d chronon_decode_count=%d chronon_post_decode_downscale_count=%d plan=%s package=%s output_bytes=%d",
		prepareDuration.Round(time.Millisecond), cachedDuration.Round(time.Millisecond), renderDuration.Round(time.Millisecond),
		sourceWidth, sourceHeight, imageWidth, imageHeight, decodeCount, *sidecar.Job.Image.PostDecodeDownscaleCount,
		prepared.Workspace.PlanPath(), prepared.Workspace.PreparedPackagePath(), info.Size())
}
