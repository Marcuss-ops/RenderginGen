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
		dryRun      = flag.Bool("dry-run", false, "Compile plans only, do not render")
		doUpload    = flag.Bool("upload", true, "Upload rendered videos to Google Drive")
		concurrency = flag.Int("concurrency", 3, "Number of concurrent renders")
		folderID    = flag.String("folder", "1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS", "Drive folder ID")
		credPath    = flag.String("credentials", "", "Path to credentials.json")
		tokenPath   = flag.String("token", "", "Path to token.json")
		chrononBin  = flag.String("chronon-bin", "", "Path to chronon3d_cli")
		assetsRoot  = flag.String("assets-root", "", "Path to golden assets root")
		uploadBin   = flag.String("drive-upload-bin", "", "Path to drive-upload binary")
		onlyPreset  = flag.String("only", "", "Render only specific preset ID")
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

				// 4. Render with chronon3d_cli
				renderStart := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				cmd := exec.CommandContext(ctx, *chrononBin,
					"render", "--plan", planPath,
					"--assets-root", *assetsRoot,
					"--backend", "software",
					"--encoder-backend", "pipe",
					"--hardware", "none",
					"--encode-preset", "ultrafast",
					"-o", videoPath)
				cmd.Dir = *assetsRoot

				out, err := cmd.CombinedOutput()
				cancel()
				if err != nil {
					log.Fatalf("[W%d] FAIL render %s: %v\nOutput: %s", workerID, job.ID, err, string(out))
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
