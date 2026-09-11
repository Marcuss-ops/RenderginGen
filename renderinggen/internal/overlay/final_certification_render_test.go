package overlay

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cornerInsideEntity reports whether the 2x2 crop sampled at corner c is fully
// inside the entity's coverage region. A flush bottom/right card legitimately
// owns its corner of the canvas, so those corners must be excluded from the
// background-preservation check (the black regression is caught by the
// corners the entity does NOT cover).
func cornerInsideEntity(c [2]int, region [4]float64) bool {
	if region[2] <= region[0] || region[3] <= region[1] {
		return false
	}
	return float64(c[0]) >= region[0] && float64(c[0])+1 <= region[2] &&
		float64(c[1]) >= region[1] && float64(c[1])+1 <= region[3]
}

// assertBackgroundPreserved checks the corners of every sampled frame that
// fall outside the entity's declared coverage: the Pale Olive background must
// survive untouched there. A dark corner is the black-background regression
// signature.
func assertBackgroundPreserved(t *testing.T, plan *Plan, path string, frames []int) {
	t.Helper()
	corners := [][2]int{{50, 50}, {1869, 50}, {50, 1029}, {1869, 1029}}
	coverage := entityCoverage(plan)
	for _, frame := range frames {
		checked := 0
		for _, c := range corners {
			if cornerInsideEntity(c, coverage) {
				continue
			}
			checked++
			luma := sampleLuma(t, path, frame, c[0], c[1])
			if luma < 180 {
				t.Errorf("frame %d corner (%d,%d) luma=%.1f: background replaced (black-frame regression?)",
					frame, c[0], c[1], luma)
			}
		}
		if checked == 0 {
			t.Errorf("frame %d: every corner is inside the entity coverage; background preservation is unverifiable", frame)
		}
	}
}

// TestFinal_AllOfficialPresetsRender is the runtime registry gate: every
// official preset must produce a structurally valid, fully decodable MP4
// with the background preserved. Skipped presets do not exist: registry
// coverage and rendering are asserted together, in one place.
func TestFinal_AllOfficialPresetsRender(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()

	for _, id := range OfficialPresetIDs() {
		t.Run(id, func(t *testing.T) {
			plan := certificationPlan(t, id)
			videoPath := renderCertificationPlan(t, bin, assetsRoot, outDir, id, id)

			// A. structural
			probeMP4Structural(t, videoPath)
			// B. decode
			decodeFully(t, videoPath)
			// C. pixel: first, middle and last frames must keep the
			// background and never come back black.
			assertBackgroundPreserved(t, plan, videoPath, []int{0, int(certificationDurationFrames) / 2, int(certificationDurationFrames) - 1})
		})
	}
}

// TestFinal_EntityCenterDiffersFromBackground proves the entity really drew:
// the pixel at the entity's center must differ from the untouched Pale Olive
// background in a mid-animation frame. This is the assertion that catches a
// silently-missing layer even when every structural check is green. The
// sample point is derived from the compiled plan (image frames are centred at
// canvas_centre + position), never hard-coded.
func TestFinal_EntityCenterDiffersFromBackground(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()

	plan := certificationPlan(t, "image_scale_in")
	videoPath := renderCertificationPlan(t, bin, assetsRoot, outDir, "image_scale_in", "image_scale_in")
	coverage := entityCoverage(plan)
	if coverage[2] <= coverage[0] || coverage[3] <= coverage[1] {
		t.Fatal("entity coverage missing: cannot derive the sample point")
	}
	cx := int((coverage[0] + coverage[2]) / 2)
	cy := int((coverage[1] + coverage[3]) / 2)
	mid := int(certificationDurationFrames) / 2
	centerLuma := sampleLuma(t, videoPath, mid, cx, cy)
	cornerLuma := sampleLuma(t, videoPath, mid, 50, 50)
	if diff := centerLuma - cornerLuma; diff > -20 && diff < 20 {
		t.Errorf("entity center (%d,%d) luma=%.1f vs background %.1f: entity layer is not visibly drawn", cx, cy, centerLuma, cornerLuma)
	}
}

