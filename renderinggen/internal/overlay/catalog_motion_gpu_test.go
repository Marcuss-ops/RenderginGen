package overlay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
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
	certified := make([]certifiedMotion, 0, 70)

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
				case "metric_v1", "metric_didone_v1":
					item["kind"] = string(KindMetricStat)
				case "date_v1", "date_didone_v1":
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
				if err := verifyCatalogMotionOutput(videoPath, 1280, 720, true); err != nil {
					t.Fatalf("verify presentation GPU output %s: %v", id, err)
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
	if len(certified) != 70 {
		t.Fatalf("GPU certification produced %d verified outputs, want 70", len(certified))
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
	outputRoot := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_CERT_OUTPUT_DIR"))
	preserveInputs := outputRoot != ""
	if preserveInputs {
		if err := os.MkdirAll(outputRoot, 0o755); err != nil {
			t.Fatalf("create persistent GPU test output directory: %v", err)
		}
	}
	imageBytes := motionCanaryJPEG(t, color.RGBA{R: 32, G: 160, B: 220, A: 255})
	if err := os.MkdirAll(filepath.Join(assetsRoot, "assets", "semantic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsRoot, "assets", "semantic", certificationAssetID+".jpg"), imageBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	imageDigest := sha256.Sum256(imageBytes)
	imageSHA := hex.EncodeToString(imageDigest[:])
	stackAssetPath := filepath.Join(assetsRoot, "assets", "semantic", "certification_image_back.jpg")
	stackImageBytes := motionCanaryJPEG(t, color.RGBA{R: 210, G: 95, B: 45, A: 255})
	stackImageDigest := sha256.Sum256(stackImageBytes)
	stackImageSHA := hex.EncodeToString(stackImageDigest[:])

	var items []map[string]any
	entityCaptionIDs := motion.Registry.SelectableCategoryMotionIDs("entity_caption_v1")
	trumpEntityCaptionIDs := motion.Registry.SelectableCategoryMotionIDs("trump_entity_text_v1")
	typewriterCaptionIDs := motion.Registry.SelectableCategoryMotionIDs("typewriter")
	typewriterGlitchCaptionIDs := motion.Registry.SelectableCategoryMotionIDs("typewriter_glitch")
	modernTypewriterCaptionIDs := motion.Registry.SelectableCategoryMotionIDs("typewriter_modern_v1")
	entityPresentationIDs := motion.Registry.PresentationMotionIDs("entity_card_v1")
	entityCaptionStart := 0
	phraseIDs := make([]string, 0, 128)
	captionMotionIDs := append([]string(nil), entityCaptionIDs...)
	captionMotionIDs = append(captionMotionIDs, trumpEntityCaptionIDs...)
	captionMotionIDs = append(captionMotionIDs, typewriterCaptionIDs...)
	captionMotionIDs = append(captionMotionIDs, typewriterGlitchCaptionIDs...)
	captionMotionIDs = append(captionMotionIDs, modernTypewriterCaptionIDs...)
	captionMotionIDs = append(captionMotionIDs, entityPresentationIDs...)
	for i, id := range captionMotionIDs {
		item := map[string]any{
			"id": fmt.Sprintf("entity-caption-%03d", i), "kind": string(KindEntityCard), "template_id": "PERSON",
			"preset_id": PhraseDefaultPresetID, "image_preset_id": "image_scale_in",
			"caption_motion_id": id,
			"entity_caption":    "GPU ENTITY CAPTION CANARY", "text": "GPU ENTITY CAPTION CANARY",
			"entity_id": "entity:gpu-caption-canary", "start_ms": 0, "end_ms": 2000, "duration_ms": 2000,
			"asset_refs": []map[string]any{{"asset_id": certificationAssetID, "sha256": imageSHA,
				"url": "assets/semantic/" + certificationAssetID + ".jpg", "media_type": "image/jpeg"}},
		}
		items = append(items, item)
	}
	entityCaptionCount := len(items)
	phraseStart := entityCaptionCount
	for _, category := range []string{"typewriter", "typewriter_modern_v1", "apple_v2", "apple_v3", "phrase_apple_clean_v1", "apple_phrase_v1", "phrase_highlight_v1", "text_3d_v1", "short_phrase_style"} {
		phraseIDs = append(phraseIDs, motion.Registry.CategoryMotionIDs(category)...)
	}
	if len(phraseIDs) != 203 {
		t.Fatalf("phrase family inventory has %d IDs, want 203", len(phraseIDs))
	}
	for i, id := range phraseIDs {
		items = append(items, map[string]any{
			"id": fmt.Sprintf("phrase-%03d", i), "kind": "important_phrase", "template_id": "IMPORTANT_PHRASE",
			"motion_id": id, "preset_id": PhraseDefaultPresetID, "motion_params": map[string]any{"enter_frames": 36},
			"text": "MOTION CATALOG GPU CANARY", "start_ms": 0, "end_ms": 2000,
		})
	}
	imageCategories := []string{"image_premium_v1", "editorial_image_v1", "image_25d_clean_v1", "overlay_v3_image", "web", "web_rect_v1", "paint_v1", "light_leak_v1", "brush_v1"}
	imageIDs := make([]string, 0, 126)
	for _, cat := range imageCategories {
		imageIDs = append(imageIDs, motion.Registry.CategoryMotionIDs(cat)...)
	}
	if len(imageIDs) != 126 {
		t.Fatalf("image motion inventory has %d IDs, want 126", len(imageIDs))
	}
	for i, id := range imageIDs {
		item := map[string]any{
			"id": fmt.Sprintf("image-%02d", i), "kind": "image", "template_id": "IMAGE_OVERLAY",
			"preset_id": ImageMotionCorpusPresetID, "motion_id": id, "start_ms": 0, "end_ms": 2000,
			"entity_caption": "GPU motion canary",
			"asset_refs": []map[string]any{{
				"asset_id": certificationAssetID, "sha256": imageSHA,
				"url": "assets/semantic/" + certificationAssetID + ".jpg", "media_type": "image/jpeg",
			}},
		}
		if id == "image_stack_focus" {
			if err := os.WriteFile(stackAssetPath, stackImageBytes, 0o644); err != nil {
				t.Fatalf("materialize second stack image asset: %v", err)
			}
			item["motion_params"] = map[string]any{"active_layer_id": "front"}
			item["image_layers"] = []map[string]any{
				{"id": "back", "asset_id": certificationAssetID, "start_ms": 0, "end_ms": 2000, "preset_id": ImageMotionCorpusPresetID},
				{"id": "front", "asset_id": certificationAssetID, "start_ms": 0, "end_ms": 2000, "preset_id": ImageMotionCorpusPresetID},
			}
			item["asset_refs"] = append(item["asset_refs"].([]map[string]any), map[string]any{
				"asset_id": "certification_image_back", "sha256": stackImageSHA, "url": "assets/semantic/certification_image_back.jpg", "media_type": "image/jpeg",
			})
			item["image_layers"].([]map[string]any)[0]["asset_id"] = "certification_image_back"
			// A composited item owns its caption on the image layer, never on the
			// parent: the compiler rejects a parent entity_caption on image_layers,
			// so the harness must build the plan the contract actually accepts.
			delete(item, "entity_caption")
			item["image_layers"].([]map[string]any)[1]["caption"] = "GPU motion canary"
		}
		items = append(items, item)
	}
	familyFilter := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_CERT_FAMILY"))
	motionFilter := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_CERT_MOTION"))
	gpuHotPathMode := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_CERT_GPU_MODE"))
	if gpuHotPathMode == "" {
		gpuHotPathMode = "require_gpu_native"
	}
	backend := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_CERT_BACKEND"))
	if backend == "" {
		backend = "vulkan"
	}
	hardware := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_CERT_HARDWARE"))
	if hardware == "" {
		hardware = "nvenc"
	}
	encoderBackend := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_CERT_ENCODER_BACKEND"))
	if encoderBackend == "" {
		encoderBackend = "native"
	}
	encodePreset := "p1"
	rateControl := "qp"
	rateValueFlag := "--qp"
	if hardware == "none" {
		encodePreset = "medium"
		rateControl = "crf"
		rateValueFlag = "--crf"
	}
	type motionResult struct {
		MotionID  string  `json:"motion_id"`
		Status    string  `json:"status"`
		DurationS float64 `json:"duration_seconds,omitempty"`
		Error     string  `json:"error,omitempty"`
	}
	var results []motionResult
	writeReport := func() {
		if !preserveInputs {
			return
		}
		report := struct {
			GeneratedAt    string         `json:"generated_at_utc"`
			Backend        string         `json:"backend"`
			Hardware       string         `json:"hardware_encoder"`
			EncoderBackend string         `json:"encoder_backend"`
			GPUHotPathMode string         `json:"gpu_hot_path_mode"`
			Motions        []motionResult `json:"motions"`
		}{time.Now().UTC().Format(time.RFC3339), backend, hardware, encoderBackend, gpuHotPathMode, results}
		data, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr == nil {
			_ = os.WriteFile(filepath.Join(outputRoot, "motion-runtime-report.json"), data, 0o644)
		}
	}
	switch familyFilter {
	case "":
	case "phrase":
		items = items[phraseStart : phraseStart+len(phraseIDs)]
	case "image":
		items = items[entityCaptionCount+len(phraseIDs):]
	case "editorial_image":
		start := entityCaptionCount + len(phraseIDs) + 21
		items = items[start : start+14]
	case "image_25d":
		start := entityCaptionCount + len(phraseIDs) + 21 + 14
		items = items[start : start+8]
	case "overlay_v3_image":
		start := entityCaptionCount + len(phraseIDs) + 21 + 14 + 8
		items = items[start : start+10]
	case "web":
		start := entityCaptionCount + len(phraseIDs) + 21 + 14 + 8 + 10
		items = items[start : start+14]
	case "web_rect":
		start := entityCaptionCount + len(phraseIDs) + 21 + 14 + 8 + 10 + 14
		items = items[start : start+12]
	case "paint":
		start := entityCaptionCount + len(phraseIDs) + 21 + 14 + 8 + 10 + 14 + 12
		items = items[start : start+12]
	case "light_leak":
		start := entityCaptionCount + len(phraseIDs) + 21 + 14 + 8 + 10 + 14 + 12 + 12
		items = items[start : start+12]
	case "brush":
		start := entityCaptionCount + len(phraseIDs) + 21 + 14 + 8 + 10 + 14 + 12 + 12 + 12
		items = items[start : start+23]
	case "text_3d":
		start := phraseStart + 144
		items = items[start : start+10]
	case "short_phrase_style":
		start := phraseStart + 154
		items = items[start : start+48]
	case "entity_caption":
		items = items[entityCaptionStart:entityCaptionCount]
	default:
		t.Fatalf("RENDERINGGEN_GPU_CERT_FAMILY=%q, want phrase, image, editorial_image, image_25d, overlay_v3_image, web, web_rect, paint, light_leak, brush, text_3d, short_phrase_style or entity_caption", familyFilter)
	}
	if strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_CERT_MULTI_ACCENT_ONLY")) == "1" {
		if familyFilter != "phrase" {
			t.Fatalf("RENDERINGGEN_GPU_CERT_MULTI_ACCENT_ONLY requires family=phrase, got %q", familyFilter)
		}
		filtered := items[:0]
		for _, item := range items {
			id := item["motion_id"].(string)
			plugin, err := motion.Registry.Resolve(id)
			if err != nil {
				t.Fatalf("resolve motion %q for multi-accent certification: %v", id, err)
			}
			if len(plugin.(motion.DeclarativePlugin).Definition.LayerComponents) > 1 {
				filtered = append(filtered, item)
			}
		}
		items = filtered
		if len(items) != 5 {
			t.Fatalf("multi-accent certification selected %d motions, want 5", len(items))
		}
	}
	if motionFilter != "" {
		// Comma-separated batch form (session-proof reruns): each listed ID
		// must match exactly one motion; order is preserved.
		wanted := strings.Split(motionFilter, ",")
		byID := make(map[string]map[string]any, len(items))
		for _, item := range items {
			if id, ok := item["motion_id"].(string); ok && id != "" {
				byID[id] = item
			}
			if id, ok := item["caption_motion_id"].(string); ok && id != "" {
				byID[id] = item
			}
		}
		filtered := make([]map[string]any, 0, len(wanted))
		for _, id := range wanted {
			id = strings.TrimSpace(id)
			item, ok := byID[id]
			if !ok {
				t.Fatalf("RENDERINGGEN_GPU_CERT_MOTION=%q matched no motion for %q", motionFilter, id)
			}
			filtered = append(filtered, item)
		}
		items = filtered
		if items[0]["kind"] == "image" {
			phraseIDs = nil
		}
	}

	// Render each motion in isolation so one unsupported track cannot take down
	// unrelated motions in the same Vulkan device submission. Image motions use
	// one real image per graph, matching the production image overlay request.
	for start := 0; start < len(items); {
		batchSize := 1
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		batchItems := items[start:end]
		ids := make([]string, len(batchItems))
		for i, item := range batchItems {
			if id, ok := item["motion_id"].(string); ok && id != "" {
				ids[i] = id
			} else {
				ids[i] = item["caption_motion_id"].(string)
			}
		}
		canvasWidth, canvasHeight := 1280, 720
		if backend == "software" {
			canvasWidth, canvasHeight = 640, 360
		}
		planDocument := map[string]any{
			"schema_version": "renderinggen.overlay-plan.v1",
			"plan_id":        fmt.Sprintf("catalog-motions-gpu-%03d", start/batchSize),
			"video_id":       "motion-catalog-canary", "width": canvasWidth, "height": canvasHeight, "fps_num": 24, "fps_den": 1,
			"items": batchItems,
		}
		planDocument["background"] = map[string]any{"kind": "color", "color": []float64{0.08, 0.08, 0.08, 1}}
		raw, err := json.Marshal(planDocument)
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := CompileSemantic(raw)
		if err != nil {
			results = append(results, motionResult{MotionID: ids[0], Status: "compile_failed", Error: err.Error()})
			if preserveInputs {
				outDir := filepath.Join(outputRoot, "inputs", ids[0])
				_ = os.MkdirAll(outDir, 0o755)
				_ = os.WriteFile(filepath.Join(outDir, "compile-error.txt"), []byte(err.Error()+"\n"), 0o644)
			}
			writeReport()
			t.Errorf("compile catalog motion %v: %v", ids, err)
			start = end
			continue
		}
		if batchItems[0]["kind"] == "important_phrase" && len(compiled.Plan.Layers) < 2 {
			t.Fatalf("compiled %d layers for phrase motion %v, want background and phrase layer", len(compiled.Plan.Layers), ids)
		}
		if batchItems[0]["kind"] == "image" && len(compiled.Plan.Layers) < 2 {
			t.Fatalf("compiled %d layers for image motion %v, want background and rendered image layers", len(compiled.Plan.Layers), ids)
		}
		if batchItems[0]["kind"] == string(KindEntityCard) {
			captionLayerIndex := len(compiled.Plan.Layers) - 1
			captionLayer := compiled.Plan.Layers[captionLayerIndex]
			if captionLayerIndex < 1 || captionLayer.Type != "text" || !layerHasAnimation(captionLayer) {
				t.Fatalf("entity-caption motion %v did not compile image + animated caption layers: %+v", ids, compiled.Plan.Layers)
			}
		}
		outDir := t.TempDir()
		if preserveInputs {
			outDir = filepath.Join(outputRoot, "inputs", ids[0])
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				t.Fatalf("create persistent GPU input directory: %v", err)
			}
		}
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
		cmdTimeout := 3 * time.Minute
		if encoderBackend == "native" && isMultiAccentMotion(ids[0]) {
			cmdTimeout = 12 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		renderStarted := time.Now()
		cmd := exec.CommandContext(ctx, bin, "render", "--plan", planPath, "--prepared-package", preparedPath,
			"--assets-root", assetsRoot, "--backend", backend, "--hardware", hardware, "--encoder-backend", encoderBackend,
			"--gpu-hot-path-mode", gpuHotPathMode, "--encode-preset", encodePreset, "--rate-control", rateControl, rateValueFlag, "23", "-o", videoPath)
		cmd.Dir = assetsRoot
		output, renderErr := cmd.CombinedOutput()
		cancel()
		duration := time.Since(renderStarted).Seconds()
		if preserveInputs {
			_ = os.WriteFile(filepath.Join(outDir, "render.log"), output, 0o644)
		}
		if renderErr != nil {
			errStr := renderErr.Error()
			if encoderBackend == "native" && isMultiAccentMotion(ids[0]) && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				errStr = fmt.Sprintf("strict native render exceeded %s: %v", cmdTimeout, ctx.Err())
			}
			results = append(results, motionResult{MotionID: ids[0], Status: "render_failed", DurationS: duration, Error: errStr})
			writeReport()
			t.Errorf("strict GPU render rejected catalog motion %v: %v", ids, renderErr)
			start = end
			continue
		}
		if _, err := os.Stat(videoPath); err != nil {
			results = append(results, motionResult{MotionID: ids[0], Status: "output_missing", Error: err.Error()})
			writeReport()
			t.Errorf("strict GPU render of motion %v succeeded but output is missing: %v", ids, err)
			start = end
			continue
		}
		if err := verifyCatalogMotionOutput(videoPath, canvasWidth, canvasHeight,
			backend == "vulkan" && hardware == "nvenc" && encoderBackend == "native" && gpuHotPathMode == "require_gpu_native"); err != nil {
			results = append(results, motionResult{MotionID: ids[0], Status: "verification_failed", DurationS: duration, Error: err.Error()})
			writeReport()
			t.Errorf("verify rendered catalog motion %v: %v", ids, err)
			start = end
			continue
		}
		results = append(results, motionResult{MotionID: ids[0], Status: "passed", DurationS: duration})
		writeReport()
		start = end
	}
}

// A successful process and an existing path are not certification: decode the
// whole video and prove that the strict run did not silently use CPU pixels.
func verifyCatalogMotionOutput(path string, width, height int, strict bool) error {
	probeBytes, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-count_frames", "-show_entries", "stream=codec_name,width,height,nb_read_frames", "-of", "json", path).Output()
	if err != nil {
		return fmt.Errorf("decode MP4 with ffprobe: %w", err)
	}
	var probe struct {
		Streams []struct {
			Codec  string `json:"codec_name"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Frames string `json:"nb_read_frames"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(probeBytes, &probe); err != nil {
		return fmt.Errorf("parse ffprobe output: %w", err)
	}
	if len(probe.Streams) != 1 {
		return fmt.Errorf("expected one video stream, got %d", len(probe.Streams))
	}
	stream := probe.Streams[0]
	if stream.Codec != "h264" || stream.Width != width || stream.Height != height || stream.Frames != "48" {
		return fmt.Errorf("output is %s %dx%d %s frames, want h264 %dx%d 48 frames",
			stream.Codec, stream.Width, stream.Height, stream.Frames, width, height)
	}
	if !strict {
		return nil
	}
	sidecarBytes, err := os.ReadFile(path + ".timing.json")
	if err != nil {
		return fmt.Errorf("read strict GPU telemetry: %w", err)
	}
	var sidecar struct {
		Job struct {
			GPU map[string]json.RawMessage `json:"gpu"`
		} `json:"job"`
	}
	if err := json.Unmarshal(sidecarBytes, &sidecar); err != nil {
		return fmt.Errorf("parse strict GPU telemetry: %w", err)
	}
	for key, want := range map[string]string{"effective_backend": "vulkan", "encoder_backend": "nvenc"} {
		var got string
		if err := json.Unmarshal(sidecar.Job.GPU[key], &got); err != nil || got != want {
			return fmt.Errorf("GPU telemetry %s=%q, want %q (parse error: %v)", key, got, want, err)
		}
	}
	for key, want := range map[string]int64{
		"gpu_native_surface_frames": 48, "gpu_native_encode_frames": 48,
		"cpu_pixel_readback_bytes": 0, "gpu_readback_bytes": 0,
		"software_fallback_nodes": 0, "software_encode_frames": 0,
		"encoder_staging_copy_bytes": 0,
	} {
		var got int64
		if err := json.Unmarshal(sidecar.Job.GPU[key], &got); err != nil || got != want {
			return fmt.Errorf("GPU telemetry %s=%d, want %d (parse error: %v)", key, got, want, err)
		}
	}
	return nil
}

func motionCanaryJPEG(t *testing.T, base color.RGBA) []byte {
	t.Helper()
	const width, height = 480, 360
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			stripe := uint8((x/32 + y/32) % 2 * 24)
			img.SetRGBA(x, y, color.RGBA{R: min(base.R+stripe, 255), G: base.G, B: base.B, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		t.Fatalf("encode GPU motion canary image: %v", err)
	}
	return buf.Bytes()
}

func isMultiAccentMotion(id string) bool {
	plugin, err := motion.Registry.Resolve(id)
	if err != nil {
		return false
	}
	dec, ok := plugin.(motion.DeclarativePlugin)
	return ok && len(dec.Definition.LayerComponents) > 1
}
