package dateanim

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRenderMaterializedDateAnimation is the opt-in end-to-end gate: editorial
// text -> stable style choice -> rewritten plan with the real dates -> real
// chronon3d_cli -> MP4. It is skipped unless both the renderer and the
// ChrononTemplate assets root are explicitly available. Set DATEANIM_E2E_OUT
// to keep the plans and MP4s for a visual review.
func TestRenderMaterializedDateAnimation(t *testing.T) {
	if os.Getenv("RENDERINGGEN_DATE_ANIM_E2E") != "1" {
		t.Skip("set RENDERINGGEN_DATE_ANIM_E2E=1 to run the real date-animation render")
	}
	binary := os.Getenv("CHRONON_BIN")
	assetsRoot := os.Getenv("CHRONON_TEMPLATE_ROOT")
	if binary == "" || assetsRoot == "" {
		t.Skip("CHRONON_BIN and CHRONON_TEMPLATE_ROOT are required for the real render")
	}
	outRoot := os.Getenv("DATEANIM_E2E_OUT")
	cases := []struct {
		id     string
		dates  []string
		frames string
		fps    string
	}{
		{"01_timeline_progress", []string{"2007", "2024"}, "120", "24/1"},
		{"02_year_morph", []string{"1998", "2007", "2013", "2024"}, "240", "24/1"},
		{"07_split_date", []string{"1998", "2007", "2013", "2024"}, "240", "24/1"},
		{"12_rolling_year", []string{"1998", "2007", "2013", "2024"}, "320", "30/1"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			style, ok := StyleByID(tc.id)
			if !ok {
				t.Fatalf("style %s is missing", tc.id)
			}
			materialized, err := MaterializeStyle(style, tc.dates)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if outRoot != "" {
				dir = filepath.Join(outRoot, tc.id)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			planPath := filepath.Join(dir, tc.id+".plan.json")
			videoPath := filepath.Join(dir, tc.id+".mp4")
			if err := os.WriteFile(planPath, materialized.PlanBytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(binary, "render-plan",
				"--input", planPath,
				"--output", videoPath,
				"--assets-root", assetsRoot,
				"--backend", "vulkan",
				"--rate-control", "crf",
				"--crf", "14",
				"--encode-preset", "slow",
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("render: %v\n%s", err, output)
			}
			info, err := os.Stat(videoPath)
			if err != nil || info.Size() == 0 {
				t.Fatalf("rendered MP4 is missing or empty: %v", err)
			}
			probe := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
				"-show_entries", "stream=width,height,r_frame_rate,nb_frames", "-of", "json", videoPath)
			raw, err := probe.Output()
			if err != nil {
				t.Fatalf("ffprobe: %v", err)
			}
			var result struct {
				Streams []struct {
					Width      int    `json:"width"`
					Height     int    `json:"height"`
					FrameRate  string `json:"r_frame_rate"`
					FrameCount string `json:"nb_frames"`
				} `json:"streams"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Streams) != 1 {
				t.Fatalf("streams = %d, want 1", len(result.Streams))
			}
			stream := result.Streams[0]
			if stream.Width != 1920 || stream.Height != 1080 {
				t.Fatalf("canvas = %dx%d, want 1920x1080", stream.Width, stream.Height)
			}
			if stream.FrameRate != tc.fps {
				t.Fatalf("fps = %q, want %s", stream.FrameRate, tc.fps)
			}
			if stream.FrameCount != tc.frames {
				t.Fatalf("frames = %q, want %s", stream.FrameCount, tc.frames)
			}
		})
	}
}
