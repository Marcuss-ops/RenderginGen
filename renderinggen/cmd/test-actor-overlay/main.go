package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
)

func main() {
	fmt.Println("==================================================================")
	fmt.Println("🚀 RUNNING 24 FPS E2E TEST: GO + RENDERINGGEN + CHRONON (24 FPS)")
	fmt.Println("==================================================================")

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	bgVideo := "assets/Pale-Olive.mp4"
	actorImage := "assets/actor_gerard.jpg"
	fontPath := "assets/fonts/Poppins-Bold.ttf"
	outputFile := filepath.Join(cwd, "actor_pale_olive_24fps_1080p.mp4")

	// 15 seconds @ 24 fps = 360 frames
	totalDurationFrames := int64(360)

	// 1. Define Entity Overlays using the 24 FPS Contract
	overlays := []overlay.FastEntityOverlay{
		{
			Type:       "image",
			StartFrame: 0,
			EndFrame:   120, // 0s - 5s
			Position:   "center",
			Size:       480,
			Asset:      actorImage,
			Animation:  "scale_drop",
			Opacity:    1.0,
			Translate:  []float64{0, -80},
		},
		{
			Type:       "text",
			StartFrame: 0,
			EndFrame:   120, // 0s - 5s
			Text:       "Gerard Butler",
			Font:       fontPath,
			Size:       64,
			Color:      []float64{1.0, 1.0, 1.0, 1.0},
			Position:   "lower_third",
			Animation:  "fade_in",
			Opacity:    1.0,
		},
		{
			Type:       "text",
			StartFrame: 120,
			EndFrame:   240, // 5s - 10s
			Text:       "International Film Star",
			Font:       fontPath,
			Size:       68,
			Color:      []float64{1.0, 1.0, 1.0, 1.0},
			Position:   "center",
			Animation:  "reveal_from_bottom",
			Opacity:    1.0,
		},
		{
			Type:       "image",
			StartFrame: 240,
			EndFrame:   336, // 10s - 14s
			Position:   "image_left",
			Size:       420,
			Asset:      actorImage,
			Animation:  "scale_drop",
			Opacity:    1.0,
			Translate:  []float64{60, 0},
		},
		{
			Type:       "text",
			StartFrame: 240,
			EndFrame:   336, // 10s - 14s
			Text:       "Leading New Blockbuster Production",
			Font:       fontPath,
			Size:       54,
			Color:      []float64{1.0, 1.0, 1.0, 1.0},
			Position:   "center",
			Animation:  "fade_in",
			Opacity:    1.0,
			Translate:  []float64{280, 0},
		},
	}

	// 2. Build Plan via Contract (24 fps canonical contract)
	plan, err := overlay.CompileFastEntityOverlays(
		"actor-pale-olive-24fps-e2e",
		1920, 1080,
		24, 1,
		totalDurationFrames,
		bgVideo,
		overlays,
	)
	if err != nil {
		panic(fmt.Errorf("BuildPlan failed: %w", err))
	}
	plan.Output.Path = outputFile

	planBytes, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		panic(err)
	}

	planPath := filepath.Join(cwd, "actor_pale_olive_24fps_plan.json")
	if err := os.WriteFile(planPath, planBytes, 0644); err != nil {
		panic(err)
	}
	fmt.Printf("✓ Generated 24 FPS Render Plan JSON: %s (%d layers, 24/1 fps)\n", planPath, len(plan.Layers))

	// 3. Execute Chronon3d Direct YUV GPU Renderer at 24 FPS
	assetsRoot := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/RenderingGen/testdata/golden"
	chrononBin := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/Chronon3d/build/chronon/linux-video-fast-dev/apps/chronon3d_cli/chronon3d_cli"

	args := []string{
		"render",
		"--plan", planPath,
		"--assets-root", assetsRoot,
		"--encoder-backend", "native",
		"--hardware", "nvenc",
		"--gpu-hot-path-mode", "require_direct_yuv",
		"-o", outputFile,
	}

	fmt.Println("⚙️ Executing Chronon GPU Native Direct YUV Renderer (24 FPS)...")
	startTime := time.Now()
	cmd := exec.Command(chrononBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic(fmt.Errorf("Chronon execution failed: %w", err))
	}
	renderDuration := time.Since(startTime)
	fmt.Printf("✓ 24 FPS Render completed in %v! (~%.1f FPS)\n", renderDuration, float64(totalDurationFrames)/renderDuration.Seconds())

	// 4. Publish directly to Google Drive
	folderID := "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
	credsFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/credentials.json"
	tokenFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/token.json"

	videoBytes, err := os.ReadFile(outputFile)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	publisher, err := drive.NewGoogleOAuth(ctx, credsFile, tokenFile, folderID)
	if err != nil {
		panic(fmt.Errorf("Drive init failed: %w", err))
	}

	uploadName := "actor_gerard_butler_pale_olive_24fps_1080p.mp4"
	fmt.Printf("☁️ Uploading %s (%d bytes) to Drive folder %s...\n", uploadName, len(videoBytes), folderID)
	res, err := publisher.Publish(ctx, drive.PublishRequest{
		Name:         uploadName,
		ContentType:  "video/mp4",
		Data:         videoBytes,
		ParentFolder: folderID,
	})
	if err != nil {
		panic(fmt.Errorf("Drive publish failed: %w", err))
	}

	fmt.Println("==================================================================")
	fmt.Printf("🎉 24 FPS E2E TEST SUCCESSFUL!\n")
	fmt.Printf("   File ID:    %s\n", res.FileID)
	fmt.Printf("   Drive Link: %s\n", res.WebViewLink)
	fmt.Printf("   SHA-256:    %s\n", res.SHA256)
	fmt.Printf("   Size:       %d bytes\n", res.SizeBytes)
	fmt.Println("==================================================================")
}
