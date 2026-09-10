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
	Text        string
	PresetID    string
}

func renderWithDaemon(chrononBin, assetsRoot, planPath, videoPath string) error {
	_ = os.Remove(videoPath)
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("chronon_job_%d.sock", time.Now().UnixNano()))
	logPath := filepath.Join(os.TempDir(), fmt.Sprintf("chronon_daemon_%d.log", time.Now().UnixNano()))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer func() {
		_ = logFile.Close()
		_ = os.Remove(socketPath)
	}()

	cmd := exec.Command(chrononBin, "daemon", "-s", socketPath, "-a", assetsRoot, "--backend", "software")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ready := false
	for i := 0; i < 60; i++ {
		time.Sleep(100 * time.Millisecond)
		if conn, err := net.Dial("unix", socketPath); err == nil {
			_ = conn.Close()
			ready = true
			break
		}
	}
	if !ready {
		return fmt.Errorf("daemon socket ready timeout")
	}

	client := chronon.NewIPCClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	req := chronon.RenderRequest{
		PlanPath:   planPath,
		AssetsRoot: assetsRoot,
		OutputPath: videoPath,
		Report:     false,
	}

	if err := client.Render(ctx, req); err != nil {
		return fmt.Errorf("daemon render error: %w", err)
	}

	fi, err := os.Stat(videoPath)
	if err != nil {
		return fmt.Errorf("output video not found: %w", err)
	}
	if fi.Size() < 100*1024 {
		return fmt.Errorf("rendered video too small (%d bytes), background video missing", fi.Size())
	}

	return nil
}

func verifyFrameCentering(videoPath string) (bool, string) {
	outPNG := filepath.Join(os.TempDir(), fmt.Sprintf("verify_%d.png", time.Now().UnixNano()))
	defer os.Remove(outPNG)

	cmd := exec.Command("ffmpeg", "-y", "-ss", "00:00:02.500", "-i", videoPath, "-vframes", "1", outPNG)
	if err := cmd.Run(); err != nil {
		return false, fmt.Sprintf("ffmpeg extract error: %v", err)
	}

	checkScript := fmt.Sprintf(`
from PIL import Image
import numpy as np, sys

img = np.array(Image.open('%s'))
mean = float(img.mean())
if mean < 180:
    print(f"FAIL: Mean pixel intensity is too low ({mean:.1f}), background missing or black!")
    sys.exit(1)

mask = np.mean(img, axis=2) < 180
ys, xs = np.where(mask)
if len(xs) == 0:
    print("FAIL: No text detected on frame!")
    sys.exit(2)

cx = (float(xs.min()) + float(xs.max())) / 2.0
cy = (float(ys.min()) + float(ys.max())) / 2.0
print(f"OK: mean={mean:.1f}, text_center=({cx:.1f}, {cy:.1f}), bbox_x=[{xs.min()}, {xs.max()}], bbox_y=[{ys.min()}, {ys.max()}]")
if abs(cy - 540) > 80:
    print(f"FAIL: Text is not vertically centered! (cy={cy:.1f}, expected ~540)")
    sys.exit(3)
sys.exit(0)
`, outPNG)

	pyCmd := exec.Command("python3", "-c", checkScript)
	out, err := pyCmd.CombinedOutput()
	if err != nil {
		return false, string(out)
	}
	return true, string(out)
}

