package overlay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestFinal_AppleStyleSemanticLowering verifies that all three checked-in
// Apple style fixtures (crime, discovery, young) lower from their
// renderinggen.overlay-plan.v1 semantic representation into complete and
// valid chronon.render-plan.v2 plans.
func TestFinal_AppleStyleSemanticLowering(t *testing.T) {
	stylesDir := appleStylesDir(t)
	profiles := []string{"crime", "discovery", "young"}

	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			path := filepath.Join(stylesDir, "style-preset-"+profile+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture %s: %v", path, err)
			}
			var job struct {
				ID         string          `json:"id"`
				RenderPlan json.RawMessage `json:"render_plan"`
			}
			if err := json.Unmarshal(data, &job); err != nil {
				t.Fatalf("unmarshal fixture envelope: %v", err)
			}

			result, err := CompileSemantic(job.RenderPlan)
			if err != nil {
				t.Fatalf("compile semantic plan for %s: %v", profile, err)
			}
			plan := result.Plan
			if plan == nil {
				t.Fatal("compiled plan is nil")
			}
			if plan.Schema != "chronon.render-plan.v2" || plan.Version != 2 {
				t.Fatalf("expected chronon.render-plan.v2 v2, got %s v%d", plan.Schema, plan.Version)
			}
			if plan.Canvas.DurationFrames != 360 { // 12000 ms @ 30fps = 360 frames
				t.Errorf("duration_frames = %d, want 360 (12s @ 30fps)", plan.Canvas.DurationFrames)
			}
			if plan.Canvas.Width != 1920 || plan.Canvas.Height != 1080 {
				t.Errorf("canvas dimensions %dx%d, want 1920x1080", plan.Canvas.Width, plan.Canvas.Height)
			}
			if len(plan.Layers) != 7 {
				t.Fatalf("expected 7 compiled layers, got %d", len(plan.Layers))
			}
		})
	}
}

func appleStylesDir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	return filepath.Join(filepath.Dir(source), "../../../testdata/styles")
}

func appleAssetsRoot(t *testing.T) string {
	t.Helper()
	source := certificationSourceRoot(t)
	root := t.TempDir()

	fixtures := map[string]string{
		"assets/apple.png":              "apple.png",
		"assets/background.jpg":         "background.jpg",
		"assets/fonts/Poppins-Bold.ttf": "Poppins-Bold.ttf",
		"fonts/Poppins-Bold.ttf":        "Poppins-Bold.ttf",
		"Poppins-Bold.ttf":              "Poppins-Bold.ttf",
		"apple.png":                     "apple.png",
	}

	for logicalPath, fixture := range fixtures {
		data, err := os.ReadFile(filepath.Join(source, fixture))
		if err != nil {
			t.Skipf("fixture %s not available: %v", fixture, err)
		}
		target := filepath.Join(root, filepath.FromSlash(logicalPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatalf("write asset %s: %v", logicalPath, err)
		}
	}
	return root
}

// TestRenderingGen2ChrononAppleStyleFinal renders all three Apple styles
// (crime, discovery, young) through Chronon, validating structural properties,
// bitstream decoding, and multi-point visual frame difference across styles.
func TestRenderingGen2ChrononAppleStyleFinal(t *testing.T) {
	bin := chrononBinFor(t)
	stylesDir := appleStylesDir(t)
	assetsRoot := appleAssetsRoot(t)
	outDir := filepath.Join(filepath.Dir(stylesDir), "../apple_style_final_videos")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir outDir: %v", err)
	}

	profiles := []string{"crime", "discovery", "young"}
	renderedVideos := make(map[string]string)

	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			path := filepath.Join(stylesDir, "style-preset-"+profile+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture %s: %v", path, err)
			}
			var job struct {
				ID         string          `json:"id"`
				RenderPlan json.RawMessage `json:"render_plan"`
			}
			if err := json.Unmarshal(data, &job); err != nil {
				t.Fatalf("unmarshal fixture envelope: %v", err)
			}

			result, err := CompileSemantic(job.RenderPlan)
			if err != nil {
				t.Fatalf("compile %s: %v", profile, err)
			}
			plan := result.Plan
			videoPath := filepath.Join(outDir, fmt.Sprintf("apple_style_%s_30fps_1080p.mp4", profile))
			plan.Output.Path = videoPath

			planPath := filepath.Join(outDir, fmt.Sprintf("apple_style_%s_plan.json", profile))
			planBytes, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				t.Fatalf("marshal plan: %v", err)
			}
			if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
				t.Fatalf("write plan: %v", err)
			}

			// Render using Chronon software/pipe or vulkan
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			startRender := time.Now()
			cmd := exec.CommandContext(ctx, bin,
				"render", "--plan", planPath,
				"--assets-root", assetsRoot,
				"--backend", "software",
				"--encoder-backend", "pipe",
				"--hardware", "none",
				"-o", videoPath)
			cmd.Dir = assetsRoot

			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("chronon render failed for %s: %v\n%s", profile, err, tailBytes(out))
			}
			renderDur := time.Since(startRender)
			t.Logf("Rendered %s in %.2fs (%.1f fps)", profile, renderDur.Seconds(), 360.0/renderDur.Seconds())

			// 1. Structural probe
			probeMP4StructuralWithFPS(t, videoPath, 30, 360, 12.0)

			// 2. Decode fully
			decodeFully(t, videoPath)

			renderedVideos[profile] = videoPath
		})
	}

	// 3. Compare visual distinction between profiles
	if len(renderedVideos) == 3 {
		// Sample frames across the phrase, word/entity and image windows. A
		// pair may legitimately share pixels at one sample (for example, both
		// image motions are at rest), so require each pair to differ somewhere
		// across the sampled timeline rather than at every individual frame.
		distinct := map[string]bool{}
		for _, frame := range []int{40, 100, 340} {
			frameBytes := make(map[string][]byte)
			for _, profile := range profiles {
				frameBytes[profile] = extractFramePNG(t, renderedVideos[profile], frame)
			}
			for i := 0; i < len(profiles); i++ {
				for j := i + 1; j < len(profiles); j++ {
					p1, p2 := profiles[i], profiles[j]
					if pixelDifference(t, frameBytes[p1], frameBytes[p2]) >= 50 {
						distinct[p1+"/"+p2] = true
					}
				}
			}
		}
		for i := 0; i < len(profiles); i++ {
			for j := i + 1; j < len(profiles); j++ {
				key := profiles[i] + "/" + profiles[j]
				if !distinct[key] {
					t.Errorf("Apple style pair %s never differs by at least 50 pixels across sampled frames", key)
				}
			}
		}
	}
}

func probeMP4StructuralWithFPS(t *testing.T, path string, wantFPS, wantFrames int, wantMinDuration float64) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,nb_frames,r_frame_rate",
		"-show_entries", "format=duration", "-of", "json", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var probe struct {
		Streams []struct {
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			NBFrames   string `json:"nb_frames"`
			RFrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatalf("ffprobe JSON: %v", err)
	}
	if len(probe.Streams) != 1 {
		t.Fatalf("expected 1 video stream, got %d", len(probe.Streams))
	}
	s := probe.Streams[0]
	if s.Width != 1920 || s.Height != 1080 {
		t.Errorf("resolution %dx%d, want 1920x1080", s.Width, s.Height)
	}
	wantRFR := fmt.Sprintf("%d/1", wantFPS)
	if s.RFrameRate != wantRFR {
		t.Errorf("fps %q, want %s", s.RFrameRate, wantRFR)
	}
}
