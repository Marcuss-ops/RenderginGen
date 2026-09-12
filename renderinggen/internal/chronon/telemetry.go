package chronon

import (
	"encoding/json"
	"fmt"
	"os"
)

// ═══════════════════════════════════════════════════════════════════════════
// Observability ownership (Phase 10): Chronon owns telemetry. Chronon emits
// TWO documents per render:
//
//   * `<output>.telemetry-summary.json` — the BOUNDED, schema-typed summary
//     (chronon3d.render-telemetry-summary.v1). This is the ONLY Chronon
//     telemetry surface this worker ingests. It never contains per-frame
//     arrays; the worker records it verbatim (Chronon owns the schema) and
//     projects only its documented numeric subset onto metrics.
//   * `<output>.timing.json` — the RAW deep-profile sidecar. The worker
//     treats it exactly like an MP4: bytes → SHA-256 → object store → small
//     reference. It NEVER parses, mutates or re-transports its internals
//     (preserveRawTimingSidecar in the processor does the opaque handling).
//
// RenderingGen therefore never has to know Chronon's raw profile schema, and
// Chronon may evolve its deep profiler without breaking the worker.
// ═══════════════════════════════════════════════════════════════════════════

// TelemetrySummarySchema is the stable schema of Chronon's bounded telemetry
// summary sidecar (`<output>.telemetry-summary.json`).
const TelemetrySummarySchema = "chronon3d.render-telemetry-summary.v1"

// TelemetrySummarySuffix is the file suffix of the bounded summary sidecar.
const TelemetrySummarySuffix = ".telemetry-summary.json"

// RawTimingSidecarContentType is the content type under which the RAW
// deep-profile timing sidecar (`<output>.timing.json`) is preserved. It is an
// opaque, Chronon-owned artifact: consumers store it and carry the reference;
// they never interpret the body.
const RawTimingSidecarContentType = "application/vnd.chronon.timing+json"

// ReadTelemetrySummary reads the bounded telemetry summary sidecar Chronon
// writes next to the rendered output and returns the document VERBATIM (no
// parse/mutate/re-encode) after validating its schema header. The summary is
// the single bounded telemetry surface a host may ingest; the raw deep-profile
// sidecar is handled opaquely elsewhere (bytes → hash → object store → ref).
func ReadTelemetrySummary(outputPath string) (json.RawMessage, error) {
	return ReadTelemetrySummaryFile(outputPath + TelemetrySummarySuffix)
}

// ReadTelemetrySummaryFile reads a bounded telemetry summary from an explicit
// path and validates the schema header.
func ReadTelemetrySummaryFile(path string) (json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("chronon telemetry summary: %w", err)
	}
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("chronon telemetry summary: decode: %w", err)
	}
	if header.Schema != TelemetrySummarySchema {
		return nil, fmt.Errorf("chronon telemetry summary: schema %q, want %q",
			header.Schema, TelemetrySummarySchema)
	}
	return data, nil
}

