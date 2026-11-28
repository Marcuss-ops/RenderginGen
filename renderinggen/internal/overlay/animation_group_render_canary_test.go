package overlay

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestAnimationGroupHeterogeneousRenderCanary is the opt-in end-to-end proof
// that one group can own a phrase, an image and a single date card through the
// real Vulkan Chronon CLI. It runs only with CHRONON_BIN set, so a bare
// `go test ./...` never renders video. Raw output lets this canary check actual
// decoded GPU frames without adding an encoder or codec as another variable.
func TestAnimationGroupHeterogeneousRenderCanary(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := groupCanaryAssetsRoot(t)
	imagePath := filepath.Join(assetsRoot, "assets", "semantic", "member-a.png")
	imageSHA := fixtureSHA256(t, imagePath)

	plan := map[string]any{
		"schema_version": "renderinggen.overlay-plan.v1",
		"plan_id":        "animation-group-canary", "video_id": "animation-group-canary",
		"width": 640, "height": 360, "fps_num": 24, "fps_den": 1, "duration_ms": 2500,
		"background": map[string]any{"kind": "color", "color": []any{0.04, 0.05, 0.07, 1}},
		"items": []any{
			map[string]any{
				"id": "headline", "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
				"preset_id": "phrase_default", "text": "Duisburg 1948",
				"params":   map[string]any{"font_family": "oswald"},
				"start_ms": 0, "end_ms": 2500,
			},
			map[string]any{
				"id": "portrait", "kind": "entity_image", "template_id": "image_popup",
				"preset_id": "image_focus_in", "start_ms": 0, "end_ms": 2500,
				"asset_refs": []any{map[string]any{
					"asset_id": "member-a", "sha256": imageSHA,
					"url": "https://fixtures.example/member-a.png", "media_type": "image/png",
				}},
			},
			map[string]any{
				"id": "date", "kind": "number", "template_id": "TIMELINE_DATE_CARD",
				"preset_id": "phrase_default", "text": "1948",
				"date":     map[string]any{"value": "1948"},
				"params":   map[string]any{"font_family": "oswald"},
				"start_ms": 0, "end_ms": 2500,
			},
		},
		"editorial_compositions": []any{map[string]any{
			"schema_version": "editorial_composition.v1",
			"composition_id": "canary-summary", "scene_id": "scene-1",
			"window": map[string]any{"start_us": 0, "duration_us": 2_500_000},
			"members": []any{map[string]any{
				"member_id": "canary-summary-member", "kind": "summary",
				"start_us": 500_000, "duration_us": 1_250_000,
				"reveal": "progressive",
				"points": []any{
					map[string]any{"text": "First grounded summary point", "start_us": 500_000, "duration_us": 500_000},
					map[string]any{"text": "Second grounded summary point", "start_us": 1_250_000, "duration_us": 500_000},
				},
			}},
		}},
		"animation_groups": []any{map[string]any{
			"id": "scene-1", "start_ms": 0, "end_ms": 2500,
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
	summaryFrames := map[string][2]int64{
		"First grounded summary point":  {12, 42},
		"Second grounded summary point": {30, 42},
	}
	seenSummary := make(map[string]bool, len(summaryFrames))
	for _, layer := range compiled.Plan.Layers {
		if animationGroupLayerHasAnimation(layer) {
			animated++
		}
		if layer.Type == "text" {
			for text, want := range summaryFrames {
				if strings.Join(strings.Fields(layer.Text), " ") == text {
					if layer.StartFrame != want[0] || layer.StartFrame+layer.DurationFrames != want[1] {
						t.Fatalf("progressive summary %q timing=[%d,%d), want [%d,%d)", text, layer.StartFrame, layer.StartFrame+layer.DurationFrames, want[0], want[1])
					}
					seenSummary[text] = true
				}
			}
		}
	}
	if len(seenSummary) != len(summaryFrames) {
		t.Fatalf("canary lowered %d progressive summary points, want %d", len(seenSummary), len(summaryFrames))
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
	videoPath := filepath.Join(t.TempDir(), "animation-group-canary.rgba")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, bin,
		"render", "--plan", planPath, "--assets-root", assetsRoot,
		"--backend", "vulkan", "--video-sink", "raw",
		"--hardware", "none", "--gpu-hot-path-mode", "require_gpu_native",
		"-o", videoPath)
	command.Dir = assetsRoot
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Chronon animation-group render failed: %v\n%s", err, tailBytes(output))
	}
	info, err := os.Stat(videoPath)
	if err != nil {
		t.Fatalf("stat raw Vulkan animation-group output: %v", err)
	}
	const frameBytes = int64(640 * 360 * 4)
	if want := frameBytes * 60; info.Size() != want {
		t.Fatalf("raw Vulkan canary bytes = %d, want %d (60 RGBA frames)", info.Size(), want)
	}
	visiblePixels := map[int64]int{
		20: assertCanaryFrameVisible(t, videoPath, 20, frameBytes),
		59: assertCanaryFrameVisible(t, videoPath, 59, frameBytes),
	}

	sidecarPath := videoPath + ".timing.json"
	sidecarBytes, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read Vulkan canary timing sidecar: %v", err)
	}
	var sidecar struct {
		FramesTotal int `json:"frames_total"`
		Job         struct {
			ExecutionDowngradeCount int `json:"execution_downgrade_count"`
			GPU                     struct {
				EffectiveBackend string `json:"effective_backend"`
				Submissions      int    `json:"gpu_submissions"`
			} `json:"gpu"`
		} `json:"job"`
	}
	if err := json.Unmarshal(sidecarBytes, &sidecar); err != nil {
		t.Fatalf("decode Vulkan canary timing sidecar: %v", err)
	}
	if sidecar.FramesTotal != 60 || sidecar.Job.GPU.EffectiveBackend != "vulkan" ||
		sidecar.Job.GPU.Submissions <= 0 || sidecar.Job.ExecutionDowngradeCount != 0 {
		t.Fatalf("Vulkan canary telemetry does not certify 60 GPU frames: %+v", sidecar)
	}
	writeCanaryEvidence(t, bin, planPath, videoPath, sidecarPath, info.Size(), visiblePixels,
		sidecar.FramesTotal, sidecar.Job.GPU.EffectiveBackend, sidecar.Job.GPU.Submissions)
}

func assertCanaryFrameVisible(t *testing.T, path string, frame, frameBytes int64) int {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Seek(frame*frameBytes, io.SeekStart); err != nil {
		t.Fatalf("seek canary frame %d: %v", frame, err)
	}
	pixels := make([]byte, frameBytes)
	if _, err := io.ReadFull(file, pixels); err != nil {
		t.Fatalf("read canary frame %d: %v", frame, err)
	}
	changed := 0
	for i := 0; i < len(pixels); i += 4 {
		if absByteDelta(pixels[i], 10) > 18 || absByteDelta(pixels[i+1], 13) > 18 || absByteDelta(pixels[i+2], 18) > 18 {
			changed++
		}
	}
	if changed < 32 {
		t.Fatalf("canary frame %d contains only %d foreground pixels; expected the grouped image/text/date to remain visible", frame, changed)
	}
	return changed
}

func absByteDelta(left, right byte) int {
	if left > right {
		return int(left - right)
	}
	return int(right - left)
}

func writeCanaryEvidence(t *testing.T, bin, planPath, rawPath, sidecarPath string, rawBytes int64,
	visiblePixels map[int64]int, frameCount int, backend string, submissions int) {
	t.Helper()
	destination := os.Getenv("CHRONON_CANARY_EVIDENCE_DIR")
	if destination == "" {
		return
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatalf("create canary evidence directory: %v", err)
	}
	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	sidecarBytes, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "animation-group-plan.json"), planBytes, 0o644); err != nil {
		t.Fatalf("write canary plan evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "animation-group.timing.json"), sidecarBytes, 0o644); err != nil {
		t.Fatalf("write canary telemetry evidence: %v", err)
	}
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve canary source path")
	}
	commit, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	cliSHA := os.Getenv("CHRONON_BUILD_SHA256")
	if cliSHA == "" {
		cliSHA = hashCanaryFile(t, bin)
	}
	report := map[string]any{
		"schema":                               "editorial_composition_render_canary.v1",
		"certification":                        "pass",
		"source_commit":                        strings.TrimSpace(string(commit)),
		"renderinggen_source_sha256":           hashCanaryFile(t, sourcePath),
		"chronon_cli_sha256":                   cliSHA,
		"plan_sha256":                          fmt.Sprintf("%x", sha256.Sum256(planBytes)),
		"raw_rgba_sha256":                      hashCanaryFile(t, rawPath),
		"backend":                              backend,
		"gpu_submissions":                      submissions,
		"execution_downgrade_count":            0,
		"canvas":                               map[string]int{"width": 640, "height": 360, "fps": 24},
		"expected_frames":                      60,
		"actual_frames":                        frameCount,
		"allowed_frame_tolerance":              0,
		"raw_rgba_bytes":                       rawBytes,
		"background_rgba":                      []int{10, 13, 18, 255},
		"visibility_samples_foreground_pixels": visiblePixels,
		"visibility_threshold_pixels":          32,
		"visibility_sample_frames":             []int{20, 59},
	}
	reportBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "report.json"), append(reportBytes, '\n'), 0o644); err != nil {
		t.Fatalf("write canary evidence report: %v", err)
	}
}

func hashCanaryFile(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open canary evidence input %s: %v", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.CopyBuffer(hash, file, make([]byte, 1024*1024)); err != nil {
		t.Fatalf("hash canary evidence input %s: %v", path, err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
