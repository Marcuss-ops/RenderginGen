package overlay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/motion"
)

// TestEveryPresentationMotionExecutesOnTheStrictGPU compiles every presentation
// motion through the semantic contract and renders it through the strict
// Vulkan/NVENC-native lane. The native encoder is required: a raw host-frame
// sink cannot prove GPU-native surface handoff.
func TestEveryPresentationMotionExecutesOnTheStrictGPU(t *testing.T) {
	bin := chrononBinFor(t)
	assetsRoot := certificationAssetsRoot(t)
	type certifiedMotion struct {
		FamilyID string `json:"family_id"`
		MotionID string `json:"motion_id"`
		File     string `json:"file"`
		SHA256   string `json:"sha256"`
		Bytes    int64  `json:"bytes"`
		Codec    string `json:"codec"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		Frames   int    `json:"frames"`
	}
	outputRoot := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_CERT_OUTPUT_DIR"))
	if outputRoot == "" {
		outputRoot = t.TempDir()
	} else if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		t.Fatalf("create persistent GPU certification output directory: %v", err)
	}
	outputRoot, err := filepath.Abs(outputRoot)
	if err != nil {
		t.Fatalf("resolve GPU certification output directory: %v", err)
	}
	certified := make([]certifiedMotion, 0, 50)

	for _, familyID := range motion.Registry.PresentationFamilyIDs() {
		for _, id := range motion.Registry.PresentationMotionIDs(familyID) {
			if !t.Run(id, func(t *testing.T) {
				plugin, err := motion.Registry.Resolve(id)
				if err != nil {
					t.Fatal(err)
				}
				definition := plugin.(motion.DeclarativePlugin).Definition
				item := map[string]any{
					"id": "presentation-" + id, "template_id": definition.SupportedTemplate,
					"preset_id": PhraseDefaultPresetID, "motion_id": id,
					"text": "2026 59.99", "start_ms": 0, "end_ms": 2000,
				}
				switch familyID {
				case "metric_v1":
					item["kind"] = string(KindMetricStat)
				case "date_v1":
					item["kind"] = string(KindTimelineDate)
				case "entity_card_v1":
					item["kind"] = string(KindEntityCard)
					item["entity_id"] = "entity:gpu-certification"
					item["duration_ms"] = 2000
				}
				raw, err := json.Marshal(map[string]any{
					"schema_version": SemanticSchema, "plan_id": "presentation-gpu-" + id,
					"video_id": "presentation-gpu-certification", "width": 1280, "height": 720,
					"fps_num": 24, "fps_den": 1,
					"background": map[string]any{"kind": "color", "color": []float64{0.08, 0.08, 0.08, 1}},
					"items":      []any{item},
				})
				if err != nil {
					t.Fatal(err)
				}
				compiled, err := CompileSemantic(raw)
				if err != nil {
					t.Fatalf("compile motion_id %s: %v", id, err)
				}
				outDir := t.TempDir()
				videoPath := filepath.Join(outputRoot, id+".mp4")
				if _, err := os.Stat(videoPath); err == nil {
					t.Fatalf("refusing to overwrite existing certified output %s", videoPath)
				} else if !os.IsNotExist(err) {
					t.Fatalf("check certification output path %s: %v", videoPath, err)
				}
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
					t.Fatalf("strict Vulkan/GPU-native render rejected %s: %v\n%s", id, renderErr, string(output))
				}
				info, err := os.Stat(videoPath)
				if err != nil {
					t.Fatalf("strict GPU render of %s succeeded without an output: %v", id, err)
				}
				if info.Size() == 0 {
					t.Fatalf("strict GPU render of %s produced an empty MP4", id)
				}
				probeOutput, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
					"-count_frames", "-show_entries", "stream=codec_name,width,height,nb_read_frames", "-of", "json", videoPath).Output()
				if err != nil {
					t.Fatalf("ffprobe strict GPU output for %s: %v", id, err)
				}
				var probe struct {
					Streams []struct {
						CodecName     string `json:"codec_name"`
						Width         int    `json:"width"`
						Height        int    `json:"height"`
						FramesDecoded string `json:"nb_read_frames"`
					} `json:"streams"`
				}
				if err := json.Unmarshal(probeOutput, &probe); err != nil {
					t.Fatalf("parse ffprobe output for %s: %v", id, err)
				}
				if len(probe.Streams) != 1 {
					t.Fatalf("strict GPU output for %s has %d video streams, want 1", id, len(probe.Streams))
				}
				stream := probe.Streams[0]
				frames, err := strconv.Atoi(strings.TrimSpace(stream.FramesDecoded))
				if err != nil {
					t.Fatalf("parse decoded frame count %q for %s: %v", stream.FramesDecoded, id, err)
				}
				if stream.CodecName != "h264" || stream.Width != 1280 || stream.Height != 720 || frames != 48 {
					t.Fatalf("strict GPU output for %s: codec=%s size=%dx%d frames=%d; want h264 1280x720 48 frames",
						id, stream.CodecName, stream.Width, stream.Height, frames)
				}
				file, err := os.Open(videoPath)
				if err != nil {
					t.Fatalf("open certified output %s: %v", videoPath, err)
				}
				hasher := sha256.New()
				if _, err := io.Copy(hasher, file); err != nil {
					_ = file.Close()
					t.Fatalf("hash certified output %s: %v", videoPath, err)
				}
				if err := file.Close(); err != nil {
					t.Fatalf("close certified output %s: %v", videoPath, err)
				}
				certified = append(certified, certifiedMotion{
					FamilyID: familyID, MotionID: id, File: filepath.Base(videoPath),
					SHA256: hex.EncodeToString(hasher.Sum(nil)), Bytes: info.Size(),
					Codec: stream.CodecName, Width: stream.Width, Height: stream.Height, Frames: frames,
				})
			}) {
				t.Fatalf("presentation GPU certification stopped at %s; remaining presets were not run", id)
			}
		}
	}
	if len(certified) != 50 {
		t.Fatalf("GPU certification produced %d verified outputs, want 50", len(certified))
	}
	manifest := struct {
		SchemaVersion string            `json:"schema_version"`
		Backend       string            `json:"backend"`
		GPUHotPath    string            `json:"gpu_hot_path_mode"`
		Encoder       string            `json:"encoder"`
		OutputDir     string            `json:"output_dir"`
		Motions       []certifiedMotion `json:"motions"`
	}{
		SchemaVersion: "renderinggen.presentation-gpu-certification.v1",
		Backend:       "vulkan", GPUHotPath: "require_gpu_native", Encoder: "nvenc-native",
		OutputDir: outputRoot, Motions: certified,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("serialize GPU certification manifest: %v", err)
	}
	manifestBytes = append(manifestBytes, 10)
	manifestPath := filepath.Join(outputRoot, "presentation_gpu_certification_manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("write GPU certification manifest %s: %v", manifestPath, err)
	}
}

// TestEveryCallableCatalogMotionExecutesOnTheStrictGPU compiles the phrase and
// image inventories, then renders them through the strict Vulkan/NVENC lane.
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
