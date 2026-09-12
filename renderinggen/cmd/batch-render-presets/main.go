package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

type PresetJob struct {
	Index        int
	ID           string
	Filename     string
	PlanFilename string
	PresetID     string
	Text         string
	OutDir       string
}

func main() {
	var (
		dryRun       = flag.Bool("dry-run", false, "Compile plans only, do not render")
		doUpload     = flag.Bool("upload", true, "Upload rendered videos to Google Drive")
		concurrency  = flag.Int("concurrency", 3, "Number of concurrent renders")
		folderID     = flag.String("folder", "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS", "Drive folder ID")
		credPath     = flag.String("credentials", "", "Path to credentials.json")
		tokenPath    = flag.String("token", "", "Path to token.json")
		chrononBin   = flag.String("chronon-bin", "", "Path to chronon3d_cli")
		assetsRoot   = flag.String("assets-root", "", "Path to golden assets root")
		uploadBin    = flag.String("drive-upload-bin", "", "Path to drive-upload binary")
		onlyPreset   = flag.String("only", "", "Render only specific preset ID")
		backend      = flag.String("backend", "vulkan", "Render backend (vulkan, software)")
		hardware     = flag.String("hardware", "nvenc", "Hardware encoder (nvenc, none)")
		encodePreset = flag.String("encode-preset", "p1", "Encode preset (p1, ultrafast)")
		socketPath   = flag.String("socket", "", "Render through warm Chronon3d daemons on this UNIX socket base; empty spawns one CLI process per video")
		gpuDevice    = flag.Uint("gpu-device", 0, "Vulkan device index for the daemons started by -socket")
		daemonCount  = flag.Int("daemons", 3, "Warm daemons to spread jobs across, one socket each")
		daemonLanes  = flag.Int("daemon-lanes", 1, "Concurrent RENDER_JOBs per daemon; its device scheduler rejects more than two and degrades latency once they overlap")
	)
	flag.Parse()

	baseDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	repoRoot := baseDir
	for {
		if _, err := os.Stat(filepath.Join(repoRoot, "RenderingGen")); err == nil {
			break
		}
		parent := filepath.Dir(repoRoot)
		if parent == repoRoot {
			repoRoot = baseDir
			break
		}
		repoRoot = parent
	}

	renderingGenDir := filepath.Join(repoRoot, "RenderingGen")
	if *chrononBin == "" {
		*chrononBin = filepath.Join(repoRoot, "Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli")
	}
	if *assetsRoot == "" {
		*assetsRoot = filepath.Join(renderingGenDir, "testdata/golden")
	}
	if *uploadBin == "" {
		*uploadBin = filepath.Join(renderingGenDir, "bin/drive-upload")
	}
	if *credPath == "" {
		*credPath = filepath.Join(renderingGenDir, "infra/docker/credentials.json")
	}
	if *tokenPath == "" {
		*tokenPath = filepath.Join(renderingGenDir, "infra/docker/token.json")
	}

	phraseDir := filepath.Join(renderingGenDir, "renderinggen/highend_phrase_videos")
	typewriterDir := filepath.Join(renderingGenDir, "typewriter_phrase_videos")
	_ = os.MkdirAll(phraseDir, 0o755)
	_ = os.MkdirAll(typewriterDir, 0o755)

	allJobs := []PresetJob{
		// 15 High-End Animated Phrases
		{
			ID:           "highend_phrase_01_kinetic_split_word",
			Filename:     "01_kinetic_split_word_1920x1080_24fps_5s.mp4",
			PlanFilename: "01_kinetic_split_word_plan.json",
			PresetID:     "kinetic_split_word",
			Text:         "KINETIC PERFORMANCE",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_02_dynamic_island_expansion",
			Filename:     "02_dynamic_island_expansion_1920x1080_24fps_5s.mp4",
			PlanFilename: "02_dynamic_island_expansion_plan.json",
			PresetID:     "dynamic_island_expansion",
			Text:         "NOW PLAYING",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_03_masked_upward_reveal",
			Filename:     "03_masked_upward_reveal_1920x1080_24fps_5s.mp4",
			PlanFilename: "03_masked_upward_reveal_plan.json",
			PresetID:     "masked_upward_reveal",
			Text:         "BUILT FOR SPEED",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_04_staggered_char_float",
			Filename:     "04_staggered_char_float_1920x1080_24fps_5s.mp4",
			PlanFilename: "04_staggered_char_float_plan.json",
			PresetID:     "staggered_char_float",
			Text:         "EVERY FRAME MATTERS",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_05_high_specular_light_sweep",
			Filename:     "05_high_specular_light_sweep_1920x1080_24fps_5s.mp4",
			PlanFilename: "05_high_specular_light_sweep_plan.json",
			PresetID:     "high_specular_light_sweep",
			Text:         "TITANIUM ENGINE",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_06_depth_of_field_rack_focus",
			Filename:     "06_depth_of_field_rack_focus_1920x1080_24fps_5s.mp4",
			PlanFilename: "06_depth_of_field_rack_focus_plan.json",
			PresetID:     "depth_of_field_rack_focus",
			Text:         "FOCUS ON THE SIGNAL",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_07_micro_tracker_kerning_compression",
			Filename:     "07_micro_tracker_kerning_compression_1920x1080_24fps_5s.mp4",
			PlanFilename: "07_micro_tracker_kerning_compression_plan.json",
			PresetID:     "micro_tracker_kerning_compression",
			Text:         "PRECISION TYPOGRAPHY",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_08_isometric_3d_fold",
			Filename:     "08_isometric_3d_fold_1920x1080_24fps_5s.mp4",
			PlanFilename: "08_isometric_3d_fold_plan.json",
			PresetID:     "isometric_3d_fold",
			Text:         "SPATIAL COMPUTING",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_09_soft_edge_spotlight_dissolve",
			Filename:     "09_soft_edge_spotlight_dissolve_1920x1080_24fps_5s.mp4",
			PlanFilename: "09_soft_edge_spotlight_dissolve_plan.json",
			PresetID:     "soft_edge_spotlight_dissolve",
			Text:         "A SOFT REVEAL",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_10_chromatic_aberration_pop",
			Filename:     "10_chromatic_aberration_pop_1920x1080_24fps_5s.mp4",
			PlanFilename: "10_chromatic_aberration_pop_plan.json",
			PresetID:     "chromatic_aberration_pop",
			Text:         "IMPACT",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_11_fluid_gradient_text_flow",
			Filename:     "11_fluid_gradient_text_flow_1920x1080_24fps_5s.mp4",
			PlanFilename: "11_fluid_gradient_text_flow_plan.json",
			PresetID:     "fluid_gradient_text_flow",
			Text:         "FLUID MOTION",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_12_velocity_inertia_snap",
			Filename:     "12_velocity_inertia_snap_1920x1080_24fps_5s.mp4",
			PlanFilename: "12_velocity_inertia_snap_plan.json",
			PresetID:     "velocity_inertia_snap",
			Text:         "FAST. THEN EXACT.",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_13_vertical_rolling_counter",
			Filename:     "13_vertical_rolling_counter_1920x1080_24fps_5s.mp4",
			PlanFilename: "13_vertical_rolling_counter_plan.json",
			PresetID:     "vertical_rolling_counter",
			Text:         "327% GROWTH",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_14_glassmorphism_card_tilt",
			Filename:     "14_glassmorphism_card_tilt_1920x1080_24fps_5s.mp4",
			PlanFilename: "14_glassmorphism_card_tilt_plan.json",
			PresetID:     "glassmorphism_card_tilt",
			Text:         "GLASS / LIGHT / DEPTH",
			OutDir:       phraseDir,
		},
		{
			ID:           "highend_phrase_15_pixel_grid_alpha_matrix",
			Filename:     "15_pixel_grid_alpha_matrix_1920x1080_24fps_5s.mp4",
			PlanFilename: "15_pixel_grid_alpha_matrix_plan.json",
			PresetID:     "pixel_grid_alpha_matrix",
			Text:         "MATRIX ASSEMBLY",
			OutDir:       phraseDir,
		},

		// 5 Typewriter Animations
		{
			ID:           "typewriter_01_clean",
			Filename:     "01_typewriter_clean_1920x1080_24fps_5s.mp4",
			PlanFilename: "01_typewriter_clean_plan.json",
			PresetID:     "phrase_typewriter_clean",
			Text:         "EVERY PIXEL MATTERS",
			OutDir:       typewriterDir,
		},
		{
			ID:           "typewriter_02_pop",
			Filename:     "02_typewriter_pop_1920x1080_24fps_5s.mp4",
			PlanFilename: "02_typewriter_pop_plan.json",
			PresetID:     "phrase_typewriter_pop",
			Text:         "CREATIVE REVOLUTION",
			OutDir:       typewriterDir,
		},
		{
			ID:           "typewriter_03_neon",
			Filename:     "03_typewriter_neon_1920x1080_24fps_5s.mp4",
			PlanFilename: "03_typewriter_neon_plan.json",
			PresetID:     "phrase_typewriter_neon",
			Text:         "FUTURE OF MOTION",
			OutDir:       typewriterDir,
		},
		{
			ID:           "typewriter_04_tracking",
			Filename:     "04_typewriter_tracking_1920x1080_24fps_5s.mp4",
			PlanFilename: "04_typewriter_tracking_plan.json",
			PresetID:     "phrase_typewriter_tracking",
			Text:         "TIMELESS TYPOGRAPHY",
			OutDir:       typewriterDir,
		},
		{
			ID:           "typewriter_05_glitch",
			Filename:     "05_typewriter_glitch_1920x1080_24fps_5s.mp4",
			PlanFilename: "05_typewriter_glitch_plan.json",
			PresetID:     "phrase_typewriter_glitch",
			Text:         "MAXIMUM PERFORMANCE",
			OutDir:       typewriterDir,
		},
	}

	var jobs []PresetJob
	for i, j := range allJobs {
		j.Index = i + 1
		if *onlyPreset != "" && j.PresetID != *onlyPreset && j.Filename != *onlyPreset {
			continue
		}
		jobs = append(jobs, j)
	}

	totalStart := time.Now()
	var (
		completedCount int32
		failedCount    int32
		uploadMutex    sync.Mutex
		wg             sync.WaitGroup
		jobChan        = make(chan PresetJob, len(jobs))
	)

	numWorkers := *concurrency
	if numWorkers < 1 {
		numWorkers = 1
	}
	if numWorkers > len(jobs) {
		numWorkers = len(jobs)
	}

	for _, j := range jobs {
		jobChan <- j
	}
	close(jobChan)

	log.Printf("Starting batch render of %d jobs with %d workers...", len(jobs), numWorkers)

	// Render transport. By default every video is a fresh chronon3d_cli
	// process, which pays the full cold start — Vulkan device init, font/image
	// caches, surface pools, NVENC open — before the first frame. With -socket
	// the batch drives a persistent daemon instead and reuses that warm engine
	// for every job, so the per-video cost collapses to the frames themselves.
	var renderer chronon.Renderer
	daemonCleanup := func() {}
	if *socketPath != "" {
		handles, err := ensureDaemonPool(*socketPath, *daemonCount, *chrononBin, *assetsRoot, *backend, *gpuDevice)
		if err != nil {
			log.Fatalf("daemon: %v", err)
		}
		sockets := make([]string, 0, len(handles))
		for _, h := range handles {
			sockets = append(sockets, h.socketPath)
		}
		lanes := *daemonLanes
		if lanes < 1 {
			lanes = 1
		}
		renderer = newDaemonPool(sockets, lanes)
		// Shut the daemons WE started back down and reap them. Idempotent
		// because both the failure path and the normal exit call it, and it
		// never touches a daemon that was already serving its socket.
		var cleanupOnce sync.Once
		daemonCleanup = func() {
			cleanupOnce.Do(func() {
				for _, h := range handles {
					if h.cmd == nil {
						continue
					}
					shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
					if err := chronon.NewIPCClient(h.socketPath).Shutdown(shutdownCtx); err != nil {
						log.Printf("WARN: daemon %s shutdown: %v", h.socketPath, err)
						_ = h.cmd.Process.Kill()
					}
					shutdownCancel()
					_ = h.cmd.Wait()
				}
			})
		}
		log.Printf("Transport: %d warm daemon(s) %s.0..%d (%d render lane(s) each)",
			len(handles), *socketPath, len(handles)-1, lanes)
	} else {
		log.Printf("Transport: chronon3d_cli subprocess per video (cold engine every job)")
	}

	// buildRenderRequest is the transport-neutral job description. The GPU
	// contract travels as semantic Requirements + HardwareEncoder, never as
	// hand-built flags, so the daemon resolves the encoder through the same
	// authority (resolveNativeEncodeSelection) the CLI arguments come from and
	// the two transports cannot drift into different encode lanes.
	buildRenderRequest := func(planPath, videoPath string) chronon.RenderRequest {
		gpu := *hardware != "" && *hardware != chronon.HardwareEncoderNone
		req := chronon.RenderRequest{
			PlanPath:        planPath,
			AssetsRoot:      *assetsRoot,
			OutputPath:      videoPath,
			EncodePreset:    *encodePreset,
			HardwareEncoder: *hardware,
			Requirements: chronon.ExecutionRequirements{
				Backend:     *backend,
				GPURequired: gpu,
				// The batch accepts the engine's own hot-path mode, so the
				// selection matches what the CLI transport emits today.
				CPUFallbackAllowed: true,
			},
		}
		if !gpu {
			// No native encoder requested: the host pipe lane carries the frame
			// and libx264 rejects the NVENC-only pN presets, so none is sent.
			req.Output.PipePixFmt = "rgba"
		}
		return req
	}

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobChan {
				log.Printf("[W%d] [%d/%d] Starting %s (%s)...", workerID, job.Index, len(allJobs), job.ID, job.PresetID)

				// 1. Build semantic plan JSON
				rawSemantic := fmt.Sprintf(`{
					"schema_version": "renderinggen.overlay-plan.v1",
					"plan_id": %q,
					"video_id": %q,
					"width": 1920,
					"height": 1080,
					"fps_num": 24,
					"fps_den": 1,
					"duration_ms": 5000,
					"background": {
						"kind": "color",
						"color": [0.9333333333333333, 0.9450980392156862, 0.9058823529411765, 1.0]
					},
					"items": [
						{
							"id": "item_1",
							"template_id": "IMPORTANT_PHRASE",
							"preset_id": %q,
							"text": %q,
							"start_ms": 0,
							"end_ms": 5000
						}
					]
				}`, job.ID, job.ID, job.PresetID, job.Text)

				// 2. Compile semantic to chronon plan
				compileResult, err := overlay.CompileSemantic([]byte(rawSemantic))
				if err != nil {
					log.Fatalf("[W%d] FAIL compile semantic for %s: %v", workerID, job.ID, err)
				}
				plan := compileResult.Plan
				videoPath := filepath.Join(job.OutDir, job.Filename)
				plan.Output.Path = videoPath

				// 3. Write compiled plan
				planPath := filepath.Join(job.OutDir, job.PlanFilename)
				planData, err := json.MarshalIndent(plan, "", "  ")
				if err != nil {
					log.Fatalf("[W%d] FAIL marshal plan for %s: %v", workerID, job.ID, err)
				}
				if err := os.WriteFile(planPath, planData, 0o644); err != nil {
					log.Fatalf("[W%d] FAIL write plan file %s: %v", workerID, planPath, err)
				}

				if *dryRun {
					log.Printf("[W%d] [dry-run] Compiled plan written to %s", workerID, planPath)
					continue
				}

				// 4. Render: through the warm daemon when one is configured, else
				// with one chronon3d_cli subprocess.
				renderStart := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				var detail string
				var renderErr error
				if renderer != nil {
					renderErr = renderer.Render(ctx, buildRenderRequest(planPath, videoPath))
				} else {
					renderArgs := []string{
						"render", "--plan", planPath,
						"--assets-root", *assetsRoot,
						"--backend", *backend,
						"--hardware", *hardware,
						"--encode-preset", *encodePreset,
					}
					if *backend == "software" {
						renderArgs = append(renderArgs, "--encoder-backend", "pipe", "--pipe-pixfmt", "rgba")
					}
					renderArgs = append(renderArgs, "-o", videoPath)
					cmd := exec.CommandContext(ctx, *chrononBin, renderArgs...)
					cmd.Dir = *assetsRoot

					out, err := cmd.CombinedOutput()
					detail, renderErr = string(out), err
				}
				cancel()
				if renderErr != nil {
					// Fail this job without tearing the transport down: Fatalf would
					// exit past the daemon cleanup, and stopping the daemon mid-flight
					// also fails every peer render sharing it.
					log.Printf("[W%d] FAIL render %s: %v\nOutput: %s", workerID, job.ID, renderErr, detail)
					atomic.AddInt32(&failedCount, 1)
					continue
				}
				renderDur := time.Since(renderStart)
				log.Printf("[W%d] Rendered %s in %.2fs (%.1f fps)", workerID, job.ID, renderDur.Seconds(), 120.0/renderDur.Seconds())

				// 5. Structural probe & decode verification
				verifyMP4(videoPath, 120, 1920, 1080)
				atomic.AddInt32(&completedCount, 1)

				// 6. Upload to Google Drive if requested (mutex for clean log output)
				if *doUpload {
					uploadMutex.Lock()
					uploadToDrive(*uploadBin, *credPath, *tokenPath, *folderID, videoPath, job.Filename)
					uploadMutex.Unlock()
				}
			}
		}(w)
	}

	wg.Wait()
	// Release an owned daemon before reporting, so the GPU is free even when
	// jobs failed and the process is about to exit non-zero.
	daemonCleanup()
	if failed := atomic.LoadInt32(&failedCount); failed > 0 {
		log.Fatalf("ALL DONE WITH FAILURES! Rendered %d/%d videos, %d failed, in %s",
			atomic.LoadInt32(&completedCount), len(jobs), failed, time.Since(totalStart).Round(time.Second))
	}
	log.Printf("ALL DONE! Rendered %d/%d videos in %s", completedCount, len(jobs), time.Since(totalStart).Round(time.Second))
}

