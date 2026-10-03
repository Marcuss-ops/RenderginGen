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

func main() {
	fmt.Println("=== Runtime Test Clip Renderer (User Video + Solid Overlay) ===")

	workDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		log.Fatalf("resolve repo root: %v", err)
	}

	outDir := filepath.Join(repoRoot, "out", "runtime_clips")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatalf("create outDir: %v", err)
	}

	absChronon := filepath.Join(repoRoot, "Chronon3d", ".tmp", "chronon-builds", "linux-video-fast-dev", "apps", "chronon3d_cli", "chronon3d_cli")
	if _, err := os.Stat(absChronon); err != nil {
		log.Fatalf("chronon3d_cli not found at %s: %v", absChronon, err)
	}

	userVideoSrc := filepath.Join(repoRoot, "assets", "user_test_video.mp4")
	sourceRelPath := filepath.Join("assets", "semantic", "user-drive-clip.mp4")
	fixtureRoot := filepath.Join(outDir, "fixture_root")
	sourceAbsPath := filepath.Join(fixtureRoot, sourceRelPath)
	if err := os.MkdirAll(filepath.Dir(sourceAbsPath), 0o755); err != nil {
		log.Fatalf("create fixture assets dir: %v", err)
	}

	sourceBytes, err := os.ReadFile(userVideoSrc)
	if err != nil {
		log.Fatalf("failed to read user video fixture %s: %v", userVideoSrc, err)
	}
	if err := os.WriteFile(sourceAbsPath, sourceBytes, 0o644); err != nil {
		log.Fatalf("failed to copy user video fixture: %v", err)
	}
	sourceDigest := sha256.Sum256(sourceBytes)
	sourceHex := hex.EncodeToString(sourceDigest[:])
	fmt.Printf("User video ready: %s (SHA256: %s..., %d bytes)\n", sourceRelPath, sourceHex[:16], len(sourceBytes))

	// Semantic plan definition
	// 15 seconds, 24 fps, 360 frames
	planDoc := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "user_runtime_clip_test",
		"video_id":       "user-drive-clip",
		"width":          1920,
		"height":         1080,
		"fps_num":        24,
		"fps_den":        1,
		"duration_ms":    15000,
		"source": map[string]any{
			"asset_id": "user-drive-clip",
			"path":     sourceRelPath,
			"sha256":   sourceHex,
		},
		"foreground_scale_percent": 75,
		"source_frame": map[string]any{
			"clip_radius_px": 42.0,
			"stroke": map[string]any{
				"width_px": 6.0,
				"color":    "#38BDF8",
			},
			"shadow": map[string]any{
				"color":       "#000000",
				"opacity":     0.65,
				"blur_px":     26.0,
				"offset_x_px": 0.0,
				"offset_y_px": 14.0,
			},
		},
		"background": map[string]any{
			"kind":  "color",
			"color": []float64{0.05, 0.07, 0.12, 1.0},
		},
		"items": []any{},
	}

	planJSON, err := json.Marshal(planDoc)
	if err != nil {
		log.Fatalf("marshal plan doc: %v", err)
	}

	compiled, err := overlay.CompileSemantic(planJSON)
	if err != nil {
		log.Fatalf("compile semantic plan: %v", err)
	}

	chrononPlanBytes, err := json.MarshalIndent(compiled.Plan, "", "  ")
	if err != nil {
		log.Fatalf("marshal chronon plan: %v", err)
	}

	planPath := filepath.Join(outDir, "user_runtime_clip_test.plan.json")
	if err := os.WriteFile(planPath, chrononPlanBytes, 0o644); err != nil {
		log.Fatalf("write plan file: %v", err)
	}
	fmt.Printf("Chronon plan compiled and saved: %s\n", planPath)

	videoOnlyMP4 := filepath.Join(outDir, "user_runtime_clip_video_only.mp4")
	finalMuxedMP4 := filepath.Join(outDir, "user_runtime_clip_final.mp4")

	// Render with Chronon3D CLI using auto backend (GPU acceleration via Vulkan + GPU encoder)
	fmt.Println("\nRendering video with Chronon3D CLI (GPU Vulkan accelerated)...")
	renderCmd := exec.Command(absChronon,
		"render", "--plan", planPath,
		"--assets-root", fixtureRoot,
		"--backend", "auto",
		"--encoder-backend", "pipe",
		"--encode-preset", "ultrafast",
		"-o", videoOnlyMP4)
	renderCmd.Dir = workDir
	renderCmd.Stdout = os.Stdout
	renderCmd.Stderr = os.Stderr

	if err := renderCmd.Run(); err != nil {
		log.Fatalf("render failed: %v", err)
	}

	vStat, err := os.Stat(videoOnlyMP4)
	if err != nil || vStat.Size() == 0 {
		log.Fatalf("rendered video %s is missing or empty", videoOnlyMP4)
	}
	fmt.Printf("✓ Video stream rendered: %s (%d bytes)\n", videoOnlyMP4, vStat.Size())

	// Mux audio from original video
	fmt.Println("\nMuxing original audio into final MP4...")
	muxCmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "warning",
		"-i", videoOnlyMP4,
		"-i", userVideoSrc,
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", "192k",
		"-map", "0:v:0",
		"-map", "1:a:0?",
		"-shortest",
		"-y", finalMuxedMP4)
	if out, err := muxCmd.CombinedOutput(); err != nil {
		log.Printf("warning: audio mux failed (%v): %s - falling back to video only", err, string(out))
		finalMuxedMP4 = videoOnlyMP4
	} else {
		fmt.Printf("✓ Final MP4 with audio created: %s\n", finalMuxedMP4)
	}

	// Extract frame 72 (3 seconds in) as preview PNG
	framePNG := filepath.Join(outDir, "user_runtime_clip_preview.frame.png")
	extractCmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-ss", "00:00:03.000", "-i", finalMuxedMP4,
		"-vframes", "1", "-y", framePNG)
	if output, err := extractCmd.CombinedOutput(); err != nil {
		log.Printf("warning: frame extract failed: %v\n%s", err, string(output))
	} else {
		fmt.Printf("✓ Frame preview extracted: %s\n", framePNG)
	}

	fmt.Println("\n========================================================")
	fmt.Println("🎉 User runtime test clip successfully created!")
	fmt.Println("========================================================")
}
