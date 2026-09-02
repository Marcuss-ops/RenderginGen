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
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
)

type PresetVideoJob struct {
	ID          string
	Title       string
	Description string
	Overlays    []overlay.FastEntityOverlay
}

func main() {
	fmt.Println("==================================================================")
	fmt.Println("🎬 RENDERING PRESET MODERNI — SOLO IMMAGINI REALI E TYPOGRAPHY INTER (24 FPS)")
	fmt.Println("==================================================================")

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	bgVideo := "assets/Pale-Olive.mp4"
	fontInterBold := "assets/fonts/Inter-Bold.ttf"
	fontDMBold := "assets/fonts/DMSans-Bold.ttf"

	assetsRoot := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/RenderingGen/testdata/golden"
	chrononBin := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/Chronon3d/build/chronon/linux-video-fast-dev/apps/chronon3d_cli/chronon3d_cli"

	folderID := "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
	credsFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/credentials.json"
	tokenFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/token.json"

	ctx := context.Background()
	publisher, err := drive.NewGoogleOAuth(ctx, credsFile, tokenFile, folderID)
	if err != nil {
		panic(fmt.Errorf("Drive init failed: %w", err))
	}

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("chronon_modern_%d.sock", time.Now().UnixNano()))
	fmt.Printf("🚀 Avvio Daemon Chronon su socket: %s...\n", socketPath)
	daemonCmd := exec.Command(chrononBin, "daemon", "-s", socketPath, "-a", assetsRoot, "--backend", "auto")
	if err := daemonCmd.Start(); err != nil {
		panic(fmt.Errorf("Impossibile avviare il daemon: %w", err))
	}
	defer func() {
		_ = daemonCmd.Process.Kill()
		_ = os.Remove(socketPath)
	}()

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
		panic("Daemon non pronto")
	}
	client := chronon.NewIPCClient(socketPath)

	durationFrames := int64(120) // 5 secondi @ 24 fps

	jobs := []PresetVideoJob{
		// ── 1. Cillian Murphy (Scale-In Pop + Inter-Bold Lower Third) ──
		{
			ID:          "modern_01_cillian_scale_in",
			Title:       "Cillian Murphy — Scale In Pop",
			Description: "Riconoscimento attore con foto realistica 8K e tipografia moderna Inter Bold",
			Overlays: []overlay.FastEntityOverlay{
				{
					Type:       "image",
					StartFrame: 0,
					EndFrame:   120,
					Position:   "center",
					Size:       480,
					Asset:      "assets/actor_cillian.jpg",
					Animation:  "scale_drop", // Preset: image_scale_in
					Opacity:    1.0,
					Translate:  []float64{0, -80},
				},
				{
					Type:       "text",
					StartFrame: 0,
					EndFrame:   120,
					Text:       "Cillian Murphy",
					Font:       fontInterBold,
					Size:       68,
					Color:      []float64{0.08, 0.08, 0.12, 1.0},
					Position:   "lower_third",
					Animation:  "fade_in", // Preset: lower_third_safe
					Opacity:    1.0,
				},
			},
		},

		// ── 2. Leonardo DiCaprio (Slide In Left + Slide Up Headline) ──
		{
			ID:          "modern_02_leonardo_slide_in",
			Title:       "Leonardo DiCaprio — Slide In Left",
			Description: "Entrata laterale fluida con titolo a risalita dal basso",
			Overlays: []overlay.FastEntityOverlay{
				{
					Type:       "image",
					StartFrame: 0,
					EndFrame:   120,
					Position:   "image_left",
					Size:       460,
					Asset:      "assets/actor_leonardo.jpg",
					Animation:  "slide_in", // Preset: image_slide_left
					Opacity:    1.0,
					Translate:  []float64{100, -40},
				},
				{
					Type:       "text",
					StartFrame: 0,
					EndFrame:   120,
					Text:       "Academy Award Winner",
					Font:       fontInterBold,
					Size:       60,
					Color:      []float64{0.08, 0.08, 0.12, 1.0},
					Position:   "center",
					Animation:  "reveal_from_bottom", // Preset: clean_slide_up
					Opacity:    1.0,
					Translate:  []float64{240, 20},
				},
			},
		},

		// ── 3. Gerard Butler (Focus Zoom In + Headline DM Sans) ──
		{
			ID:          "modern_03_gerard_focus_zoom",
			Title:       "Gerard Butler — Focus Zoom In",
			Description: "Zoom cinematografico morbido con typography DM Sans Bold",
			Overlays: []overlay.FastEntityOverlay{
				{
					Type:       "image",
					StartFrame: 0,
					EndFrame:   120,
					Position:   "center",
					Size:       480,
					Asset:      "assets/actor_gerard.jpg",
					Animation:  "focus_in", // Preset: image_focus_in
					Opacity:    1.0,
					Translate:  []float64{0, -80},
				},
				{
					Type:       "text",
					StartFrame: 0,
					EndFrame:   120,
					Text:       "Gerard Butler",
					Font:       fontDMBold,
					Size:       72,
					Color:      []float64{0.08, 0.08, 0.12, 1.0},
					Position:   "lower_third",
					Animation:  "reveal_from_bottom", // Preset: name_slide_up
					Opacity:    1.0,
				},
			},
		},

		// ── 4. Cinematic Artwork (Modern Rounded Card SDF) ──
		{
			ID:          "modern_04_rounded_artwork_sdf",
			Title:       "Cinematic Artwork — Modern Rounded Pop (SDF)",
			Description: "Card fotografica con angoli arrotondati calcolati in CUDA in tempo reale",
			Overlays: []overlay.FastEntityOverlay{
				{
					Type:       "image",
					StartFrame: 0,
					EndFrame:   120,
					Position:   "center",
					Size:       500,
					Asset:      "assets/test_artwork.jpg",
					Animation:  "scale_drop", // Preset: modern_rounded_pop
					Opacity:    1.0,
					Translate:  []float64{0, -70},
				},
				{
					Type:       "text",
					StartFrame: 0,
					EndFrame:   120,
					Text:       "Ultra High Definition Portrait",
					Font:       fontInterBold,
					Size:       56,
					Color:      []float64{0.08, 0.08, 0.12, 1.0},
					Position:   "center",
					Animation:  "scale_drop", // Preset: phrase_scale_in
					Opacity:    1.0,
					Translate:  []float64{0, 240},
				},
			},
		},

		// ── 5. Pure Impact Quote (Clean Inter-Bold Caption) ──
		{
			ID:          "modern_05_clean_typography_quote",
			Title:       "Statement Quote — Modern Typography",
			Description: "Frase centrale d'impatto ad altissima leggibilità con font Inter Bold",
			Overlays: []overlay.FastEntityOverlay{
				{
					Type:       "text",
					StartFrame: 0,
					EndFrame:   120,
					Text:       "Next-Generation Real-Time Video Engine",
					Font:       fontInterBold,
					Size:       64,
					Color:      []float64{0.08, 0.08, 0.12, 1.0},
					Position:   "center",
					Animation:  "scale_drop", // Preset: snap_scale
					Opacity:    1.0,
				},
			},
		},
	}

	outDir := filepath.Join(cwd, "modern_preset_videos")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		panic(err)
	}

	type Result struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		FileID   string  `json:"file_id"`
		DriveURL string  `json:"drive_url"`
		Sec      float64 `json:"sec"`
	}
	var results []Result

	for idx, job := range jobs {
		fmt.Printf("\n------------------------------------------------------------------\n")
		fmt.Printf("[%d/%d] 🎬 Rendering: %s\n", idx+1, len(jobs), job.Title)
		fmt.Printf("   %s\n", job.Description)

		videoFileName := fmt.Sprintf("%s_24fps_1080p.mp4", job.ID)
		videoPath := filepath.Join(outDir, videoFileName)
		planPath := filepath.Join(outDir, fmt.Sprintf("%s_plan.json", job.ID))

		plan, err := overlay.CompileFastEntityOverlays(
			job.ID,
			1920, 1080,
			24, 1,
			durationFrames,
			bgVideo,
			job.Overlays,
		)
		if err != nil {
			fmt.Printf("❌ BuildPlan error: %v\n", err)
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
				CompositionRequired: false,
				PacketCopyAllowed:   true,
			},
		}

		t0 := time.Now()
		if err := client.Render(ctx, req); err != nil {
			fmt.Printf("❌ Render failed: %v\n", err)
			continue
		}
		renderSec := time.Since(t0).Seconds()
		fmt.Printf("✓ Render completato in %.2fs (~%.1f FPS)\n", renderSec, float64(durationFrames)/renderSec)

		videoBytes, err := os.ReadFile(videoPath)
		if err != nil {
			fmt.Printf("❌ Read failed: %v\n", err)
			continue
		}

		fmt.Printf("☁️ Caricamento su Google Drive...\n")
		res, err := publisher.Publish(ctx, drive.PublishRequest{
			Name:         videoFileName,
			ContentType:  "video/mp4",
			Data:         videoBytes,
			ParentFolder: folderID,
		})
		if err != nil {
			fmt.Printf("❌ Drive upload error: %v\n", err)
			continue
		}

		fmt.Printf("🎉 UPLOAD SUCCESS: %s\n", res.WebViewLink)
		results = append(results, Result{
			ID:       job.ID,
			Title:    job.Title,
			FileID:   res.FileID,
			DriveURL: res.WebViewLink,
			Sec:      renderSec,
		})
	}

	summaryBytes, _ := json.MarshalIndent(results, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "modern_presets_summary.json"), summaryBytes, 0644)

	fmt.Println("\n==================================================================")
	fmt.Printf("🏁 COMPLETATI %d VIDEO CON IMMAGINI REALI E TYPOGRAPHY INTER!\n", len(results))
	fmt.Println("==================================================================")
}