func verifyMP4(videoPath string, wantFrames int, wantW, wantH int) {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-select_streams", "v:0",
		"-count_frames",
		"-show_entries", "stream=width,height,nb_read_frames",
		"-of", "csv=p=0",
		videoPath).Output()
	if err != nil {
		log.Fatalf("verifyMP4 ffprobe failed for %s: %v", videoPath, err)
	}
	parts := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(parts) < 3 {
		log.Fatalf("verifyMP4 parse failed for %s: got %q", videoPath, string(out))
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	n, _ := strconv.Atoi(parts[2])
	if w != wantW || h != wantH || n != wantFrames {
		log.Fatalf("verifyMP4 %s invalid: got %dx%d, %d frames; want %dx%d, %d frames",
			videoPath, w, h, n, wantW, wantH, wantFrames)
	}

	// Full ffmpeg bitstream decode
	decCmd := exec.Command("ffmpeg", "-v", "error", "-i", videoPath, "-f", "null", "-")
	decOut, err := decCmd.CombinedOutput()
	if err != nil {
		log.Fatalf("verifyMP4 decode error for %s: %v\n%s", videoPath, err, string(decOut))
	}
}

// daemonHandle is one daemon serving one socket. cmd is nil when the socket was
// already served and this batch therefore does not own — and must not stop —
// that daemon.
type daemonHandle struct {
	socketPath string
	cmd        *exec.Cmd
}

