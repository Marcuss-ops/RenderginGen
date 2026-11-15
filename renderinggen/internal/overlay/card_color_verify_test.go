package overlay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestCardColorStaysNeutral renders a masked card from SMPTE bars. The old
// software-decode path turned the neutral gray bar into (200,85,64); the
// native surface + MaskNode download must keep it neutral.
func TestCardColorStaysNeutral(t *testing.T) {
	bin := chrononBinFor(t)
	root := "/tmp/card-color-verify"
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "assets", "semantic", "card-source", "source.mp4")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "smptebars=size=1920x1080:rate=24",
		"-t", "3", "-pix_fmt", "yuv420p", "-c:v", "libx264",
		"-preset", "ultrafast", "-y", source)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate bars: %v\n%s", err, tailBytes(out))
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	raw := fmt.Sprintf(`{
		"schema_version":"renderinggen.overlay-plan.v1",
		"plan_id":"card-color-neutral","video_id":"card-source",
		"width":1920,"height":1080,"fps_num":24,"fps_den":1,"duration_ms":1500,
		"source":{"asset_id":"card-source","path":"assets/semantic/card-source/source.mp4","sha256":"%s"},
		"foreground_scale_percent":78,
		"source_frame":{
			"border":{"width_px":8,"color":"#EDEDED","radius_px":28},
			"shadow":{"color":"#0A0A0A","opacity":0.85,"blur_px":30,"offset_x_px":0,"offset_y_px":12},
			"stroke":{"width_px":2,"color":"#FFFFFF"},
			"clip_radius_px":20
		},
		"background":{"kind":"video","asset_refs":[{"asset_id":"card-source","sha256":"%s","url":"assets/semantic/card-source/source.mp4","media_type":"video/mp4"}],"fit":"cover","loop":true},
		"items":[]
	}`, sha, sha)
	compiled, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	plan := compiled.Plan
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(outDir, "card.mp4")
	plan.Output.Path = video
	planBytes, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(outDir, "plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "render", "--plan", planPath,
		"--assets-root", root, "--backend", "vulkan",
		"--encoder-backend", "native", "--hardware", "nvenc",
		"--gpu-hot-path-mode", "require_gpu_native",
		"--encode-preset", "p1", "--rate-control", "qp", "--qp", "18",
		"-o", video)
	cmd.Dir = root
	out, renderErr := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(outDir, "render.log"), out, 0o644)
	if renderErr != nil {
		t.Fatalf("render: %v\n%s", renderErr, tailBytes(out))
	}
	t.Logf("root=%s video=%s", root, video)
	frame := filepath.Join(outDir, "frame-1s.png")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-ss", "1", "-i", video, "-frames:v", "1", "-y", frame).CombinedOutput(); err != nil {
		t.Fatalf("extract: %v\n%s", err, tailBytes(out))
	}
	img := goal3DecodePNG(t, mustRead(t, frame), "card")
	// First SMPTE bar (neutral gray) inside the 78% card, above the subtitles.
	gr, gg0, gb, _ := img.At(300, 200).RGBA()
	gray := [3]int{int(gr >> 8), int(gg0 >> 8), int(gb >> 8)}
	t.Logf("gray bar = %v", gray)
	if maxInt(gray[0], gray[1], gray[2])-minInt(gray[0], gray[1], gray[2]) > 16 {
		t.Fatalf("card gray bar is not neutral: %v", gray)
	}
	// Second SMPTE bar is yellow. The old chroma-corrupted frame rendered it
	// red (R high, G zero), so this sample discriminates the fix.
	r, g, b, _ := img.At(500, 200).RGBA()
	rr, ggg, bb := int(r>>8), int(g>>8), int(b>>8)
	t.Logf("yellow bar = (%d,%d,%d)", rr, ggg, bb)
	if rr < 140 || ggg < 140 || bb > 90 || absInt(rr-ggg) > 40 {
		t.Fatalf("card yellow bar is wrong: (%d,%d,%d)", rr, ggg, bb)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func maxInt(a, b, c int) int {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}

func minInt(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
