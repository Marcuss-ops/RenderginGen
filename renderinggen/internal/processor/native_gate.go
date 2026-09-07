// native_gate.go owns the gpu-vulkan-native receipt gate: a render certified
// as native Vulkan must prove it in Chronon's timing + media receipts before
// the artifact may be published under the strict native identity.
package processor

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
)

func requireNativeVulkan(outputPath string, expectedFrames int) error {
	raw, err := chronon.ReadTimingSidecar(outputPath)
	if err != nil {
		return fmt.Errorf("missing Chronon timing receipt: %w", err)
	}
	var doc struct {
		Job struct {
			ExecutionPath      string `json:"execution_path"`
			SurfaceHandoffPath string `json:"surface_handoff_path"`
			GPU                struct {
				EffectiveBackend     string `json:"effective_backend"`
				EncoderBackend       string `json:"encoder_backend"`
				FallbackNodes        *int64 `json:"software_fallback_nodes"`
				CPUReadbackFrames    *int64 `json:"cpu_readback_frames"`
				SoftwareEncodeFrames *int64 `json:"software_encode_frames"`
				NVENCFrames          *int64 `json:"nvenc_frames"`
				VulkanFrames         *int64 `json:"vulkan_frames"`
				NativeSurfaceFrames  *int64 `json:"gpu_native_surface_frames"`
			} `json:"gpu"`
		} `json:"job"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode Chronon timing receipt: %w", err)
	}
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