// daemonPool spreads render jobs across N warm daemons.
//
// One daemon is not enough: its device scheduler admits only a couple of
// concurrent RENDER_JOBs, rejects the rest outright, and degrades each session
// towards realtime once two overlap — so a single daemon serializes the batch
// and loses the overlap N CLI processes had. N single-lane daemons give both a
// warm engine on every job and the parallelism of the subprocess transport.
type daemonPool struct {
	lanes []chronon.Renderer
	next  atomic.Uint64
}

func newDaemonPool(sockets []string, lanesPerDaemon int) *daemonPool {
	pool := &daemonPool{lanes: make([]chronon.Renderer, 0, len(sockets))}
	for _, socketPath := range sockets {
		pool.lanes = append(pool.lanes,
			chronon.LimitConcurrency(chronon.NewIPCClient(socketPath), lanesPerDaemon))
	}
	return pool
}

func (p *daemonPool) Render(ctx context.Context, req chronon.RenderRequest) error {
	if len(p.lanes) == 0 {
		return fmt.Errorf("daemon pool has no lanes")
	}
	// Round-robin: jobs are spread evenly, and a lane that is mid-render only
	// queues the next job assigned to it instead of stalling the whole pool.
	index := p.next.Add(1) - 1
	return p.lanes[index%uint64(len(p.lanes))].Render(ctx, req)
}

