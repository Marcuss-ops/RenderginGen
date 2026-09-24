package overlay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// imageMotionDistinctFPS is the fixture frame rate: the same 24/1 the rest of
// the certification suite renders at, so the frame windows line up.
const imageMotionDistinctFPS = 24

// imageMotionLongestEntranceFrames is the longest entrance any selectable image
// motion declares (the Overlay V3 reveal family). The sampled frame must be past
// it, or the hash would measure the transition rather than the motion's steady
// state — and two motions that differ only in their entrance would then look
// distinct for the wrong reason.
const imageMotionLongestEntranceFrames = 60

// imageMotionShortestExitFrames is the shortest exit tail a selectable image
// motion declares. The sampled frame must be before it, so every motion is
// sampled while it is still fully visible instead of mid-exit.
const imageMotionShortestExitFrames = 6

// imageMotionDistinctFrame is the frame every image-motion render is sampled at.
// The item window is the certification window (start frame 10, exclusive end
// 125), so the sample sits at local frame 74: past the longest 60-frame entrance
// (which ends at frame 70) and before the shortest 6-frame exit (which starts at
// frame 119). The invariant is asserted below, so moving the sample or the window
// reports which bound it broke instead of failing opaquely.
const imageMotionDistinctFrame = 84

// TestEveryImageMotionRendersADistinctFrame is the opt-in image-motion
// certification that the Vulkan 2.5D blur regression made necessary: every
// selectable image motion must render frames that are distinct from every other
// motion when driven with the same fixed image. The test runs only when
// CHRONON_BIN names a real chronon3d_cli binary, the same opt-in the other
// real-engine suites use. Without it the test self-skips, so a bare
// `go test ./...` never becomes minutes of GPU work, and RENDERINGGEN_SKIP_GPU_E2E
// (set by `make test-unit` and CI) keeps the fast gate green everywhere.
// It is the only test that proves the two 2.5D blur motions — which previously
// encoded two distinct motion tracks into the same pure-canvas output and both
// passed every structural gate — now really encode different pixels.
func TestEveryImageMotionRendersADistinctFrame(t *testing.T) {
	bin := chrononBinFor(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available for pixel check: %v", err)
	}
	// The invariant the frame choice rests on, stated as the two bounds it sits
	// between: after the longest entrance and before the shortest exit.
	itemStartFrame := int(certificationStartMS) * imageMotionDistinctFPS / 1000
	itemEndFrame := certificationDurationFrames
	afterEntrance := itemStartFrame + imageMotionLongestEntranceFrames
	beforeExit := int(itemEndFrame) - imageMotionShortestExitFrames
	if imageMotionDistinctFrame <= afterEntrance || imageMotionDistinctFrame >= beforeExit {
		t.Fatalf("sample frame %d is outside the steady-state window (%d, %d) of the item's frames [%d, %d): it must be past the longest %d-frame entrance and before the shortest %d-frame exit",
			imageMotionDistinctFrame, afterEntrance, beforeExit, itemStartFrame, itemEndFrame, imageMotionLongestEntranceFrames, imageMotionShortestExitFrames)
	}
	ids := motion.Registry.ImageOverlayMotionIDs()
	if len(ids) != 18 {
		t.Fatalf("registered image motions = %d, want 18", len(ids))
	}
	assetsRoot := certificationAssetsRoot(t)
	outDir := t.TempDir()

	hashes := make(map[string][32]byte, len(ids))
	var rendered []string
	for _, motionID := range ids {
		t.Run(motionID, func(t *testing.T) {
			plan := imageMotionDistinctPlan(t, motionID)
			videoPath := filepath.Join(outDir, safeName(motionID)+".mp4")
			plan.Output.Path = videoPath
			planPath := filepath.Join(outDir, safeName(motionID)+".json")
			planBytes, err := json.Marshal(plan)
			if err != nil {
				t.Fatalf("marshal plan for %s: %v", motionID, err)
			}
			if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
				t.Fatalf("write plan for %s: %v", motionID, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, "render", "--plan", planPath, "--assets-root", assetsRoot, "--backend", "software", "--encoder-backend", "pipe", "--hardware", "none", "--gpu-hot-path-mode", "auto", "--encode-preset", "ultrafast", "-o", videoPath)
			cmd.Dir = assetsRoot
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("chronon render %s: %v\n%s", motionID, err, tailBytes(out))
			}
			if _, err := os.Stat(videoPath); err != nil {
				t.Fatalf("render %s reported success but output is missing: %v", motionID, err)
			}
			probeMP4Structural(t, videoPath)
			decodeFully(t, videoPath)
			hash := frameHash(t, videoPath, imageMotionDistinctFrame)
			if hash == ([32]byte{}) {
				t.Fatalf("empty frame hash for %s", motionID)
			}
			rendered = append(rendered, motionID)
			hashes[motionID] = hash
		})
	}
	if len(rendered) != len(ids) {
		t.Fatalf("rendered %d/%d motions: skipped subtests never reach the dedup check", len(rendered), len(ids))
	}
	seen := make(map[[32]byte]string, len(ids))
	for _, motionID := range ids {
		hash := hashes[motionID]
		if previous, ok := seen[hash]; ok {
			t.Errorf("image motions %q and %q render identical pixels at frame %d (sha256 %x): the motion pair is not visually distinct — this is the Vulkan pure-canvas signature that batch-verify now rejects as content_missing", previous, motionID, imageMotionDistinctFrame, hash)
		} else {
			seen[hash] = motionID
		}
	}
}

