package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

type ShowcaseSpec struct {
	Name     string
	PlanJSON string
}

func main() {
	// Binaries and output locations resolve from the environment (or a
	// documented default relative to the working tree), never a developer's
	// absolute home path.
	chrononBin := os.Getenv("CHRONON3D_CLI")
	if chrononBin == "" {
		chrononBin = filepath.Join("Chronon3d", "build", "chronon", "linux-video-release",
			"apps", "chronon3d_cli", "chronon3d_cli")
		if _, err := os.Stat(chrononBin); err != nil {
			log.Fatalf("chronon3d_cli not found at %s: set CHRONON3D_CLI or run from the monorepo root: %v", chrononBin, err)
		}
	}
	if _, err := os.Stat(chrononBin); err != nil {
		log.Fatalf("chronon3d_cli not found at %s: %v", chrononBin, err)
	}

	showcases := []ShowcaseSpec{
		{
			Name: "01_documentary_background.mp4",
			PlanJSON: `{
				"schema_version": "renderinggen.overlay-plan.v1",
				"plan_id": "01_documentary_background",
				"video_id": "showcase-01",
				"width": 1920, "height": 1080,
				"fps_num": 30, "fps_den": 1, "duration_ms": 2500,
				"items": [
					{
						"id": "doc-bg-gradient",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "rect",
							"fill_gradient": {
								"type": "linear",
								"stops": [["#080C14", 0.0], ["#111827", 0.5], ["#1E293B", 1.0]],
								"start": [0.0, 0.0], "end": [0.0, 1.0]
							},
							"effects": [
								{"type": "fractal_noise", "amplitude": 0.05},
								{"type": "vignette"},
								{"type": "noise", "amount": 0.02}
							]
						}
					},
					{
						"id": "doc-grid-overlay",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "grid",
							"grid_spacing": 64.0,
							"stroke": {"color": "#334155", "width": 1.0},
							"opacity": 0.4
						}
					}
				]
			}`,
		},
		{
			Name: "02_media_card.mp4",
			PlanJSON: `{
				"schema_version": "renderinggen.overlay-plan.v1",
				"plan_id": "02_media_card",
				"video_id": "showcase-02",
				"width": 1920, "height": 1080,
				"fps_num": 30, "fps_den": 1, "duration_ms": 2500,
				"items": [
					{
						"id": "card-dark-bg",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "rect",
							"fill": "#070B14",
							"effects": [
								{"type": "vignette"}
							]
						}
					},
					{
						"id": "card-glow-accent",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "rounded_rect",
							"width": 960, "height": 580,
							"radius": 36,
							"fill": "#0F172A",
							"stroke": {"color": "#38BDF8", "width": 4.0},
							"opacity": 0.95
						}
					},
					{
						"id": "card-inner-pill",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "rounded_rect",
							"width": 860, "height": 120,
							"radius": 20,
							"position": [530, 680],
							"fill": "#1E293B",
							"stroke": {"color": "#0284C7", "width": 2.0},
							"opacity": 0.85
						}
					}
				]
			}`,
		},
		{
			Name: "03_world_map_background.mp4",
			PlanJSON: `{
				"schema_version": "renderinggen.overlay-plan.v1",
				"plan_id": "03_world_map_background",
				"video_id": "showcase-03",
				"width": 1920, "height": 1080,
				"fps_num": 30, "fps_den": 1, "duration_ms": 2500,
				"items": [
					{
						"id": "map-bg",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "rect",
							"fill": "#0A0F1E",
							"effects": [
								{"type": "vignette"}
							]
						}
					},
					{
						"id": "map-surface",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "world_map",
							"world_map": {
								"projection": "mercator",
								"center": [12.5, 42.0],
								"zoom": 2.6,
								"highlight": ["IT", "US", "DE", "FR", "JP"],
								"highlight_color": "#00F0FF",
								"route": [[12.5, 41.9], [2.35, 48.85], [-74.0, 40.7]],
								"dots": true
							}
						}
					},
					{
						"id": "map-dotgrid",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "dot_grid",
							"grid_spacing": 48.0,
							"dot_radius": 2.0,
							"fill": "#38BDF8",
							"opacity": 0.35
						}
					}
				]
			}`,
		},
		{
			Name: "04_device_browser.mp4",
			PlanJSON: `{
				"schema_version": "renderinggen.overlay-plan.v1",
				"plan_id": "04_device_browser",
				"video_id": "showcase-04",
				"width": 1920, "height": 1080,
				"fps_num": 30, "fps_den": 1, "duration_ms": 2500,
				"items": [
					{
						"id": "browser-bg",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "rect",
							"fill": "#0B1120",
							"effects": [
								{"type": "vignette"}
							]
						}
					},
					{
						"id": "browser-frame",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "device_frame",
							"device": "browser",
							"width": 1480,
							"height": 880,
							"chrome": true,
							"fill": "#0F172A",
							"stroke": {"color": "#334155", "width": 2.0}
						}
					}
				]
			}`,
		},
		{
			Name: "05_shape_gallery.mp4",
			PlanJSON: `{
				"schema_version": "renderinggen.overlay-plan.v1",
				"plan_id": "05_shape_gallery",
				"video_id": "showcase-05",
				"width": 1920, "height": 1080,
				"fps_num": 30, "fps_den": 1, "duration_ms": 2500,
				"items": [
					{
						"id": "gallery-bg",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "rect",
							"fill_gradient": {
								"type": "linear",
								"stops": [["#090D1A", 0.0], ["#151C2E", 1.0]],
								"start": [0.0, 0.0], "end": [1.0, 1.0]
							},
							"effects": [
								{"type": "vignette"}
							]
						}
					},
					{
						"id": "gallery-star",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "star",
							"points": 5,
							"inner_radius_ratio": 0.45,
							"width": 300, "height": 300,
							"position": [200, 200],
							"fill": "#F59E0B",
							"stroke": {"color": "#FBBF24", "width": 3.0}
						}
					},
					{
						"id": "gallery-polygon",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "polygon",
							"points": 6,
							"width": 280, "height": 280,
							"position": [620, 210],
							"fill": "#10B981",
							"stroke": {"color": "#34D399", "width": 3.0}
						}
					},
					{
						"id": "gallery-ellipse",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "ellipse",
							"width": 320, "height": 220,
							"position": [1020, 240],
							"fill": "#EC4899",
							"stroke": {"color": "#F472B6", "width": 3.0}
						}
					},
					{
						"id": "gallery-arc",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "arc",
							"arc_start_deg": 45.0,
							"arc_sweep_deg": 270.0,
							"arc_thickness": 12.0,
							"width": 260, "height": 260,
							"position": [1440, 220],
							"stroke": {"color": "#38BDF8", "width": 6.0}
						}
					},
					{
						"id": "gallery-arrow",
						"kind": "shape",
						"start_ms": 0, "end_ms": 2500,
						"params": {
							"shape": "arrow",
							"arrow_direction": "right",
							"arrow_head_length": 60,
							"arrow_head_width": 100,
							"arrow_shaft_width": 40,
							"width": 360, "height": 180,
							"position": [780, 680],
							"fill": "#8B5CF6",
							"stroke": {"color": "#A78BFA", "width": 3.0}
						}
					}
				]
			}`,
		},
	}

	outDir := os.Getenv("SHOWCASE_OUT_DIR")
	if outDir == "" {
		outDir = "showcase_renders"
	}
	rootDir := os.Getenv("VELOX_ROOT")
	if rootDir == "" {
		rootDir = "."
	}

	tmpDir, err := os.MkdirTemp("", "showcase-plans-*")
	if err != nil {
		log.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	for i, sc := range showcases {
		fmt.Printf("\n=== [%d/%d] Compiling & Rendering %s ===\n", i+1, len(showcases), sc.Name)

		res, err := overlay.CompileSemantic([]byte(sc.PlanJSON))
		if err != nil {
			log.Fatalf("compile failed for %s: %v", sc.Name, err)
		}

		wireBytes, err := res.Plan.Marshal()
		if err != nil {
			log.Fatalf("marshal failed for %s: %v", sc.Name, err)
		}

		planPath := filepath.Join(tmpDir, fmt.Sprintf("plan_%d.json", i+1))
		if err := os.WriteFile(planPath, wireBytes, 0o644); err != nil {
			log.Fatalf("write plan failed for %s: %v", sc.Name, err)
		}

		destPath := filepath.Join(outDir, sc.Name)
		cmd := exec.Command(chrononBin, "render", "plan", "--plan", planPath, "--backend", "vulkan", "-o", destPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("render failed for %s: %v", sc.Name, err)
		}

		info, err := os.Stat(destPath)
		if err != nil || info.Size() == 0 {
			log.Fatalf("output video %s is missing or empty", destPath)
		}
		fmt.Printf("✓ %s generated successfully (%d bytes)\n", destPath, info.Size())

		// Also copy to root directory for easy access
		rootCopy := filepath.Join(rootDir, sc.Name)
		if err := copyFile(destPath, rootCopy); err != nil {
			log.Printf("warning: failed to copy to root: %v", err)
		} else {
			fmt.Printf("✓ Copied to %s\n", rootCopy)
		}
	}

	fmt.Println("\n========================================================")
	fmt.Println("🎉 All 5 GPU Showcase MP4s rendered successfully!")
	fmt.Println("========================================================")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
