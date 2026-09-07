// Command render-each-preset renders and uploads one video per official
// preset through the real chronon3d_cli, validating every output with ffprobe
// and persisting the per-preset timing receipts. The preset catalog lives in
// presets.go; this file owns the render/upload loop.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
)

func validateMP4(path string, minDuration float64, width, height int, minFrames int64) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("output missing: %w", err)
	}
	if info.Size() <= 0 {
		return fmt.Errorf("output is empty")
	}
	probe := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,nb_frames",
		"-show_entries", "format=duration", "-of", "json", path)
	out, err := probe.Output()
	if err != nil {
		return fmt.Errorf("ffprobe: %w", err)
	}
	var result struct {
		Streams []struct {
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			NBFrames string `json:"nb_frames"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return fmt.Errorf("ffprobe JSON: %w", err)
	}
	if len(result.Streams) != 1 {
		return fmt.Errorf("expected one video stream, got %d", len(result.Streams))
	}
	stream := result.Streams[0]
	if stream.Width != width || stream.Height != height {
		return fmt.Errorf("resolution %dx%d, expected %dx%d", stream.Width, stream.Height, width, height)
	}
	frames, err := strconv.ParseInt(stream.NBFrames, 10, 64)
	if err != nil || frames < minFrames {
		return fmt.Errorf("frame count %q, expected at least %d", stream.NBFrames, minFrames)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(result.Format.Duration), 64)
	if err != nil || duration < minDuration {
		return fmt.Errorf("duration %q, expected at least %.3fs", result.Format.Duration, minDuration)
	}
	return nil
}

func main() {
	fmt.Println("==================================================================")
	fmt.Println("🎬 RENDERING SEPARATO PER OGNI SINGOLO PRESET (24 FPS)")
	fmt.Println("==================================================================")

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	// Use the real Pale Olive Classic motion background and a real celebrity
	// photograph. The preset canary must exercise the same image class used by
	// the runtime entity path; synthetic PNG fixtures are not representative.
	bgVideo := "color:#EEF1E7" // Pale Olive Classic; concrete Chronon color layer
	// The direct CLI canary mounts testdata/golden as the asset root; the
	// checked-in font therefore lives at its root (the worker's semantic path
	// is assets/fonts/Poppins-Bold.ttf after workspace materialisation).
	fontPath := "Poppins-Bold.ttf"
	assetsRoot := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/RenderingGen/testdata/golden"
	chrononBin := os.Getenv("CHRONON_BINARY")
	if chrononBin == "" {
		chrononBin = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/Chronon3d/build/chronon/linux-video-fast-dev/apps/chronon3d_cli/chronon3d_cli"
	}

	folderID := "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
	credsFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/credentials.json"
	tokenFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/token.json"

	ctx := context.Background()
	publisher, err := drive.NewGoogleOAuth(ctx, credsFile, tokenFile, folderID)
	if err != nil {
		panic(fmt.Errorf("Drive init failed: %w", err))
	}

	// 5 seconds minimum @ 24 fps; 125 frames keeps encoded duration above 5s.
	durationFrames := int64(125)

	presets := defaultPresets(fontPath)
	if os.Getenv("RENDERINGGEN_IMAGE_PRESETS_ONLY") == "1" {
		imagePresets := presets[:0]
		for _, preset := range presets {
			if preset.Kind == "image" {
				imagePresets = append(imagePresets, preset)
			}
		}
		presets = imagePresets
	}

	outDir := filepath.Join(cwd, "preset_videos")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		panic(err)
	}

	type ResultSummary struct {
		ID                   string  `json:"id"`
		PresetName           string  `json:"preset_name"`
		Title                string  `json:"title"`
		FileID               string  `json:"file_id"`
		DriveURL             string  `json:"drive_url"`
		RenderSec            float64 `json:"render_sec"`
		ReceiptRenderMS      float64 `json:"receipt_render_ms"`
		ReceiptWallTimeMS    float64 `json:"receipt_wall_time_ms"`
		ReceiptAvgFrameMS    float64 `json:"receipt_avg_frame_ms"`
		ReceiptEndToEndFPS   float64 `json:"receipt_end_to_end_fps"`
		ReceiptFramesTotal   int     `json:"receipt_frames_total"`
		ReceiptFramesOverrun int     `json:"receipt_frames_over_budget"`
		FastPathReusedFrames int     `json:"fast_path_reused_frames"`
		StartupMS            float64 `json:"startup_ms"`
		PrepareMS            float64 `json:"prepare_ms"`
		EncoderFinalizeMS    float64 `json:"encoder_finalize_ms"`
		ImageDecodeMS        float64 `json:"image_decode_ms"`
		ImageConvertMS       float64 `json:"image_convert_ms"`
		ImageDrawMS          float64 `json:"image_draw_ms"`
		StartupUnaccountedMS float64 `json:"startup_unaccounted_ms"`
		WallUnaccountedMS    float64 `json:"wall_unaccounted_ms"`
	}
	type receiptTiming struct {
		RenderMS    float64 `json:"render_ms"`
		WallTimeMS  float64 `json:"wall_time_ms"`
		FramesTotal int     `json:"frames_total"`
		Summary     struct {
			AvgFrameMS           float64 `json:"avg_frame_ms"`
			EndToEndFPS          float64 `json:"end_to_end_fps"`
			FramesOverBudget     int     `json:"frames_over_budget"`
			FastPathReusedFrames int     `json:"fast_path_reused_frames"`
		} `json:"summary"`
		Job struct {
			PrepareMS         float64 `json:"prepare_ms"`
			EncoderFinalizeMS float64 `json:"encoder_finalize_ms"`
			Image             struct {
				DecodeMS  float64 `json:"decode_ms"`
				ConvertMS float64 `json:"convert_ms"`
				DrawMS    float64 `json:"draw_ms"`
			} `json:"image"`
		} `json:"job"`
		StartupBreakdown struct {
			TotalMS       float64 `json:"total_startup_ms"`
			UnaccountedMS float64 `json:"unaccounted_ms"`
		} `json:"startup_breakdown"`
		Exclusive struct {
			UnaccountedMS float64 `json:"unaccounted_ms"`
		} `json:"exclusive_wall_timeline"`
	}
	readTiming := func(videoPath string) (receiptTiming, error) {
		var timing receiptTiming
		data, err := os.ReadFile(videoPath + ".timing.json")
		if err != nil {
			return timing, fmt.Errorf("timing receipt: %w", err)
		}
		if err := json.Unmarshal(data, &timing); err != nil {
			return timing, fmt.Errorf("timing receipt JSON: %w", err)
		}
		return timing, nil
	}
	var results []ResultSummary

	for idx, item := range presets {
		fmt.Printf("\n------------------------------------------------------------------\n")
		fmt.Printf("[%d/%d] 🚀 Rendering Preset: %s (%s)\n", idx+1, len(presets), item.PresetName, item.Title)
		fmt.Printf("   Descrizione: %s\n", item.Description)

		videoFileName := fmt.Sprintf("%s_24fps_1080p.mp4", item.ID)
		videoPath := filepath.Join(outDir, videoFileName)
		planPath := filepath.Join(outDir, fmt.Sprintf("%s_plan.json", item.ID))
		// Route every canary through the official preset catalog so text layout
		// and image placement are resolved by the same authority as production.
		item.Overlay.PresetID = item.PresetName

		// 1. Build Plan
		plan, err := overlay.CompileFastEntityOverlays(
			item.ID,
			1920, 1080,
			24, 1,
			durationFrames,
			bgVideo,
			[]overlay.FastEntityOverlay{item.Overlay},
		)
		if err != nil {
			fmt.Printf("❌ BuildPlan error: %v\n", err)
			continue
		}
		plan.Output.Path = videoPath

		planBytes, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(planPath, planBytes, 0644); err != nil {
			panic(err)
		}

		// 2. Render with Chronon compositor. Presets are authored
		// compositions (background + image/text), so use the compositor pipe
		// backend; direct-YUV is reserved for source-video-only paths.
		args := []string{
			"render",
			"--plan", planPath,
			"--assets-root", assetsRoot,
			"--backend", "software",
			"--encoder-backend", "pipe",
			"--hardware", "none",
			"--gpu-hot-path-mode", "auto",
			"-o", videoPath,
		}

		t0 := time.Now()
		cmd := exec.Command(chrononBin, args...)
		out, err := cmd.CombinedOutput()
		renderSec := time.Since(t0).Seconds()
		if err != nil {
			fmt.Printf("❌ Chronon render failed for %s: %v\n%s\n", item.ID, err, string(out))
			continue
		}
		if err := validateMP4(videoPath, 5.0, 1920, 1080, durationFrames); err != nil {
			fmt.Printf("❌ Invalid MP4 for %s: %v\n", item.ID, err)
			continue
		}
		timing, timingErr := readTiming(videoPath)
		if timingErr != nil {
			fmt.Printf("❌ Missing/invalid timing receipt for %s: %v\n", item.ID, timingErr)
			continue
		}
		fmt.Printf("✓ Render completato in %.2fs (~%.1f FPS) | render %.1fms, e2e %.1f FPS, prepare %.1fms, image decode/convert/draw %.1f/%.1f/%.1fms, over-budget %d/%d\n",
			renderSec, float64(durationFrames)/renderSec, timing.RenderMS,
			timing.Summary.EndToEndFPS, timing.Job.PrepareMS,
			timing.Job.Image.DecodeMS, timing.Job.Image.ConvertMS, timing.Job.Image.DrawMS,
			timing.Summary.FramesOverBudget, timing.FramesTotal)
		result := ResultSummary{
			ID: item.ID, PresetName: item.PresetName, Title: item.Title,
			RenderSec: renderSec, ReceiptRenderMS: timing.RenderMS,
			ReceiptWallTimeMS: timing.WallTimeMS, ReceiptAvgFrameMS: timing.Summary.AvgFrameMS,
			ReceiptEndToEndFPS: timing.Summary.EndToEndFPS, ReceiptFramesTotal: timing.FramesTotal,
			ReceiptFramesOverrun: timing.Summary.FramesOverBudget,
			FastPathReusedFrames: timing.Summary.FastPathReusedFrames,
			StartupMS:            timing.StartupBreakdown.TotalMS,
			PrepareMS:            timing.Job.PrepareMS,
			EncoderFinalizeMS:    timing.Job.EncoderFinalizeMS,
			ImageDecodeMS:        timing.Job.Image.DecodeMS,
			ImageConvertMS:       timing.Job.Image.ConvertMS,
			ImageDrawMS:          timing.Job.Image.DrawMS,
			StartupUnaccountedMS: timing.StartupBreakdown.UnaccountedMS,
			WallUnaccountedMS:    timing.Exclusive.UnaccountedMS,
		}
		// Upload only the final MP4 to the canonical Drive folder. Plans,
		// receipts and the local summary remain local diagnostics.
		fmt.Printf("☁️ Uploading %s to Drive folder %s...\n", videoFileName, folderID)
		res, err := publisher.Publish(ctx, drive.PublishRequest{
			Name:         videoFileName,
			ContentType:  "video/mp4",
			Path:         videoPath,
			ParentFolder: folderID,
		})
		if err != nil {
			fmt.Printf("❌ Drive upload failed: %v\n", err)
			continue
		}

		fmt.Printf("🎉 UPLOAD SUCCESS: %s\n", res.WebViewLink)
		result.FileID, result.DriveURL = res.FileID, res.WebViewLink
		results = append(results, result)
	}

	summaryBytes, _ := json.MarshalIndent(results, "", "  ")
	summaryPath := filepath.Join(outDir, "all_presets_upload_summary.json")
	_ = os.WriteFile(summaryPath, summaryBytes, 0644)
	fmt.Println("\n==================================================================")
	fmt.Printf("🏁 PRESET COMPLETATI: %d/%d\n", len(results), len(presets))
	fmt.Println("==================================================================")
	if len(results) != len(presets) {
		fmt.Fprintf(os.Stderr, "benchmark failed: %d preset non hanno prodotto output + timing receipt validi\n", len(presets)-len(results))
		os.Exit(1)
	}
}
