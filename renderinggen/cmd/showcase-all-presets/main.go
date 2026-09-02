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
	fmt.Println("🎬 SHOWCASE COMPLETO PRESET IMMAGINI & FRASI CHIAVE (24 FPS)")
	fmt.Println("==================================================================")

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	bgVideo := "assets/Pale-Olive.mp4"
	fontPath := "assets/fonts/Poppins-Bold.ttf"
	outputFile := filepath.Join(cwd, "showcase_all_presets_24fps_1080p.mp4")

	// 30 seconds @ 24 fps = 720 frames
	totalDurationFrames := int64(720)

	// Definizione di tutti i preset di immagini e frasi importanti
	overlays := []overlay.FastEntityOverlay{
		// ── SCENA 1: Actor Focus + Lower Third + Caption Card (0s - 5s / frame 0-120) ──
		{
			Type:       "image",
			StartFrame: 0,
			EndFrame:   120,
			Position:   "center",
			Size:       460,
			Asset:      "assets/actor_gerard.jpg",
			Animation:  "scale_drop", // Preset: image_scale_in
			Opacity:    1.0,
			Translate:  []float64{0, -80},
		},
		{
			Type:       "text",
			StartFrame: 0,
			EndFrame:   120,
			Text:       "Gerard Butler",
			Font:       fontPath,
			Size:       64,
			Color:      []float64{1.0, 1.0, 1.0, 1.0},
			Position:   "lower_third",
			Animation:  "fade_in", // Preset: lower_third_safe
			Opacity:    1.0,
		},
		{
			Type:       "text",
			StartFrame: 30,
			EndFrame:   120,
			Text:       "Hollywood Lead Actor & Producer",
			Font:       fontPath,
			Size:       42,
			Color:      []float64{0.9, 0.9, 0.95, 1.0},
			Position:   "lower_third",
			Animation:  "fade_in", // Preset: caption_card
			Opacity:    0.95,
			Translate:  []float64{0, 60},
		},

		// ── SCENA 2: Multi-Image Slide Left / Right + Clean Slide Up (5s - 10s / frame 120-240) ──
		{
			Type:       "image",
			StartFrame: 120,
			EndFrame:   240,
			Position:   "image_left",
			Size:       320,
			Asset:      "apple.png",
			Animation:  "slide_in", // Preset: image_slide_left
			Opacity:    1.0,
			Translate:  []float64{100, -40},
		},
		{
			Type:       "image",
			StartFrame: 120,
			EndFrame:   240,
			Position:   "image_right",
			Size:       320,
			Asset:      "go_logo.png",
			Animation:  "slide_in", // Preset: image_slide_right
			Opacity:    1.0,
			Translate:  []float64{-100, -40},
		},
		{
			Type:       "text",
			StartFrame: 120,
			EndFrame:   240,
			Text:       "Global Tech Ecosystem",
			Font:       fontPath,
			Size:       62,
			Color:      []float64{1.0, 1.0, 1.0, 1.0},
			Position:   "center",
			Animation:  "reveal_from_bottom", // Preset: clean_slide_up / phrase_slide_up
			Opacity:    1.0,
			Translate:  []float64{0, 200},
		},

		// ── SCENA 3: Modern Rounded Pop Card + Phrase Scale In (10s - 15s / frame 240-360) ──
		{
			Type:       "image",
			StartFrame: 240,
			EndFrame:   360,
			Position:   "center",
			Size:       450,
			Asset:      "test_artwork.jpg",
			Animation:  "scale_drop", // Preset: modern_rounded_pop (SDF Corner Radius)
			Opacity:    1.0,
			Translate:  []float64{0, -70},
		},
		{
			Type:       "text",
			StartFrame: 240,
			EndFrame:   360,
			Text:       "High Performance GPU Rendering",
			Font:       fontPath,
			Size:       56,
			Color:      []float64{1.0, 1.0, 1.0, 1.0},
			Position:   "center",
			Animation:  "scale_drop", // Preset: phrase_scale_in
			Opacity:    1.0,
			Translate:  []float64{0, 240},
		},

		// ── SCENA 4: Focus In Image + Slide Up Phrase (15s - 20s / frame 360-480) ──
		{
			Type:       "image",
			StartFrame: 360,
			EndFrame:   480,
			Position:   "center",
			Size:       380,
			Asset:      "overlay_globe.png",
			Animation:  "focus_in", // Preset: image_focus_in
			Opacity:    1.0,
			Translate:  []float64{0, -80},
		},
		{
			Type:       "text",
			StartFrame: 360,
			EndFrame:   480,
			Text:       "Worldwide Real-Time Delivery",
			Font:       fontPath,
			Size:       58,
			Color:      []float64{1.0, 1.0, 1.0, 1.0},
			Position:   "center",
			Animation:  "reveal_from_bottom", // Preset: phrase_slide_up
			Opacity:    1.0,
			Translate:  []float64{0, 180},
		},

		// ── SCENA 5: Bottom Card Rise + Snap Scale Callout (20s - 25s / frame 480-600) ──
		{
			Type:       "image",
			StartFrame: 480,
			EndFrame:   600,
			Position:   "image_left",
			Size:       400,
			Asset:      "overlay_chart.png",
			Animation:  "reveal_from_bottom", // Preset: bottom_card_rise
			Opacity:    1.0,
			Translate:  []float64{80, 0},
		},
		{
			Type:       "text",
			StartFrame: 480,
			EndFrame:   600,
			Text:       "Record Performance: +450% Throughput",
			Font:       fontPath,
			Size:       52,
			Color:      []float64{1.0, 1.0, 1.0, 1.0},
			Position:   "center",
			Animation:  "scale_drop", // Preset: snap_scale
			Opacity:    1.0,
			Translate:  []float64{280, 0},
		},

		// ── SCENA 6: Multi-Entity Outro (25s - 30s / frame 600-720) ──
		{
			Type:       "image",
			StartFrame: 600,
			EndFrame:   720,
			Position:   "image_left",
			Size:       380,
			Asset:      "assets/actor_gerard.jpg",
			Animation:  "scale_drop",
			Opacity:    1.0,
			Translate:  []float64{80, -40},
		},
		{
			Type:       "image",
			StartFrame: 600,
			EndFrame:   720,
			Position:   "image_right",
			Size:       360,
			Asset:      "glowing_emblem.png",
			Animation:  "fade_in", // Preset: image_fade_in
			Opacity:    1.0,
			Translate:  []float64{-80, -40},
		},
		{
			Type:       "text",
			StartFrame: 600,
			EndFrame:   720,
			Text:       "Fast Entity Overlay Pipeline — 100% GPU Native",
			Font:       fontPath,
			Size:       50,
			Color:      []float64{1.0, 1.0, 1.0, 1.0},
			Position:   "center",
			Animation:  "fade_in", // Preset: phrase_fade_in
			Opacity:    1.0,
			Translate:  []float64{0, 240},
		},
	}

	// 2. Build Plan via Contract (24 fps canonical contract)
	plan, err := overlay.CompileFastEntityOverlays(
		"showcase-all-presets-24fps",
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

	planPath := filepath.Join(cwd, "showcase_all_presets_plan.json")
	if err := os.WriteFile(planPath, planBytes, 0644); err != nil {
		panic(err)
	}
	fmt.Printf("✓ Generated Showcase Plan JSON: %s (%d layers, 24 FPS, %d frames)\n", planPath, len(plan.Layers), totalDurationFrames)

	// 3. Execute Chronon3d Direct YUV GPU Renderer
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

	fmt.Println("⚙️ Executing Chronon GPU Native Direct YUV Renderer (Showcase 24 FPS)...")
	startTime := time.Now()
	cmd := exec.Command(chrononBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic(fmt.Errorf("Chronon execution failed: %w", err))
	}
	renderDuration := time.Since(startTime)
	fmt.Printf("✓ Showcase Render completed in %v! (~%.1f FPS)\n", renderDuration, float64(totalDurationFrames)/renderDuration.Seconds())

	// 4. Publish directly to Google Drive
	folderID := "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
	credsFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/credentials.json"
	tokenFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/token.json"

	ctx := context.Background()
	publisher, err := drive.NewGoogleOAuth(ctx, credsFile, tokenFile, folderID)
	if err != nil {
		panic(fmt.Errorf("Drive init failed: %w", err))
	}

	uploadName := "showcase_all_presets_and_phrases_24fps_1080p.mp4"
	fmt.Printf("☁️ Uploading %s to Drive folder %s...\n", uploadName, folderID)
	res, err := publisher.Publish(ctx, drive.PublishRequest{
		Name:         uploadName,
		ContentType:  "video/mp4",
		Path:         outputFile,
		ParentFolder: folderID,
	})
	if err != nil {
		panic(fmt.Errorf("Drive publish failed: %w", err))
	}

	fmt.Println("==================================================================")
	fmt.Printf("🎉 SHOWCASE VIDEO UPLOADED SUCCESSFULLY!\n")
	fmt.Printf("   File ID:    %s\n", res.FileID)
	fmt.Printf("   Drive Link: %s\n", res.WebViewLink)
	fmt.Printf("   SHA-256:    %s\n", res.SHA256)
	fmt.Printf("   Size:       %d bytes\n", res.SizeBytes)
	fmt.Println("==================================================================")
}