// TestFinal_ExclusiveEndNoFrame125 checks the frame-count contract that bit
// the pipeline before: 125 declared frames must produce exactly 125 frames —
// neither 124 (lost tail) nor 126 (phantom frame from an inclusive end).
func TestFinal_ExclusiveEndNoFrame125(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()

	videoPath := renderCertificationPlan(t, bin, assetsRoot, outDir, "static_text_smoke", "static_text_smoke")
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-count_frames", "-show_entries", "stream=nb_read_frames", "-of",
		"csv=p=0", videoPath).Output()
	if err != nil {
		t.Fatalf("ffprobe count_frames: %v", err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatalf("parse frame count %q: %v", string(out), err)
	}
	if n != certificationDurationFrames {
		t.Errorf("decoded %d frames, want exactly %d (exclusive-end contract: last index %d)",
			n, certificationDurationFrames, certificationDurationFrames-1)
	}
}

// TestFinal_MissingAssetFailsClosed renders a plan whose image asset does
// not exist: Chronon must exit non-zero — a "successful" render of a missing
// entity is the fail-open bug this suite exists to prevent.
func TestFinal_MissingAssetFailsClosed(t *testing.T) {
	bin := chrononBinFor(t)
	outDir := t.TempDir()

	raw := fmt.Sprintf(
		`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":"missing-asset","video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
			`"items":[{"id":"img","template_id":"IMAGE_OVERLAY","preset_id":"image_scale_in","start_ms":0,"end_ms":5208,`+
			`"asset_refs":[{"asset_id":"missing-asset","sha256":%q,"url":"https://store.example/does_not_exist.jpg","media_type":"image/jpeg"}]}]}`,
		certificationAssetSHA)
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	plan := result.Plan
	videoPath := filepath.Join(outDir, "missing.mp4")
	plan.Output.Path = videoPath
	planPath := filepath.Join(outDir, "plan.json")
	planBytes, _ := json.MarshalIndent(plan, "", "  ")
	if err := os.WriteFile(planPath, planBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "render", "--plan", planPath, "--assets-root", outDir,
		"--backend", "software", "--encoder-backend", "pipe", "--hardware", "none",
		"--gpu-hot-path-mode", "auto", "-o", videoPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("render of missing asset succeeded (exit 0): fail-open bug\n%s", tailBytes(out))
	}
	if _, statErr := os.Stat(videoPath); statErr == nil {
		t.Errorf("render of missing asset produced an output file: fail-open bug\n%s", tailBytes(out))
	}
}

// TestFinal_DeterministicPlanRendered proves same input → same plan JSON for
// a registry preset (byte-identical), the deterministic-plan certification gate.
func TestFinal_DeterministicPlanRendered(t *testing.T) {
	build := func() []byte {
		plan := certificationPlan(t, "image_scale_in")
		plan.Output.Path = "out.mp4"
		data, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first, second := build(), build()
	if string(first) != string(second) {
		t.Error("same input produced different plan JSON: deterministic-plan gate failed")
	}
}

// TestFinal_RepeatedRenderSameProcess renders the same preset several times
// in one test process and asserts every output is structurally valid — the
// repeated-render stability gate (deeper memory assertions live in the
// Chronon daemon stability suite).
func TestFinal_RepeatedRenderSameProcess(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()

	for i := 0; i < 3; i++ {
		videoPath := renderCertificationPlan(t, bin, assetsRoot, outDir,
			"static_text_smoke", fmt.Sprintf("static_text_smoke_repeat%d", i))
		probeMP4Structural(t, videoPath)
		decodeFully(t, videoPath)
	}
}

func tailBytes(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 800 {
		s = s[len(s)-800:]
	}
	return s
}
