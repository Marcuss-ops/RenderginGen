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

type phrasePreset struct {
	ID          string
	Preset      string
	Text        string
	Description string
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	assetsRoot := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/RenderingGen/testdata/golden"
	chrononBin := os.Getenv("CHRONON_BINARY")
	if chrononBin == "" {
		chrononBin = "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/Chronon3d/build/chronon/linux-video-fast-dev/apps/chronon3d_cli/chronon3d_cli"
	}
	outDir := filepath.Join(root, "highend_phrase_videos")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		panic(err)
	}

	presets := []phrasePreset{
		{"01_kinetic_split_word", "kinetic_split_word", "KINETIC PERFORMANCE", "word split with overshoot"},
		{"02_dynamic_island_expansion", "dynamic_island_expansion", "NOW PLAYING", "compact expansion"},
		{"03_masked_upward_reveal", "masked_upward_reveal", "BUILT FOR SPEED", "masked upward reveal"},
		{"04_staggered_char_float", "staggered_char_float", "EVERY FRAME MATTERS", "staggered character float"},
		{"05_high_specular_light_sweep", "high_specular_light_sweep", "TITANIUM ENGINE", "specular tracking sweep"},
		{"06_depth_of_field_rack_focus", "depth_of_field_rack_focus", "FOCUS ON THE SIGNAL", "rack focus blur"},
		{"07_micro_tracker_kerning_compression", "micro_tracker_kerning_compression", "PRECISION TYPOGRAPHY", "kerning compression"},
		{"08_isometric_3d_fold", "isometric_3d_fold", "SPATIAL COMPUTING", "fold-in depth approximation"},
		{"09_soft_edge_spotlight_dissolve", "soft_edge_spotlight_dissolve", "A SOFT REVEAL", "soft dissolve"},
		{"10_chromatic_aberration_pop", "chromatic_aberration_pop", "IMPACT", "chromatic pop offset"},
		{"11_fluid_gradient_text_flow", "fluid_gradient_text_flow", "FLUID MOTION", "flowing tracking"},
		{"12_velocity_inertia_snap", "velocity_inertia_snap", "FAST. THEN EXACT.", "inertia snap"},
		{"13_vertical_rolling_counter", "vertical_rolling_counter", "327% GROWTH", "rolling glyph counter"},
		{"14_glassmorphism_card_tilt", "glassmorphism_card_tilt", "GLASS / LIGHT / DEPTH", "soft card tilt pop"},
		{"15_pixel_grid_alpha_matrix", "pixel_grid_alpha_matrix", "MATRIX ASSEMBLY", "pixel-grid approximation"},
	}

	ctx := context.Background()
	socket := filepath.Join(os.TempDir(), fmt.Sprintf("chronon_highend_%d.sock", time.Now().UnixNano()))
	daemon := exec.Command(chrononBin, "daemon", "-s", socket, "-a", assetsRoot, "--backend", "auto")
	if err := daemon.Start(); err != nil {
		panic(fmt.Errorf("start daemon: %w", err))
	}
	defer func() {
		_ = daemon.Process.Kill()
		_ = os.Remove(socket)
	}()
	ready := false
	for i := 0; i < 80; i++ {
		time.Sleep(100 * time.Millisecond)
		if conn, err := net.Dial("unix", socket); err == nil {
			_ = conn.Close()
			ready = true
			break
		}
	}
	if !ready {
		panic("chronon daemon not ready")
	}
	client := chronon.NewIPCClient(socket)

	for i, p := range presets {
		fmt.Printf("[%02d/%02d] %s\n", i+1, len(presets), p.Preset)
		planPath := filepath.Join(outDir, p.ID+"_plan.json")
		videoPath := filepath.Join(outDir, p.ID+"_1920x1080_24fps_5s.mp4")
		plan, err := overlay.CompileFastEntityOverlays(
			"highend_phrase_"+p.ID, 1920, 1080, 24, 1, 120, "Pale-Olive.mp4",
			[]overlay.FastEntityOverlay{{
				Type: "text", StartFrame: 0, EndFrame: 120, Text: p.Text,
				Font: "fonts/Inter-Bold.ttf", Size: 72,
				Color: []float64{0.08, 0.08, 0.12, 1}, Position: "center",
				PresetID: p.Preset, Opacity: 1,
			}},
		)
		if err != nil {
			panic(fmt.Errorf("compile %s: %w", p.Preset, err))
		}
		plan.Output.Path = videoPath
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(planPath, data, 0644); err != nil {
			panic(err)
		}
		req := chronon.RenderRequest{PlanPath: planPath, AssetsRoot: assetsRoot, OutputPath: videoPath,
			Requirements: chronon.ExecutionRequirements{GPURequired: true, CPUFallbackAllowed: false, PacketCopyAllowed: true}}
		if err := client.Render(ctx, req); err != nil {
			panic(fmt.Errorf("render %s: %w", p.Preset, err))
		}
	}
	fmt.Printf("rendered %d high-end phrase presets in %s\n", len(presets), outDir)
}
