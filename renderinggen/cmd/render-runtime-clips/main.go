package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

type ClipScenario struct {
	ID          string
	Name        string
	Description string
	SourceFrame map[string]any
}

func main() {
	chrononBin := os.Getenv("CHRONON_BIN")
	if chrononBin == "" {
		chrononBin = os.Getenv("CHRONON3D_CLI")
	}
	if chrononBin == "" {
		chrononBin = filepath.Join("..", "..", "Chronon3d", ".tmp", "chronon-builds", "linux-video-fast-dev", "apps", "chronon3d_cli", "chronon3d_cli")
	}
	absChronon, err := filepath.Abs(chrononBin)
	if err != nil {
		log.Fatalf("resolve chronon path: %v", err)
	}
	if _, err := os.Stat(absChronon); err != nil {
		log.Fatalf("chronon3d_cli not found at %s: %v", absChronon, err)
	}

	outDir, err := filepath.Abs(filepath.Join("..", "..", "out", "runtime_clips"))
	if err != nil {
		log.Fatalf("resolve out dir: %v", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatalf("create out dir: %v", err)
	}

	workDir := filepath.Join(outDir, "fixture_root")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		log.Fatalf("failed to create fixture dir: %v", err)
	}

	// 1. Copy the real local documentary video fixture: 1920x1080 30fps
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		log.Fatalf("resolve repo root: %v", err)
	}
	realVideoSrc := filepath.Join(repoRoot, "01_documentary_background.mp4")
	sourceRelPath := filepath.Join("assets", "semantic", "real-documentary-clip.mp4")
	sourceAbsPath := filepath.Join(workDir, sourceRelPath)
	if err := os.MkdirAll(filepath.Dir(sourceAbsPath), 0o755); err != nil {
		log.Fatalf("create assets dir: %v", err)
	}

	sourceBytes, err := os.ReadFile(realVideoSrc)
	if err != nil {
		log.Fatalf("failed to read real video fixture %s: %v", realVideoSrc, err)
	}
	if err := os.WriteFile(sourceAbsPath, sourceBytes, 0o644); err != nil {
		log.Fatalf("failed to copy real video fixture: %v", err)
	}
	sourceDigest := sha256.Sum256(sourceBytes)
	sourceHex := hex.EncodeToString(sourceDigest[:])
	fmt.Printf("Real video fixture ready: %s (SHA256: %s..., %d bytes)\n", sourceRelPath, sourceHex[:16], len(sourceBytes))

	// 2. Define the scenarios to demonstrate with the real video
	scenarios := []ClipScenario{
		{
			ID:          "clip_01_rounded_corners",
			Name:        "01_clip_rounded_corners.mp4",
			Description: "Real Clip con Bounding Box a Bordi Smussati (clip_radius_px: 50)",
			SourceFrame: map[string]any{
				"clip_radius_px": 50.0,
			},
		},
		{
			ID:          "clip_02_stroke_outline",
			Name:        "02_clip_stroke_outline.mp4",
			Description: "Real Clip con Stroke/Outline perimetrale (stroke: width 8px #00F0FF)",
			SourceFrame: map[string]any{
				"stroke": map[string]any{
					"width_px": 8.0,
					"color":    "#00F0FF",
				},
			},
		},
		{
			ID:          "clip_03_rounded_stroke_shadow",
			Name:        "03_clip_rounded_stroke_shadow.mp4",
			Description: "Real Clip Completa: Bordi Smussati (clip_radius_px: 50) + Stroke (width 8px #FFD700) + Ombra Morbida",
			SourceFrame: map[string]any{
				"clip_radius_px": 50.0,
				"stroke": map[string]any{
					"width_px": 8.0,
					"color":    "#FFD700",
				},
				"shadow": map[string]any{
					"color":       "#000000",
					"opacity":     0.70,
					"blur_px":     30.0,
					"offset_y_px": 15.0,
				},
			},
		},
	}

	for i, sc := range scenarios {
		fmt.Printf("\n======================================================\n")
		fmt.Printf("[%d/%d] Rendering: %s\n", i+1, len(scenarios), sc.Description)
		fmt.Printf("======================================================\n")

		planDoc := map[string]any{
			"schema_version": "renderinggen.overlay-plan.v1",
			"plan_id":        sc.ID,
			"video_id":       "real-documentary-clip",
			"width":          1920,
			"height":         1080,
			"fps_num":        30,
			"fps_den":        1,
			"duration_ms":    2000,
			"source": map[string]any{
				"asset_id": "real-documentary-clip",
				"path":     sourceRelPath,
				"sha256":   sourceHex,
			},
			"foreground_scale_percent": 80,
			"source_frame":            sc.SourceFrame,
			"background": map[string]any{
				"kind":  "color",
				"color": []float64{0.06, 0.09, 0.16, 1.0},
			},
			"items": []any{},
		}

		planJSON, err := json.Marshal(planDoc)
		if err != nil {
			log.Fatalf("marshal plan doc: %v", err)
		}

		compiled, err := overlay.CompileSemantic(planJSON)
		if err != nil {
			log.Fatalf("compile semantic plan for %s: %v", sc.ID, err)
		}

		chrononPlanBytes, err := json.MarshalIndent(compiled.Plan, "", "  ")
		if err != nil {
			log.Fatalf("marshal chronon plan: %v", err)
		}

		planPath := filepath.Join(outDir, fmt.Sprintf("%s.plan.json", sc.ID))
		if err := os.WriteFile(planPath, chrononPlanBytes, 0o644); err != nil {
			log.Fatalf("write plan file: %v", err)
		}

		outMP4 := filepath.Join(outDir, sc.Name)

		// Render with chronon3d_cli using software backend for full stroke & mask fidelity
		renderCmd := exec.Command(absChronon,
			"render", "--plan", planPath,
			"--assets-root", workDir,
			"--backend", "software",
			"--encoder-backend", "pipe",
			"--encode-preset", "ultrafast",
			"-o", outMP4)
		renderCmd.Dir = workDir
		renderCmd.Stdout = os.Stdout
		renderCmd.Stderr = os.Stderr

		if err := renderCmd.Run(); err != nil {
			log.Fatalf("render failed for %s: %v", sc.Name, err)
		}

		stat, err := os.Stat(outMP4)
		if err != nil || stat.Size() == 0 {
			log.Fatalf("rendered output %s is missing or empty", outMP4)
		}
		fmt.Printf("✓ MP4 rendered successfully: %s (%d bytes)\n", outMP4, stat.Size())

		// Extract frame 24 (1 second in) as PNG to view
		framePNG := filepath.Join(outDir, fmt.Sprintf("%s.frame.png", sc.ID))
		extractCmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
			"-ss", "00:00:01.000", "-i", outMP4,
			"-vframes", "1", "-y", framePNG)
		if output, err := extractCmd.CombinedOutput(); err != nil {
			log.Printf("warning: frame extract failed: %v\n%s", err, string(output))
		} else {
			fmt.Printf("✓ Frame preview extracted: %s\n", framePNG)
		}
	}

	fmt.Println("\n========================================================")
	fmt.Printf("🎉 All %d runtime clips rendered successfully in: %s\n", len(scenarios), outDir)
	fmt.Println("========================================================")
}
