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

type PhraseVideoJob struct {
	ID          string
	Title       string
	Description string
	Overlay     overlay.FastEntityOverlay
}

func main() {
	fmt.Println("==================================================================")
	fmt.Println("✨ RENDERING DEDICATO: SOLO FRASI CON ANIMAZIONI DINAMICHE (24 FPS)")
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

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("chronon_phrases_%d.sock", time.Now().UnixNano()))
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

	darkColor := []float64{0.08, 0.08, 0.12, 1.0}

	jobs := []PhraseVideoJob{
		// 1. Lower Third Safe (Nome & Ruolo in basso con dissolvenza morbida)
		{
			ID:          "phrase_01_lower_third_safe",
			Title:       "Frase 1 — Lower Third Safe",
			Description: "Posizionamento in basso (terzo inferiore) con morbido fade-in",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   120,
				Text:       "Christopher Nolan — Director",
				Font:       fontInterBold,
				Size:       64,
				Color:      darkColor,
				Position:   "lower_third",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},

		// 2. Clean Slide Up (Headline che risale fluidamente dal basso verso il centro)
		{
			ID:          "phrase_02_clean_slide_up",
			Title:       "Frase 2 — Clean Slide Up",
			Description: "Risalita fluida dal basso con frenata elastica (ease-out)",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   120,
				Text:       "The Future of High-Speed Video",
				Font:       fontInterBold,
				Size:       68,
				Color:      darkColor,
				Position:   "center",
				Animation:  "reveal_from_bottom",
				Opacity:    1.0,
			},
		},

		// 3. Phrase Scale Pop (Impatto con zoom ed espansione dinamica)
		{
			ID:          "phrase_03_scale_pop",
			Title:       "Frase 3 — Scale In Pop",
			Description: "Animazione d'impatto con espansione rapida e assestamento centrale",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   120,
				Text:       "BREAKING NEWS",
				Font:       fontInterBold,
				Size:       76,
				Color:      darkColor,
				Position:   "center",
				Animation:  "scale_drop",
				Opacity:    1.0,
			},
		},

		// 4. Elegant DM Sans Fade In (Citazione elegante e minimale)
		{
			ID:          "phrase_04_dm_sans_fade",
			Title:       "Frase 4 — Elegant Fade In (DM Sans)",
			Description: "Typography raffinata DM Sans con dissolvenza progressiva a tutto schermo",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   120,
				Text:       "Simplicity is the ultimate sophistication.",
				Font:       fontDMBold,
				Size:       60,
				Color:      darkColor,
				Position:   "center",
				Animation:  "fade_in",
				Opacity:    1.0,
			},
		},

		// 5. Kinetic Left Punch (Ingresso dinamico laterale da sinistra)
		{
			ID:          "phrase_05_slide_left_punch",
			Title:       "Frase 5 — Slide Left Punch",
			Description: "Entrata veloce dal lato sinistro con decelerazione progressiva",
			Overlay: overlay.FastEntityOverlay{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   120,
				Text:       "Artificial Intelligence Infrastructure",
				Font:       fontInterBold,
				Size:       62,
				Color:      darkColor,
				Position:   "center",
				Animation:  "slide_in",
				Opacity:    1.0,
			},
		},
	}

	outDir := filepath.Join(cwd, "phrase_preset_videos")
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
		fmt.Printf("[%d/%d] ✨ Rendering Frase: %s\n", idx+1, len(jobs), job.Title)
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
			[]overlay.FastEntityOverlay{job.Overlay},
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

		fmt.Printf("☁️ Caricamento su Google Drive...\n")
		res, err := publisher.Publish(ctx, drive.PublishRequest{
			Name:         videoFileName,
			ContentType:  "video/mp4",
			Path:         videoPath,
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
	_ = os.WriteFile(filepath.Join(outDir, "phrase_presets_summary.json"), summaryBytes, 0644)

	fmt.Println("\n==================================================================")
	fmt.Printf("🏁 COMPLETATI %d VIDEO DEDICATI ALLE FRASI!\n", len(results))
	fmt.Println("==================================================================")
}
