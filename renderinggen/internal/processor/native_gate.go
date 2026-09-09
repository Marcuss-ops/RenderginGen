// native_gate.go owns the gpu-vulkan-native receipt gate: a render certified
// as native Vulkan must prove it in Chronon's BOUNDED telemetry summary
// (`<output>.telemetry-summary.json`, chronon3d.render-telemetry-summary.v1)
// + media receipt before the artifact may be published under the strict
// native identity. The gate never reads Chronon's raw deep-profile timing
// sidecar (opaque artifact), so it cannot drift from the stable summary
// contract (observability ownership, Phase 10).
package processor

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
)

func requireNativeVulkan(outputPath string, expectedFrames int) error {
	// Observability ownership (Phase 10): the gate certifies from Chronon's
	// BOUNDED telemetry summary (`<output>.telemetry-summary.json`), never
	// from the raw deep-profile timing sidecar. ReadTelemetrySummary +
	// DecodeNativeTelemetry reject a missing file and any document that is
	// not the versioned summary schema, so the gate fails closed by
	// construction.
	raw, err := chronon.ReadTelemetrySummary(outputPath)
	if err != nil {
		return fmt.Errorf("missing Chronon telemetry summary: %w", err)
	}
	telemetry, err := chronon.DecodeNativeTelemetry(raw)
	if err != nil {
		return fmt.Errorf("decode Chronon telemetry summary: %w", err)
	}
	doc := telemetry
	directYUV := doc.Job.ExecutionPath == "direct_yuv"
	if !directYUV && doc.Job.GPU.EffectiveBackend != "vulkan" {
		return fmt.Errorf("effective_backend=%q, want vulkan", doc.Job.GPU.EffectiveBackend)
	}
	if doc.Job.GPU.FallbackNodes == nil || *doc.Job.GPU.FallbackNodes != 0 {
		if doc.Job.GPU.FallbackNodes == nil {
			return fmt.Errorf("software_fallback_nodes missing, want 0")
		}
		return fmt.Errorf("software_fallback_nodes=%d, want 0", *doc.Job.GPU.FallbackNodes)
	}
	if doc.Job.GPU.EncoderBackend != "nvenc" {
		return fmt.Errorf("encoder_backend=%q, want nvenc", doc.Job.GPU.EncoderBackend)
	}
	if doc.Job.GPU.CPUReadbackFrames != nil && *doc.Job.GPU.CPUReadbackFrames > 0 {
		return fmt.Errorf("cpu_readback_frames=%d, want 0", *doc.Job.GPU.CPUReadbackFrames)
	}
	if doc.Job.GPU.SoftwareEncodeFrames != nil && *doc.Job.GPU.SoftwareEncodeFrames > 0 {
		return fmt.Errorf("software_encode_frames=%d, want 0", *doc.Job.GPU.SoftwareEncodeFrames)
	}
	if doc.Job.SurfaceHandoffPath != "vulkan_copy" && doc.Job.SurfaceHandoffPath != "direct" {
		return fmt.Errorf("surface_handoff_path=%q, want vulkan_copy or direct", doc.Job.SurfaceHandoffPath)
	}
	if expectedFrames > 0 {
		if doc.Job.GPU.NVENCFrames == nil || *doc.Job.GPU.NVENCFrames != int64(expectedFrames) {
			if doc.Job.GPU.NVENCFrames == nil {
				return fmt.Errorf("nvenc_frames missing, want %d", expectedFrames)
			}
			return fmt.Errorf("nvenc_frames=%d, want %d", *doc.Job.GPU.NVENCFrames, expectedFrames)
		}
		if directYUV {
			if doc.Job.GPU.NativeSurfaceFrames == nil || *doc.Job.GPU.NativeSurfaceFrames != int64(expectedFrames) {
				if doc.Job.GPU.NativeSurfaceFrames == nil {
					return fmt.Errorf("gpu_native_surface_frames missing, want %d", expectedFrames)
				}
				return fmt.Errorf("gpu_native_surface_frames=%d, want %d", *doc.Job.GPU.NativeSurfaceFrames, expectedFrames)
			}
		} else if doc.Job.GPU.VulkanFrames == nil || *doc.Job.GPU.VulkanFrames != int64(expectedFrames) {
			if doc.Job.GPU.VulkanFrames == nil {
				return fmt.Errorf("vulkan_frames missing, want %d", expectedFrames)
			}
			return fmt.Errorf("vulkan_frames=%d, want %d", *doc.Job.GPU.VulkanFrames, expectedFrames)
		}
	}
	receiptRaw, err := os.ReadFile(outputPath + ".receipt.json")
	if err != nil {
		return fmt.Errorf("missing Chronon media receipt: %w", err)
	}
	var receipt map[string]any
	if err := json.Unmarshal(receiptRaw, &receipt); err != nil {
		return fmt.Errorf("decode Chronon media receipt: %w", err)
	}
	// A composited overlay artifact is intentionally not bitstream-copy
	// eligible. The output profile owns that policy; this gate certifies only
	// the GPU execution contract and the presence of Chronon's media receipt.
	return nil
}
