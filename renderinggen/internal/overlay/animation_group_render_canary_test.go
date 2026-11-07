package overlay

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAnimationGroupHeterogeneousRenderCanary is the opt-in end-to-end proof
// that one group can own a phrase, an image and a single date card through the
// real Chronon CLI. It runs only with CHRONON_BIN set and ffmpeg/ffprobe
// present, so a bare `go test ./...` never renders video.
func TestAnimationGroupHeterogeneousRenderCanary(t *testing.T) {
	bin := chrononBinFor(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available for canary pixel checks: %v", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("ffprobe not available for canary verification: %v", err)
	}
	assetsRoot := groupCanaryAssetsRoot(t)
	imagePath := filepath.Join(assetsRoot, "assets", "semantic", "member-a.png")
	imageSHA := fixtureSHA256(t, imagePath)

	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "animation-group-canary", "video_id": "animation-group-canary",
		"width": 1280, "height": 720, "fps_num": 24, "fps_den": 1, "duration_ms": 4000,
		"background": map[string]any{"kind": "color", "color": []any{0.04, 0.05, 0.07, 1}},
		"items": []any{
			map[string]any{
				"id": "headline", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
				"preset_id": "phrase_default", "text": "Duisburg 1948",
				"params":   map[string]any{"font_family": "oswald"},
				"start_ms": 0, "end_ms": 4000,
			},
			map[string]any{
				"id": "portrait", "kind": "entity_image", "template_id": "image_popup",
				"preset_id": "image_focus_in", "start_ms": 0, "end_ms": 4000,
				"asset_refs": []any{map[string]any{
					"asset_id": "portrait", "sha256": imageSHA,
					"url": "https://fixtures.example/member-a.png", "media_type": "image/png",
				}},
			},
			map[string]any{
				"id": "date", "kind": "number", "template_id": "TIMELINE_DATE_CARD",
				"preset_id": "phrase_default", "text": "1948",
				"date":     map[string]any{"value": "1948"},
				"params":   map[string]any{"font_family": "oswald"},
				"start_ms": 0, "end_ms": 4000,
			},
		},
		"animation_groups": []any{map[string]any{
			"id": "scene-1", "start_ms": 0, "end_ms": 4000,
			"choreography_id": AnimationGroupChoreographySequenceV1,
			"params":          map[string]any{"stagger_frames": 4},
			"members": []any{
				map[string]any{"item_id": "headline", "role": AnimationGroupRolePhrase},
				map[string]any{"item_id": "portrait", "role": AnimationGroupRoleImage},
				map[string]any{"item_id": "date", "role": AnimationGroupRoleDate},
			},
		}},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileSemantic(raw)
	if err != nil {
		t.Fatalf("compile animation-group canary: %v", err)
	}
	animated := 0
	for _, layer := range compiled.Plan.Layers {
		if animationGroupLayerHasAnimation(layer) {
			animated++
		}
	}
	if animated < 3 {
		t.Fatalf("canary group lowered %d animated layers, want at least 3: %+v", animated, compiled.Plan.Layers)
	}
	planBytes, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatalf("marshal canary Chronon plan: %v", err)
	}
	planPath := filepath.Join(t.TempDir(), "animation-group-plan.json")
	if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(t.TempDir(), "animation-group-canary.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, bin,
		"render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "software", "--encoder-backend", "pipe",
		"--hardware", "none", "--gpu-hot-path-mode", "auto",
		"--encode-preset", "ultrafast", "-o", videoPath)
	command.Dir = assetsRoot
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Chronon animation-group render failed: %v\n%s", err, tailBytes(output))
	}
	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height,duration",
		"-of", "default=noprint_wrappers=1", videoPath)
	probeOut, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe animation-group canary: %v\n%s", err, probeOut)
	}
	for _, want := range []string{"codec_name=h264", "width=1280", "height=720"} {
		if !strings.Contains(string(probeOut), want) {
			t.Fatalf("canary probe missing %q:\n%s", want, probeOut)
		}
	}
}
