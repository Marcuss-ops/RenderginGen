package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
)

type PresetItem struct {
	ID      string
	Title   string
	Overlay overlay.FastEntityOverlay
}

func main() {
	fmt.Println("==================================================================")
	fmt.Println("⚡ BENCHMARK DAEMON A CALDO (PERSISTENT GPU SESSION)")
	fmt.Println("==================================================================")

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	bgVideo := "assets/Pale-Olive.mp4"
	fontPath := "assets/fonts/Poppins-Bold.ttf"
	assetsRoot := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/RenderingGen/testdata/golden"
	chrononBin := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/Chronon3d/build/chronon/linux-video-fast-dev/apps/chronon3d_cli/chronon3d_cli"
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("chronon_bench_%d.sock", time.Now().UnixNano()))

	// 1. Avvio del Demone Chronon in background
	fmt.Printf("🚀 Avvio Daemon Chronon su socket Unix: %s...\n", socketPath)
	daemonCmd := exec.Command(chrononBin, "daemon", "-s", socketPath, "-a", assetsRoot, "--backend", "auto")
	daemonCmd.Stdout = os.Stdout
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		panic(fmt.Errorf("Impossibile avviare il daemon: %w", err))
	}
	defer func() {
		_ = daemonCmd.Process.Kill()
		_ = os.Remove(socketPath)
	}()

	// Attesa che il socket sia pronto
	ready := false
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if conn, err := net.Dial("unix", socketPath); err == nil {
			_ = conn.Close()
			ready = true
			break
		}
	}
	if !ready {
		panic("Il daemon socket non è diventato raggiungibile entro 5s")
	}
	fmt.Println("✓ Daemon connesso e pronto con contesti GPU caldi!")

	client := chronon.NewIPCClient(socketPath)
	ctx := context.Background()

	status, err := client.Status(ctx)
	if err == nil {
		fmt.Printf("📊 Daemon initial status: %s\n", status)
	}

	// 5s @ 24 fps = 120 frames
	durationFrames := int64(120)

	presets := []PresetItem{
		{
			ID:    "warm_01_image_scale_in",
			Title: "Image Scale In (Pop)",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   120,
				Position:   "center",
				Size:       450,
				Asset:      "assets/actor_gerard.jpg",
				Animation:  "scale_drop",
				Opacity:    1.0,
			},
		},
		{
			ID:    "warm_02_image_slide_left",
			Title: "Image Slide Left",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   120,
				Position:   "image_left",
				Size:       380,
				Asset:      "assets/apple.png",
				Animation:  "slide_in",
				Opacity:    1.0,
				Translate:  []float64{120, 0},
			},
		},
		{
			ID:    "warm_03_image_slide_right",
			Title: "Image Slide Right",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   120,
				Position:   "image_right",
				Size:       380,
				Asset:      "assets/go_logo.png",
				Animation:  "slide_in",
				Opacity:    1.0,
				Translate:  []float64{-120, 0},
			},
		},
		{
			ID:    "warm_04_image_focus_in",
			Title: "Image Focus In",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   120,
				Position:   "center",
				Size:       420,
				Asset:      "assets/overlay_globe.png",
				Animation:  "focus_in",
				Opacity:    1.0,
			},
		},
		{
			ID:    "warm_05_modern_rounded_pop",
			Title: "Modern Rounded Pop (SDF)",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   120,
				Position:   "center",
				Size:       480,
				Asset:      "assets/test_artwork.jpg",
				Animation:  "scale_drop",
				Opacity:    1.0,
			},
		},
		{
			ID:    "warm_06_bottom_card_rise",
			Title: "Bottom Card Rise",
			Overlay: overlay.FastEntityOverlay{
				Type:       "image",
				StartFrame: 0,
				EndFrame:   120,
				Position:   "center",
				Size:       420,
				Asset:      "assets/overlay_chart.png",
				Animation:  "reveal_from_bottom",
				Opacity:    1.0,
			},
		},
		{
			ID:    "warm_07_lower_third_safe",
			Title: "Lower Third Safe (Testo)",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   120,
				Text:       "Gerard Butler",
				Font:       fontPath,
				Size:       72,
				Color:      []float64{1.0, 1.0, 1.0, 1.0},
				Position:   "lower_third",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},
		{
			ID:    "warm_08_clean_slide_up",
			Title: "Clean Slide Up (Headline)",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   120,
				Text:       "Global Technology Infrastructure",
				Font:       fontPath,
				Size:       58,
				Color:      []float64{1.0, 1.0, 1.0, 1.0},
				Position:   "center",
				Animation:  "reveal_from_bottom",
				Opacity:    1.0,
			},
		},
	}

	outDir := filepath.Join(cwd, "warm_videos")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		panic(err)
	}

	fmt.Println("\n==================================================================")
	fmt.Println("🚀 INIZIO RENDERING WARM SESSION TRAMITE IPC SOCKET")
	fmt.Println("==================================================================")

	var totalDuration time.Duration
	failures := 0
	successes := 0

	for idx, item := range presets {
		videoPath := filepath.Join(outDir, fmt.Sprintf("%s_warm.mp4", item.ID))
		planPath := filepath.Join(outDir, fmt.Sprintf("%s_plan.json", item.ID))

		plan, err := overlay.CompileFastEntityOverlays(
			item.ID,
			1920, 1080,
			24, 1,
			durationFrames,
			bgVideo,
			[]overlay.FastEntityOverlay{item.Overlay},
		)
		if err != nil {
			fmt.Printf("❌ BuildPlan failed: %v\n", err)
			failures++
			continue
		}
		plan.Output.Path = videoPath

		planBytes, _ := json.MarshalIndent(plan, "", "  ")
		_ = os.WriteFile(planPath, planBytes, 0644)

		req := chronon.RenderRequest{
			PlanPath:   planPath,
			AssetsRoot: assetsRoot,
			OutputPath: videoPath,
			Report:     false,
			Requirements: chronon.ExecutionRequirements{
				GPURequired:         true,
				CPUFallbackAllowed:  false,
				CompositionRequired: false, // Direct-YUV Fast Path!
				PacketCopyAllowed:   true,
			},
		}

		t0 := time.Now()
		err = client.Render(ctx, req)
		dur := time.Since(t0)

		if err != nil {
			fmt.Printf("[%d/%d] ❌ Render IPC fallito per %s: %v\n", idx+1, len(presets), item.Title, err)
			failures++
		} else {
			successes++
			totalDuration += dur
			fmt.Printf("[%d/%d] ⚡ %-32s -> Render completato in %6.2f ms (~%.1f FPS)\n",
				idx+1, len(presets), item.Title, float64(dur.Milliseconds()), float64(durationFrames)/dur.Seconds())
		}
	}

	var avgDur time.Duration
	if successes > 0 {
		avgDur = totalDuration / time.Duration(successes)
	}
	fmt.Println("==================================================================")
	fmt.Printf("🏁 BENCHMARK COMPLETATO: %d/%d riusciti, %d falliti\n", successes, len(presets), failures)
	if successes > 0 {
		fmt.Printf("   Tempo medio per video (5s / 120 frame): %v (~%.1f FPS)\n", avgDur, float64(durationFrames)/avgDur.Seconds())
	} else {
		fmt.Println("   Nessun render riuscito: throughput non disponibile")
	}
	fmt.Printf("   Tempo totale per %d video: %v\n", len(presets), totalDuration)
	fmt.Println("==================================================================")

	_ = client.Shutdown(ctx)
	if failures > 0 {
		os.Exit(1)
	}
}
