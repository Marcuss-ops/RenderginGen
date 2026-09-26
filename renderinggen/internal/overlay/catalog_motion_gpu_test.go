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

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// TestEveryCallableCatalogMotionExecutesOnTheStrictGPU compiles one plan with
// the complete phrase and image inventories, then runs it through the same
// Vulkan/NVENC strict-native lane required by production script renders.
// CHRONON_BIN keeps this real-engine check opt-in for normal unit-test runs.
func TestEveryCallableCatalogMotionExecutesOnTheStrictGPU(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	imageBytes, err := os.ReadFile(filepath.Join(assetsRoot, "assets", "semantic", certificationAssetID+".jpg"))
	if err != nil {
		t.Fatal(err)
	}
	imageDigest := sha256.Sum256(imageBytes)
	imageSHA := hex.EncodeToString(imageDigest[:])

	var items []map[string]any
	phraseIDs := make([]string, 0, 107)
	for _, family := range []string{"typewriter", "classic_apple", "modern_apple"} {
		phraseIDs = append(phraseIDs, motion.Registry.FamilyMotionIDs(family)...)
	}
	if len(phraseIDs) != 107 {
		t.Fatalf("phrase family inventory has %d IDs, want 107", len(phraseIDs))
	}
	for i, id := range phraseIDs {
		items = append(items, map[string]any{
			"id": fmt.Sprintf("phrase-%03d", i), "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
			"preset_id": PhraseDefaultPresetID, "motion_id": id, "motion_params": map[string]any{"enter_frames": 36},
			"text": "MOTION CATALOG GPU CANARY", "start_ms": 0, "end_ms": 2000,
		})
	}
	imageIDs := motion.Registry.ImageOverlayMotionIDs()
	if len(imageIDs) != 18 {
		t.Fatalf("image motion inventory has %d IDs, want 18", len(imageIDs))
	}
	for i, id := range imageIDs {
		items = append(items, map[string]any{
			"id": fmt.Sprintf("image-%02d", i), "kind": "image", "template_id": "IMAGE_OVERLAY",
			"preset_id": ImageMotionCorpusPresetID, "motion_id": id, "start_ms": 0, "end_ms": 2000,
			"asset_refs": []map[string]any{{
				"asset_id": certificationAssetID, "sha256": imageSHA,
				"url": "https://example.test/certification.jpg", "media_type": "image/jpeg",
			}},
		})
	}

	// Keep phrase engine graphs to four layers. Image motions use one real image
	// per graph, matching the production image overlay render request; several
	// simultaneous camera-backed image layers can exceed the native compositor's
	// per-frame 2.5D resource budget even though each is individually renderable.
	for start := 0; start < len(items); {
		batchSize := 4
		if start >= len(phraseIDs) {
			batchSize = 1
		}
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		batchItems := items[start:end]
		ids := make([]string, len(batchItems))
		for i, item := range batchItems {
			ids[i] = item["motion_id"].(string)
		}
		raw, err := json.Marshal(map[string]any{
			"schema_version": "renderinggen.overlay-plan.v1",
			"plan_id":        fmt.Sprintf("catalog-motions-gpu-%03d", start/batchSize),
			"video_id":       "motion-catalog-canary", "width": 1280, "height": 720, "fps_num": 24, "fps_den": 1,
			"background": map[string]any{"kind": "color", "color": []float64{0.08, 0.08, 0.08, 1}},
			"items":      batchItems,
		})
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := CompileSemantic(raw)
		if err != nil {
			t.Fatalf("compile catalog motions %v: %v", ids, err)
		}
		if len(compiled.Plan.Layers) != len(batchItems)+1 {
			t.Fatalf("compiled %d layers for motions %v, want background + %d overlays", len(compiled.Plan.Layers), ids, len(batchItems))
		}
		outDir := t.TempDir()
		videoPath := filepath.Join(outDir, "catalog-motions.mp4")
		compiled.Plan.Output.Path = videoPath
		planBytes, err := json.MarshalIndent(compiled.Plan, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		planPath := filepath.Join(outDir, "plan.json")
		if err := os.WriteFile(planPath, planBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		preparedBytes, err := json.MarshalIndent(compiled.Prepared, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		preparedPath := filepath.Join(outDir, "prepared.json")
		if err := os.WriteFile(preparedPath, preparedBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		cmd := exec.CommandContext(ctx, bin, "render", "--plan", planPath, "--prepared-package", preparedPath,
			"--assets-root", assetsRoot, "--backend", "vulkan", "--hardware", "nvenc", "--encoder-backend", "native",
			"--gpu-hot-path-mode", "require_gpu_native", "--encode-preset", "p1", "--rate-control", "qp", "--qp", "23", "-o", videoPath)
		cmd.Dir = assetsRoot
		output, renderErr := cmd.CombinedOutput()
		cancel()
		if renderErr != nil {
			t.Fatalf("strict GPU render rejected catalog motions %v: %v\n%s", ids, renderErr, tailBytes(output))
		}
		if _, err := os.Stat(videoPath); err != nil {
			t.Fatalf("strict GPU render of motions %v succeeded but output is missing: %v", ids, err)
		}
		start = end
	}
}
