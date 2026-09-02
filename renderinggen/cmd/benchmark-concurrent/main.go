package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/overlay"
)

func main() {
	fmt.Println("==================================================================")
	fmt.Println("🚀 TEST CONCORRENZA: 2 STREAM PARALLELI SU DEMONE CHRONON (24 FPS)")
	fmt.Println("==================================================================")

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	bgVideo := "assets/Pale-Olive.mp4"
	fontInterBold := "assets/fonts/Inter-Bold.ttf"
	assetsRoot := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/RenderingGen/testdata/golden"
	chrononBin := "/home/pierone/src/go-master/projects/Pyt/VeloxEditing/Chronon3d/build/chronon/linux-video-fast-dev/apps/chronon3d_cli/chronon3d_cli"

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("chronon_concurrent_%d.sock", time.Now().UnixNano()))
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

	outDir := filepath.Join(cwd, "concurrent_test_videos")
	_ = os.MkdirAll(outDir, 0755)

	tasks := []struct {
		id    string
		title string
		text  string
	}{
		{"stream_a", "Stream A (Cillian)", "Cillian Murphy — Actor"},
		{"stream_b", "Stream B (Leonardo)", "Leonardo DiCaprio — Actor"},
	}

	var wg sync.WaitGroup
	t0 := time.Now()

	for _, task := range tasks {
		wg.Add(1)
		go func(t struct{ id, title, text string }) {
			defer wg.Done()
			client := chronon.NewIPCClient(socketPath)
			ctx := context.Background()

			videoPath := filepath.Join(outDir, fmt.Sprintf("%s.mp4", t.id))
			planPath := filepath.Join(outDir, fmt.Sprintf("%s_plan.json", t.id))

			plan, _ := overlay.CompileFastEntityOverlays(
				t.id, 1920, 1080, 24, 1, 120, bgVideo,
				[]overlay.FastEntityOverlay{
					{
						Type:       "text",
						StartFrame: 0,
						EndFrame:   120,
						Text:       t.text,
						Font:       fontInterBold,
						Size:       64,
						Color:      []float64{0.08, 0.08, 0.12, 1.0},
						Position:   "lower_third",
						Animation:  "fade_in",
					},
				},
			)
			plan.Output.Path = videoPath
			planBytes, _ := json.MarshalIndent(plan, "", "  ")
			_ = os.WriteFile(planPath, planBytes, 0644)

			req := chronon.RenderRequest{
				PlanPath:   planPath,
				AssetsRoot: assetsRoot,
				OutputPath: videoPath,
				Requirements: chronon.ExecutionRequirements{
					GPURequired:         true,
					CPUFallbackAllowed:  false,
					CompositionRequired: false,
					PacketCopyAllowed:   true,
				},
			}

			subT0 := time.Now()
			err := client.Render(ctx, req)
			dur := time.Since(subT0)
			if err != nil {
				fmt.Printf("❌ %s fallito: %v\n", t.title, err)
			} else {
				fmt.Printf("✓ %-24s completato in %v (~%.1f FPS)\n", t.title, dur, 120.0/dur.Seconds())
			}
		}(task)
	}

	wg.Wait()
	totalWall := time.Since(t0)

	fmt.Println("==================================================================")
	fmt.Printf("🏁 TEST CONCORRENZA COMPLETATO!\n")
	fmt.Printf("   Tempo totale per 2 stream in parallelo: %v\n", totalWall)
	fmt.Printf("   Throughput aggregato: %.1f video/minuto (%.1f FPS aggregati)\n",
		float64(len(tasks))/(totalWall.Minutes()),
		float64(len(tasks)*120)/totalWall.Seconds())
	fmt.Println("==================================================================")
}
