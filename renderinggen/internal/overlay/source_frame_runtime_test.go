package overlay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestSourceFrameRuntimePixels renders one tiny synthetic clip through the
// semantic compiler and the real Chronon CLI. It is opt-in like the other real
// engine tests, uses the software backend, and never creates a GPU workload.
func TestSourceFrameRuntimePixels(t *testing.T) {
	bin := chrononBinFor(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg is required for the one-second source-frame fixture: %v", err)
	}

	assetsRoot := t.TempDir()
	assetPath := filepath.Join(assetsRoot, "assets", "semantic", "frame-source.mp4")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	generate := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=0xD02020:s=640x360:r=24:d=1",
		"-frames:v", "24", "-an", "-c:v", "libx264", "-preset", "ultrafast",
		"-threads", "1", "-pix_fmt", "yuv420p", "-y", assetPath)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate one-second source fixture: %v\n%s", err, tailBytes(output))
	}
	assetBytes, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(assetBytes)

	raw := []byte(fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"source-frame-runtime",
		"video_id":"frame-source",
		"width":640,"height":360,"fps_num":24,"fps_den":1,"duration_ms":1000,
		"source":{"asset_id":"frame-source","path":"assets/semantic/frame-source.mp4","sha256":"%s"},
		"foreground_scale_percent":80,
		"source_frame":{
			"border":{"width_px":8,"color":"#FFFFFF","radius_px":24},
			"shadow":{"color":"#000000","opacity":0.35,"blur_px":8,"offset_x_px":0,"offset_y_px":4}
		},
		"background":{"kind":"color","color":[0.02,0.04,0.2,1]},
		"items":[]
	}`, hex.EncodeToString(digest[:])))
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile source-frame plan: %v", err)
	}
	planPath := filepath.Join(assetsRoot, "frame-plan.json")
	planBytes, err := json.MarshalIndent(compiled.Plan, "", "  ")
	if err != nil {
		t.Fatalf("marshal Chronon plan: %v", err)
	}
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(assetsRoot, "rounded-frame.mp4")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	render := exec.CommandContext(ctx, bin,
		"render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "software", "--encoder-backend", "pipe",
		"--hardware", "none", "--gpu-hot-path-mode", "auto",
		"--encode-preset", "ultrafast", "-o", outputPath)
	render.Dir = assetsRoot
	if output, err := render.CombinedOutput(); err != nil {
		t.Fatalf("Chronon source-frame render: %v\n%s", err, tailBytes(output))
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("Chronon reported success without output: %v", err)
	}

	framePNG := extractFramePNG(t, outputPath, 0)
	frame, err := png.Decode(bytes.NewReader(framePNG))
	if err != nil {
		t.Fatalf("decode rendered frame: %v", err)
	}
	if got := frame.Bounds().Size(); got != image.Pt(640, 360) {
		t.Fatalf("rendered frame is %v, want 640x360", got)
	}

	// At 80% scale, the full-canvas foreground begins at (64,36) and ends
	// at (576,324). Its centered padded plate begins at (56,28). Record the
	// red source extent so the pixel oracle catches scale/placement drift.
	redBounds := image.Rectangle{Min: image.Pt(frame.Bounds().Max.X, frame.Bounds().Max.Y), Max: image.Pt(0, 0)}
	for y := frame.Bounds().Min.Y; y < frame.Bounds().Max.Y; y++ {
		for x := frame.Bounds().Min.X; x < frame.Bounds().Max.X; x++ {
			rgb := averageRGB(frame, image.Pt(x, y), 0)
			if rgb.r > 110 && rgb.r > rgb.g*1.5 && rgb.r > rgb.b*1.5 {
				redBounds.Min.X = min(redBounds.Min.X, x)
				redBounds.Min.Y = min(redBounds.Min.Y, y)
				redBounds.Max.X = max(redBounds.Max.X, x+1)
				redBounds.Max.Y = max(redBounds.Max.Y, y+1)
			}
		}
	}
	t.Logf("red foreground pixel bounds: %v", redBounds)
	whiteBounds := image.Rectangle{Min: image.Pt(frame.Bounds().Max.X, frame.Bounds().Max.Y), Max: image.Pt(0, 0)}
	for y := frame.Bounds().Min.Y; y < frame.Bounds().Max.Y; y++ {
		for x := frame.Bounds().Min.X; x < frame.Bounds().Max.X; x++ {
			rgb := averageRGB(frame, image.Pt(x, y), 0)
			if rgb.r > 190 && rgb.g > 190 && rgb.b > 190 {
				whiteBounds.Min.X = min(whiteBounds.Min.X, x)
				whiteBounds.Min.Y = min(whiteBounds.Min.Y, y)
				whiteBounds.Max.X = max(whiteBounds.Max.X, x+1)
				whiteBounds.Max.Y = max(whiteBounds.Max.Y, y+1)
			}
		}
	}
	t.Logf("white frame pixel bounds: %v; sampled source edge pixel: RGB%v", whiteBounds, averageRGB(frame, image.Pt(65, 180), 0))
	if redBounds != image.Rect(64, 36, 576, 324) {
		t.Errorf("red foreground bounds = %v, want centered 80%% bounds (64,36)-(576,324)", redBounds)
	}
	for _, layer := range compiled.Plan.Layers {
		t.Logf("compiled layer %q size=%v pos=%v scale=%v radius=%v", layer.ID, layer.Size, layer.Position, layer.Scale, layer.Radius)
	}
	for _, point := range []image.Point{{57, 29}, {583, 29}, {57, 331}, {583, 331}} {
		if rgb := averageRGB(frame, point, 1); !(rgb.b > rgb.r*1.25 && rgb.b > rgb.g*1.15) {
			t.Errorf("outer rounded corner at %v = RGB%v, want blue canvas", point, rgb)
		}
	}
	for _, point := range []image.Point{{320, 30}, {320, 330}, {58, 180}, {582, 180}} {
		if rgb := averageRGB(frame, point, 1); !(rgb.r > 190 && rgb.g > 190 && rgb.b > 190) {
			t.Errorf("rounded clip corner at %v = RGB%v, want visible white frame", point, rgb)
		}
	}
	if rgb := averageRGB(frame, image.Pt(100, 70), 1); !(rgb.r > rgb.g*1.5 && rgb.r > rgb.b*1.5) {
		t.Errorf("clip interior = RGB%v, want red source video", rgb)
	}

	// A feathered rounded edge produces at least one mixed red/white pixel on
	// the diagonal arc, rather than a hard binary cut at the mask boundary.
	softEdgePixels := 0
	for y := 38; y <= 64; y++ {
		for x := 66; x <= 92; x++ {
			rgb := averageRGB(frame, image.Pt(x, y), 0)
			if rgb.r > 150 && rgb.g > 55 && rgb.g < 220 && rgb.b > 55 && rgb.b < 220 {
				softEdgePixels++
			}
		}
	}
	t.Logf("mixed rounded edge pixels: %d", softEdgePixels)
	if softEdgePixels == 0 {
		t.Error("rounded clip edge has no mixed red/white pixels; expected feathered coverage")
	}
}

type rgbAverage struct{ r, g, b float64 }

func averageRGB(img image.Image, center image.Point, radius int) rgbAverage {
	bounds := img.Bounds()
	var result rgbAverage
	count := 0.0
	for y := center.Y - radius; y <= center.Y+radius; y++ {
		for x := center.X - radius; x <= center.X+radius; x++ {
			if !image.Pt(x, y).In(bounds) {
				continue
			}
			r, g, b, _ := img.At(x, y).RGBA()
			result.r += float64(r >> 8)
			result.g += float64(g >> 8)
			result.b += float64(b >> 8)
			count++
		}
	}
	if count > 0 {
		result.r /= count
		result.g /= count
		result.b /= count
	}
	return result
}