func main() {
	fmt.Println("==================================================================")
	fmt.Println("✨ RENDERING 5 NUOVE ANIMAZIONI OVERLAY TYPEWRITER PER LE FRASI")
	fmt.Println("   Canvas: 1920x1080 @ 24fps (5 secondi / 120 frames)")
	fmt.Println("   Background: Pale-Olive.mp4 (Pale Olive Classico)")
	fmt.Println("   Destinazione: Google Drive (1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS)")
	fmt.Println("==================================================================")

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	bgVideo := "Pale-Olive.mp4"
	assetsRoot := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/RenderingGen/testdata/golden"

	chrononBin := os.Getenv("CHRONON_BINARY")
	if chrononBin == "" {
		chrononBin = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli"
	}

	folderID := "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS"
	credsFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/credentials.json"
	tokenFile := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored/token.json"

	ctx := context.Background()
	publisher, err := drive.NewGoogleOAuth(ctx, credsFile, tokenFile, folderID)
	if err != nil {
		panic(fmt.Errorf("Drive init failed: %w", err))
	}

	durationFrames := int64(120)

	jobs := []PhraseVideoJob{
		{
			ID:          "01_typewriter_clean",
			Title:       "Typewriter 1 — Clean Terminal",
			Description: "Macchina da scrivere classica digitale a scatto netto su ogni singolo carattere",
			Text:        "EVERY PIXEL MATTERS",
			PresetID:    "phrase_typewriter_clean",
		},
		{
			ID:          "02_typewriter_pop",
			Title:       "Typewriter 2 — Kinetic Pop & Bounce",
			Description: "Caduta dall'alto e micro-rimbalzo cinetico elastico mentre ogni lettera si materializza",
			Text:        "CREATIVE REVOLUTION",
			PresetID:    "phrase_typewriter_pop",
		},
		{
			ID:          "03_typewriter_neon",
			Title:       "Typewriter 3 — Cyber Glow & Blur",
			Description: "Effetto cyber-digitale con dissolvenza dal bagliore sfocato a nitido e glow turchese",
			Text:        "FUTURE OF MOTION",
			PresetID:    "phrase_typewriter_neon",
		},
		{
			ID:          "04_typewriter_tracking",
			Title:       "Typewriter 4 — Kinetic Tracking Drift",
			Description: "Tipografia editoriale raffinata con compressione della crenatura e scorrimento fluido",
			Text:        "TIMELESS TYPOGRAPHY",
			PresetID:    "phrase_typewriter_tracking",
		},
		{
			ID:          "05_typewriter_glitch",
			Title:       "Typewriter 5 — Glitch Impact Punch",
			Description: "Scatto laterale ad alto impatto con deformazione asimmetrica e ombra vermiglio",
			Text:        "MAXIMUM PERFORMANCE",
			PresetID:    "phrase_typewriter_glitch",
		},
	}

	outDir := filepath.Join(cwd, "typewriter_phrase_videos")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		panic(err)
	}

	type Result struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Preset   string  `json:"preset"`
		FileID   string  `json:"file_id"`
		DriveURL string  `json:"drive_url"`
		Sec      float64 `json:"sec"`
		SizeKB   float64 `json:"size_kb"`
	}
	var results []Result

	for idx, job := range jobs {
		fmt.Printf("\n------------------------------------------------------------------\n")
		fmt.Printf("[%d/%d] 🎬 Rendering Frase Typewriter: %s\n", idx+1, len(jobs), job.Title)
		fmt.Printf("   Preset: %s | Testo: %q\n", job.PresetID, job.Text)
		fmt.Printf("   Descrizione: %s\n", job.Description)

		videoFileName := fmt.Sprintf("%s_1920x1080_24fps_5s.mp4", job.ID)
		videoPath := filepath.Join(outDir, videoFileName)
		planPath := filepath.Join(outDir, fmt.Sprintf("%s_plan.json", job.ID))

		plan, err := overlay.CompileFastEntityOverlays(
			"typewriter_"+job.ID,
			1920, 1080,
			24, 1,
			durationFrames,
			bgVideo,
			[]overlay.FastEntityOverlay{{
				Type:       "text",
				StartFrame: 0,
				EndFrame:   durationFrames,
				Text:       job.Text,
				Font:       "fonts/Inter-Bold.ttf",
				Position:   "center",
				PresetID:   job.PresetID,
				Opacity:    1.0,
			}},
		)
		if err != nil {
			fmt.Printf("❌ Compile error: %v\n", err)
			continue
		}
		plan.Output.Path = videoPath

		planBytes, _ := json.MarshalIndent(plan, "", "  ")
		_ = os.WriteFile(planPath, planBytes, 0644)

		t0 := time.Now()
		if err := renderWithDaemon(chrononBin, assetsRoot, planPath, videoPath); err != nil {
			fmt.Printf("❌ Render failed: %v\n", err)
			continue
		}
		renderSec := time.Since(t0).Seconds()
		fi, _ := os.Stat(videoPath)
		sizeKB := float64(fi.Size()) / 1024.0
		fmt.Printf("✓ Render completato in %.2fs (~%.1f FPS) | Dimensione: %.1f KB\n", renderSec, float64(durationFrames)/renderSec, sizeKB)

		ok, verifyMsg := verifyFrameCentering(videoPath)
		if !ok {
			fmt.Printf("⚠️ Controllo qualità fallito: %s\n", verifyMsg)
		} else {
			fmt.Printf("🎯 Controllo qualità OK: %s", verifyMsg)
		}

		if os.Getenv("RENDERINGGEN_SKIP_UPLOAD") == "1" {
			fmt.Printf("⏭️ Upload saltato (RENDERINGGEN_SKIP_UPLOAD=1)\n")
			continue
		}
		fmt.Printf("☁️ Caricamento su Google Drive (%s)...\n", videoFileName)
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

		fmt.Printf("🎉 DRIVE UPLOAD OK! File ID: %s\n   Link: %s\n", res.FileID, res.WebViewLink)
		results = append(results, Result{
			ID:       job.ID,
			Title:    job.Title,
			Preset:   job.PresetID,
			FileID:   res.FileID,
			DriveURL: res.WebViewLink,
			Sec:      renderSec,
			SizeKB:   sizeKB,
		})
	}

	summaryBytes, _ := json.MarshalIndent(results, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "typewriter_summary.json"), summaryBytes, 0644)

	fmt.Println("\n==================================================================")
	fmt.Printf("🏁 COMPLETATI E CARICATI %d/%d VIDEO OVERLAY TYPEWRITER!\n", len(results), len(jobs))
	fmt.Println("==================================================================")
}
