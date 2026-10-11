package overlay

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// groupCanaryAssetsRoot stages two solid image fixtures plus the two newly
// registered fonts in the workspace layout the compiled plan references.
func groupCanaryAssetsRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	repoFonts := filepath.Join(filepath.Dir(source), "..", "..", "assets", "fonts")
	root := t.TempDir()
	writeSolidPNG(t, filepath.Join(root, "assets", "semantic", "member-a.png"), color.RGBA{R: 200, G: 40, B: 40, A: 255})
	writeSolidPNG(t, filepath.Join(root, "assets", "semantic", "member-b.png"), color.RGBA{R: 40, G: 90, B: 220, A: 255})
	fonts := []string{"Oswald.ttf", "AbrilFatface-Regular.ttf", "Inter.ttf", "DejaVuSans.ttf", "Poppins-Bold.ttf"}
	for _, font := range fonts {
		data, err := os.ReadFile(filepath.Join(repoFonts, font))
		if err != nil {
			t.Fatalf("font fixture %s not available under %s: %v", font, repoFonts, err)
		}
		if err := os.MkdirAll(filepath.Join(root, "assets", "fonts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "assets", "fonts", font), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func writeSolidPNG(t *testing.T, path string, c color.RGBA) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestEntityGroupAndNewFontsRenderCanary is the opt-in end-to-end proof for
// the multi-entity closeout: an entity_group duo plus an Oswald phrase and
// an Abril caption render through the real Chronon CLI (software backend)
// into a valid H.264 MP4. It runs only with CHRONON_BIN set and ffmpeg
// present, so a bare `go test ./...` never renders video.
func TestEntityGroupAndNewFontsRenderCanary(t *testing.T) {
	bin := chrononBinFor(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available for canary pixel checks: %v", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("ffprobe not available for canary verification: %v", err)
	}
	assetsRoot := groupCanaryAssetsRoot(t)
	shaA := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "member-a.png"))
	shaB := fixtureSHA256(t, filepath.Join(assetsRoot, "assets", "semantic", "member-b.png"))
	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"group-fonts-canary","video_id":"group-fonts-canary",
		"width":1280,"height":720,"fps_num":24,"fps_den":1,"duration_ms":4000,
		"background":{"kind":"color","color":[0.04,0.05,0.07,1]},
		"items":[{
			"id":"duo","kind":"entity_group","template_id":"ENTITY_GROUP",
			"params":{"group_layout":"duo"},
			"start_ms":0,"end_ms":4000,"duration_ms":4000,
			"asset_refs":[
				{"asset_id":"member-a","sha256":%q,"url":"https://fixtures.example/member-a.png","media_type":"image/png"},
				{"asset_id":"member-b","sha256":%q,"url":"https://fixtures.example/member-b.png","media_type":"image/png"}
			],
			"image_layers":[
				{"id":"a","asset_id":"member-a","start_ms":0,"end_ms":4000,"preset_id":"image_scale_in","motion_id":"image_focus_reveal","motion_params":{"enter_frames":8},"caption":"Ada","caption_font_family":"abril_fatface","params":{"width":420,"height":420}},
				{"id":"b","asset_id":"member-b","start_ms":0,"end_ms":4000,"preset_id":"image_scale_in","motion_id":"image_scale_reveal","motion_params":{"enter_frames":8},"caption":"Grace","params":{"width":420,"height":420}}
			]
		},{
			"id":"headline","kind":"important_phrase","template_id":"IMPORTANT_PHRASE","preset_id":"phrase_default",
			"text":"Duo in Oswald","params":{"font_family":"oswald"},
			"start_ms":500,"end_ms":3500,"duration_ms":3000
		}]
	}`, shaA, shaB))
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile group-fonts canary plan: %v", err)
	}
	var fonts []string
	for _, layer := range compiled.Plan.Layers {
		if layer.Style != nil && layer.Style.Font != "" {
			fonts = append(fonts, layer.Style.Font)
		}
	}
	joined := strings.Join(fonts, ",")
	if !strings.Contains(joined, "assets/fonts/Oswald.ttf") {
		t.Fatalf("no Oswald phrase layer in canary plan: %s", joined)
	}
	if !strings.Contains(joined, "assets/fonts/AbrilFatface-Regular.ttf") {
		t.Fatalf("no Abril caption layer in canary plan: %s", joined)
	}
	planBytes, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatalf("marshal canary Chronon plan: %v", err)
	}
	planPath := filepath.Join(t.TempDir(), "group-fonts-plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(t.TempDir(), "group-fonts-canary.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin,
		"render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "software", "--encoder-backend", "pipe",
		"--hardware", "none", "--gpu-hot-path-mode", "auto",
		"--encode-preset", "ultrafast", "-o", videoPath)
	cmd.Dir = assetsRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Chronon group-fonts render failed: %v\n%s", err, tailBytes(output))
	}
	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height,duration",
		"-of", "default=noprint_wrappers=1", videoPath)
	probeOut, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe group-fonts canary: %v\n%s", err, probeOut)
	}
	for _, want := range []string{"codec_name=h264", "width=1280", "height=720"} {
		if !strings.Contains(string(probeOut), want) {
			t.Fatalf("canary probe missing %q:\n%s", want, probeOut)
		}
	}
}