// ensureDaemonPool makes sure `count` daemons are serving `<socketBase>.<i>`.
// A socket that is already served is reused and left unowned; the rest are
// started here and waited for, so the returned handles are immediately usable.
func ensureDaemonPool(socketBase string, count int, binary, assetsRoot, backend string, gpuDevice uint) ([]daemonHandle, error) {
	if count < 1 {
		count = 1
	}
	if err := os.MkdirAll(filepath.Dir(socketBase), 0o755); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	handles := make([]daemonHandle, 0, count)
	for i := 0; i < count; i++ {
		socketPath := fmt.Sprintf("%s.%d", socketBase, i)
		if _, err := os.Stat(socketPath); err == nil {
			// Already served: reuse it, and never shut down a daemon we did not
			// start.
			handles = append(handles, daemonHandle{socketPath: socketPath})
			continue
		}
		cmd, err := startDaemon(socketPath, binary, assetsRoot, backend, gpuDevice)
		if err != nil {
			for _, h := range handles {
				if h.cmd != nil {
					_ = h.cmd.Process.Kill()
				}
			}
			return nil, err
		}
		handles = append(handles, daemonHandle{socketPath: socketPath, cmd: cmd})
	}
	return handles, nil
}

// startDaemon launches one daemon on socketPath and waits for its socket.
func startDaemon(socketPath, binary, assetsRoot, backend string, gpuDevice uint) (*exec.Cmd, error) {
	args := []string{"daemon", "-s", socketPath, "-a", assetsRoot, "--backend", backend}
	if backend != "software" {
		args = append(args, "--gpu-device", strconv.FormatUint(uint64(gpuDevice), 10))
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start daemon on %s: %w", socketPath, err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			return cmd, nil
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil, fmt.Errorf("daemon on %s exited before creating its socket", socketPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("daemon socket %s did not appear within 60s", socketPath)
}

func uploadToDrive(uploadBin, credPath, tokenPath, folderID, filePath, fileName string) {
	uploadStart := time.Now()
	cmd := exec.Command(uploadBin,
		"-credentials", credPath,
		"-token", tokenPath,
		"-folder", folderID,
		"-file", filePath,
		"-name", fileName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("upload %s failed: %v\n%s", fileName, err, string(out))
	}
	log.Printf("  Uploaded %s in %.2fs: %s", fileName, time.Since(uploadStart).Seconds(), strings.TrimSpace(string(out)))
}