// NativeTelemetry is the bounded summary slice the gpu-vulkan-native receipt
// gate certifies: execution identity (path/backends/surface handoff) plus the
// strict counters that must prove zero fallback, zero readback and a full
// native NVENC pass. Pointer fields are used so a MISSING counter is
// distinguishable from a measured zero — the gate fails closed on absence.
type NativeTelemetry struct {
	Schema string `json:"schema"`
	Job    struct {
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

// DecodeNativeTelemetry decodes a bounded telemetry summary for the native
// gate. It rejects documents that are not the bounded summary schema, so the
// gate can never accidentally certify from the raw deep-profile sidecar (or
// from any future unversioned shape).
func DecodeNativeTelemetry(raw json.RawMessage) (NativeTelemetry, error) {
	var telemetry NativeTelemetry
	if len(raw) == 0 {
		return telemetry, fmt.Errorf("chronon telemetry summary: empty document")
	}
	if err := json.Unmarshal(raw, &telemetry); err != nil {
		return telemetry, fmt.Errorf("chronon telemetry summary: decode: %w", err)
	}
	if telemetry.Schema != TelemetrySummarySchema {
		return telemetry, fmt.Errorf("chronon telemetry summary: schema %q, want %q",
			telemetry.Schema, TelemetrySummarySchema)
	}
	return telemetry, nil
}

// TelemetryMetrics projects the DOCUMENTED numeric subset of the bounded
// telemetry summary onto the artifact metrics namespace (chronon_ prefix,
// flattened path). This is the explicit, stable projection — never a generic
// walk that scrapes every numeric leaf: consumers get exactly the fields this
// list names, and Chronon may add/remove internal fields without changing the
// projection contract.
func TelemetryMetrics(raw json.RawMessage) map[string]float64 {
	out := make(map[string]float64)
	if len(raw) == 0 {
		return out
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return out
	}
	project := func(name string, path ...string) {
		var current any = doc
		for _, segment := range path {
			obj, ok := current.(map[string]any)
			if !ok {
				return
			}
			current, ok = obj[segment]
			if !ok {
				return
			}
		}
		value, ok := current.(float64)
		if !ok {
			return
		}
		out[name] = value
	}

	// summary statistics.
	for _, key := range []string{"mean_frame_ms", "p50_frame_ms", "p90_frame_ms",
		"p95_frame_ms", "p99_frame_ms", "steady_avg_ms", "render_only_fps",
		"render_loop_fps", "end_to_end_fps", "measured_fps", "realtime_factor",
		"frame_budget_ms", "frames_over_budget", "over_budget_ratio", "target_fps"} {
		project("chronon_summary_"+key, "summary", key)
	}
	// job walls (ms).
	for _, key := range []string{"process_wall_ms", "job_wall_ms", "engine_init_ms",
		"backend_init_ms", "plan_compile_ms", "graph_compile_ms", "prepare_ms",
		"render_loop_wall_ms", "encoder_finalize_ms", "mux_finalize_ms",
		"output_finalize_ms", "validation_ms", "ffprobe_ms", "sha256_ms",
		"sidecar_report_ms"} {
		project("chronon_job_"+key, "job", key)
	}
	// GPU counters / waits (zero-copy + native gates consume these names).
	for _, key := range []string{"software_fallback_nodes", "cpu_readback_frames",
		"software_encode_frames", "nvenc_frames", "vulkan_frames",
		"gpu_native_surface_frames", "gpu_native_encode_frames",
		"bitstream_copy_frames", "video_pipe_fallback_frames",
		"video_native_fallback_frames", "video_decode_native_surface_frames",
		"video_decode_software_frames", "cpu_pixel_readback_frames",
		"cpu_pixel_readback_bytes", "gpu_readback_bytes", "gpu_upload_bytes",
		"cuda_host_upload_bytes", "nv12_to_rgba_frames", "rgba_to_nv12_frames",
		"gpu_surface_copy_frames", "native_surface_reuse_count",
		"video_composite_ms", "decode_wait_ms", "frame_slot_wait_ms"} {
		project("chronon_job_gpu_"+key, "job", "gpu", key)
	}
	// encoder backpressure (native path).
	project("chronon_job_encoder_backpressure_wait_ms", "job", "encoder",
		"backpressure_wait_ms")
	return out
}

// MediaReceiptSuffix is the file suffix of Chronon's media-receipt sidecar
// (`<output>.receipt.json`). It is the SINGLE definition of the receipt path
// contract: ReadMediaReceipt and ReadReceiptPresence both build their path
// from it, so no caller re-derives the suffix and the two cannot disagree about
// where the receipt lives.
const MediaReceiptSuffix = ".receipt.json"

// MediaReceipt is the identity + verification section of Chronon's
// render-receipt sidecar (`<output>.receipt.json`, schema
// chronon3d.render-receipt.v1). Chronon computes the output SHA-256 itself,
// so the worker can verify identity without re-reading the rendered file.
// The timing_ms block carries the measured post-render verification phases
// (policy-controlled: under the default "fast" policy decode_ms and
// count_frames_ms are -1 and no full re-decode of the freshly muxed output
// happens). The verification block records the policy Chronon was asked to
// run (requested_policy) and the level it actually ran (resolved_policy)
// plus the aggregate pass/fail status, so consumers never infer the executed
// policy from the presence/absence of decode timings.
type MediaReceipt struct {
	Schema string `json:"schema"`
	Output struct {
		Bytes  int64  `json:"bytes"`
		SHA256 string `json:"sha256"`
	} `json:"output"`
	Verification struct {
		RequestedPolicy string `json:"requested_policy"`
		ResolvedPolicy  string `json:"resolved_policy"`
		Status          string `json:"status"`
		// Granular per-check verdicts ("pass" | "fail" | "skip"). Parsed so
		// the worker can report WHICH contract check rejected a rendered
		// output instead of only the aggregate status.
		FFprobe     string `json:"ffprobe"`
		Decode      string `json:"decode"`
		FrameCount  string `json:"frame_count"`
		Codec       string `json:"codec"`
		PixelFormat string `json:"pixel_format"`
		Resolution  string `json:"resolution"`
		FPS         string `json:"fps"`
		Audio       string `json:"audio"`
	} `json:"verification"`
	Timing struct {
		SHA256MS      float64 `json:"sha256_ms"`
		ProbeMS       float64 `json:"probe_ms"`
		CountFramesMS float64 `json:"count_frames_ms"`
		DecodeMS      float64 `json:"decode_ms"`
		TotalMS       float64 `json:"total_ms"`
	} `json:"timing_ms"`
}

// ResolvedVerificationPolicy is the verification level the receipt records as
// actually executed ("fast" | "normal" | "certify"). Empty when the receipt
// carries no verification block (pre-policy receipts).
func (r MediaReceipt) ResolvedVerificationPolicy() string {
	return r.Verification.ResolvedPolicy
}

// VerificationPassed reports whether the receipt's aggregate verification
// status is "pass". A receipt without a verification block is not a pass
// (callers decide how to treat unknown).
func (r MediaReceipt) VerificationPassed() bool {
	return r.Verification.Status == "pass"
}

// VerificationFailures lists the granular checks whose verdict is "fail", in
// stable order. A receipt with a failing aggregate but no granular verdicts
// (or only "skip"/"pass" checks) is a schema mismatch the caller must decide
// how to treat; VerificationFailures returns nil there and the caller falls
// back to the aggregate status.
func (r MediaReceipt) VerificationFailures() []string {
	var checks = []struct {
		name    string
		verdict string
	}{
		{"ffprobe", r.Verification.FFprobe},
		{"decode", r.Verification.Decode},
		{"frame_count", r.Verification.FrameCount},
		{"codec", r.Verification.Codec},
		{"pixel_format", r.Verification.PixelFormat},
		{"resolution", r.Verification.Resolution},
		{"fps", r.Verification.FPS},
		{"audio", r.Verification.Audio},
	}
	var out []string
	for _, check := range checks {
		if check.verdict == "fail" {
			out = append(out, check.name)
		}
	}
	return out
}

// ReceiptTimingMetrics projects the receipt's measured verification phases
// onto the artifact metrics namespace with the chronon_receipt_ prefix, so
// PipelineGen's per-clip reports can attribute the post-render receipt cost
// (probe, optional decode/SHA-256, total) instead of hiding it inside the
// render wall. Chronon uses -1.0 as the sentinel for a phase that did not
// run (e.g. decode_ms under the default fast policy); a sentinel phase is
// omitted, never reported as a fabricated measurement.
func (r MediaReceipt) ReceiptTimingMetrics() map[string]float64 {
	out := make(map[string]float64, 5)
	put := func(key string, ms float64) {
		if ms >= 0 {
			out[key] = ms
		}
	}
	put("chronon_receipt_sha256_ms", r.Timing.SHA256MS)
	put("chronon_receipt_probe_ms", r.Timing.ProbeMS)
	put("chronon_receipt_count_frames_ms", r.Timing.CountFramesMS)
	put("chronon_receipt_decode_ms", r.Timing.DecodeMS)
	put("chronon_receipt_total_ms", r.Timing.TotalMS)
	return out
}

// verificationPolicyCodes is the canonical numeric encoding of the receipt's
// resolved verification policy for the worker's float64 metric map (the
// queue/report channel carries numbers only; the readable label lives in the
// receipt JSON itself). fast=1, normal=2, certify=3.
var verificationPolicyCodes = map[string]float64{
	"fast":    1,
	"normal":  2,
	"certify": 3,
}

// VerificationMetrics projects the receipt's verification policy + status
// onto the artifact metrics namespace:
//
//	chronon_receipt_verification_policy  numeric code of resolved_policy
//	                                     (fast=1, normal=2, certify=3)
//	chronon_receipt_verification_status  1 = aggregate pass, 0 = fail
//
// Reports use verification_policy to label every run (fast vs certify)
// instead of inferring the policy from whether receipt_decode_ms exists.
// A receipt without a verification block adds nothing.
func (r MediaReceipt) VerificationMetrics() map[string]float64 {
	out := make(map[string]float64, 2)
	policy := r.ResolvedVerificationPolicy()
	if code, ok := verificationPolicyCodes[policy]; ok {
		out["chronon_receipt_verification_policy"] = code
	}
	if r.Verification.Status == "pass" {
		out["chronon_receipt_verification_status"] = 1
	} else if r.Verification.Status == "fail" {
		out["chronon_receipt_verification_status"] = 0
	}
	return out
}

// ReadReceiptPresence verifies that Chronon emitted its media receipt next to
// the rendered output and that it is a JSON object, WITHOUT decoding or
// trusting its identity fields. Gates whose contract is only "Chronon wrote its
// receipt" (e.g. the native-Vulkan certification) use this instead of
// re-implementing "read <output>.receipt.json and json-unmarshal it" a second
// time; callers that need the output size/SHA-256 must use ReadMediaReceipt.
func ReadReceiptPresence(outputPath string) error {
	data, err := os.ReadFile(outputPath + MediaReceiptSuffix)
	if err != nil {
		return fmt.Errorf("chronon media receipt: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("chronon media receipt: decode: %w", err)
	}
	return nil
}

// ReadMediaReceipt reads Chronon's media receipt next to the rendered output.
func ReadMediaReceipt(outputPath string) (MediaReceipt, error) {
	var receipt MediaReceipt
	data, err := os.ReadFile(outputPath + MediaReceiptSuffix)
	if err != nil {
		return receipt, fmt.Errorf("chronon media receipt: %w", err)
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return receipt, fmt.Errorf("chronon media receipt: decode: %w", err)
	}
	// count_frames_ms joined the schema after sha/probe/decode/total. A
	// receipt written by an older binary has no such key, which would decode
	// as a fabricated 0 ms measurement; normalize it to the -1 "did not run"
	// sentinel so metrics projection stays honest for legacy receipts.
	var presence struct {
		Timing map[string]json.RawMessage `json:"timing_ms"`
	}
	_ = json.Unmarshal(data, &presence)
	if _, present := presence.Timing["count_frames_ms"]; !present {
		receipt.Timing.CountFramesMS = -1
	}
	if receipt.Output.SHA256 == "" || receipt.Output.Bytes <= 0 {
		return receipt, fmt.Errorf("chronon media receipt: missing output identity")
	}
	return receipt, nil
}