// imageMotionDistinctPlan is the minimal image-motion plan the distinctness
// check renders: one corpus-identical image motion card over the same pale
// olive canvas every batch uses, driven through one selectable motion_id.
func imageMotionDistinctPlan(t *testing.T, motionID string) *Plan {
	t.Helper()
	raw := fmt.Sprintf(`{"schema_version":"renderinggen.overlay-plan.v1","plan_id":%q,"video_id":"v","width":1920,"height":1080,"fps_num":24,"fps_den":1,`+
		`"background":{"kind":"color","color":%s},`+
		`"items":[{"id":"image-motion","kind":"entity_image","template_id":"IMAGE_OVERLAY","preset_id":%q,"motion_id":%q,"text":"Image motion canary","start_ms":%d,"end_ms":%d,`+
		`"entity_id":%q,"asset_refs":[{"asset_id":%q,"sha256":%q,"url":"https://example.test/certification.jpg","media_type":"image/jpeg"}]}]}`,
		"image-motion-"+motionID, certificationBackgroundRGBA,
		ImageMotionCorpusPresetID, motionID, certificationStartMS, certificationEndMS,
		"entity:"+certificationAssetID, certificationAssetID, certificationAssetSHA)
	// The item uses the certification suite's own asset identity and window, so
	// this render goes through the same harness (certificationAssetsRoot
	// materializes assets/semantic/<id>.jpg) and the same structural contract
	// (probeMP4Structural) as every other runtime suite. The compile gate below
	// keeps the plan honest: a renamed preset or an unregistered motion fails at
	// the batch level first instead of only here.
	result, err := CompileSemantic([]byte(raw))
	if err != nil {
		_, source, _, _ := runtime.Caller(0)
		t.Fatalf("compile image motion distinct plan %q (caller %s): %v", motionID, source, err)
	}
	return result.Plan
}

func safeName(id string) string {
	return strings.ReplaceAll(id, "/", "_")
}

// frameHash decodes one frame of path and hashes the raw RGB bytes so the
// distinctness gate measures pixels, not a container encoding.
func frameHash(t *testing.T, path string, frame int) [32]byte {
	t.Helper()
	// Decode one 1920x1080 frame to raw RGB24 through the CPU pipe; the
	// bytes are what batch-verify's frameInk counts against.
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path, "-vf", fmt.Sprintf("select=eq(n\\,%d)", frame), "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("decode frame %d: %v: %s", frame, err, strings.TrimSpace(stderr.String()))
	}
	if len(raw) < 1920*1080*3 {
		t.Fatalf("ffmpeg returned %d bytes for frame %d, want %d", len(raw), frame, 1920*1080*3)
	}
	return sha256.Sum256(raw)
}
