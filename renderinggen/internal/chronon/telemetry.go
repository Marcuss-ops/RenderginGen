package chronon

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ═══════════════════════════════════════════════════════════════════════════
// Observability ownership (Phase 10): Chronon owns telemetry. The engine has
// shipped TWO generations of that contract:
//
//   * `<output>.telemetry-summary.json` (chronon3d.render-telemetry-summary.v1)
//     — the historical BOUNDED summary. Chronon3d's architecture contract now
//     FORBIDS this artifact (`timing_sidecar_v2_is_the_only_schema`), so a
//     current engine never writes it; it is still read when a legacy engine
//     produces it.
//   * `<output>.timing.json` (chronon3d.frame-timing.v2) — the canonical
//     single telemetry artifact of the current engine. It carries the bounded
//     summary INLINE (`summary` + `job` sections, the same field paths the
//     legacy summary used) alongside the unbounded per-frame array.
//
// This file ingests whichever the engine produced. From the v2 sidecar it
// takes ONLY the bounded `schema`/`version`/`summary`/`job` sections — the
// per-frame array never enters the ledger — and it re-derives nothing: every
// value is Chronon's own, transported verbatim. Independently, the processor
// preserves the whole sidecar opaquely (bytes → hash → object store → ref).
// ═══════════════════════════════════════════════════════════════════════════

// TelemetrySummarySchema is the schema of Chronon's historical bounded
// telemetry summary sidecar (`<output>.telemetry-summary.json`).
const TelemetrySummarySchema = "chronon3d.render-telemetry-summary.v1"

// TelemetrySummarySuffix is the file suffix of the bounded summary sidecar.
const TelemetrySummarySuffix = ".telemetry-summary.json"

// TimingSidecarSchemaV2 is the schema of Chronon's canonical frame-timing
// sidecar (`<output>.timing.json`) — the engine's ONLY telemetry artifact.
const TimingSidecarSchemaV2 = "chronon3d.frame-timing.v2"

// TimingSidecarSuffix is the file suffix of the frame-timing sidecar.
const TimingSidecarSuffix = ".timing.json"

// boundedTimingSections are the sections of the v2 sidecar that form the
// bounded telemetry document. Everything else (notably the unbounded
// `frame_times_ms` array) stays in the opaque artifact.
var boundedTimingSections = []string{"schema", "version", "summary", "job"}

// RawTimingSidecarContentType is the content type under which the RAW
// deep-profile timing sidecar (`<output>.timing.json`) is preserved. It is an
// opaque, Chronon-owned artifact: consumers store it and carry the reference;
// they never interpret the body.
const RawTimingSidecarContentType = "application/vnd.chronon.timing+json"

// ReadTelemetrySummary reads the bounded telemetry document Chronon wrote next
// to the rendered output. A legacy engine's summary is returned VERBATIM (no
// parse/mutate/re-encode) after its schema header validates; a current engine's
// v2 frame-timing sidecar is projected onto its own bounded sections. A host
// that finds neither document fails closed — it never falls back to scraping a
// log.
func ReadTelemetrySummary(outputPath string) (json.RawMessage, error) {
	raw, legacyErr := ReadTelemetrySummaryFile(outputPath + TelemetrySummarySuffix)
	if legacyErr == nil {
		return raw, nil
	}
	raw, sidecarErr := ReadTimingSidecarTelemetry(outputPath + TimingSidecarSuffix)
	if sidecarErr == nil {
		return raw, nil
	}
	return nil, fmt.Errorf("%v; %v", legacyErr, sidecarErr)
}

// ReadTimingSidecarTelemetry reads Chronon's v2 frame-timing sidecar from an
// explicit path and returns its BOUNDED telemetry projection: the engine's own
// schema/version/summary/job sections, verbatim, with the unbounded per-frame
// array deliberately dropped. A document that is not the v2 schema, or that
// carries no job section (the certification surface), is rejected.
func ReadTimingSidecarTelemetry(path string) (json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: %w", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: decode: %w", err)
	}
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: decode schema: %w", err)
	}
	if header.Schema != TimingSidecarSchemaV2 {
		return nil, fmt.Errorf("chronon timing sidecar: schema %q, want %q",
			header.Schema, TimingSidecarSchemaV2)
	}
	bounded := make(map[string]json.RawMessage, len(boundedTimingSections))
	for _, section := range boundedTimingSections {
		if value, ok := doc[section]; ok {
			bounded[section] = value
		}
	}
	if _, ok := bounded["job"]; !ok {
		return nil, fmt.Errorf("chronon timing sidecar: v2 document carries no job section")
	}
	out, err := json.Marshal(bounded)
	if err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: bound sections: %w", err)
	}
	return out, nil
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

// DecodeNativeTelemetry decodes a bounded telemetry document for the native
// gate. It accepts exactly the two certified schemas — the historical bounded
// summary and the current v2 frame-timing sidecar — and rejects everything
// else, so the gate can never accidentally certify from a raw v1 deep-profile
// document or from any future unversioned shape.
func DecodeNativeTelemetry(raw json.RawMessage) (NativeTelemetry, error) {
	var telemetry NativeTelemetry
	if len(raw) == 0 {
		return telemetry, fmt.Errorf("chronon telemetry summary: empty document")
	}
	if err := json.Unmarshal(raw, &telemetry); err != nil {
		return telemetry, fmt.Errorf("chronon telemetry summary: decode: %w", err)
	}
	if telemetry.Schema != TelemetrySummarySchema && telemetry.Schema != TimingSidecarSchemaV2 {
		return telemetry, fmt.Errorf("chronon telemetry summary: schema %q, want %q or %q",
			telemetry.Schema, TelemetrySummarySchema, TimingSidecarSchemaV2)
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
	Media struct {
		Container          string  `json:"container"`
		Codec              string  `json:"codec"`
		VideoCodec         string  `json:"video_codec"`
		CodecProfile       string  `json:"codec_profile"`
		VideoLevel         string  `json:"video_level"`
		PixelFormat        string  `json:"pixel_format"`
		Width              int     `json:"width"`
		Height             int     `json:"height"`
		FPSNum             int     `json:"fps_num"`
		FPSDen             int     `json:"fps_den"`
		RFPSNum            int     `json:"r_fps_num"`
		RFPSDen            int     `json:"r_fps_den"`
		FrameCount         int64   `json:"frame_count"`
		DurationMS         float64 `json:"duration_ms"`
		DurationUS         int64   `json:"duration_us"`
		HasAudio           bool    `json:"has_audio"`
		HasVideo           bool    `json:"has_video"`
		VideoStreams       int     `json:"video_streams"`
		VideoTimeBaseNum   int     `json:"video_time_base_num"`
		VideoTimeBaseDen   int     `json:"video_time_base_den"`
		SARNum             int     `json:"sar_num"`
		SARDen             int     `json:"sar_den"`
		ColorRange         string  `json:"color_range"`
		ColorSpace         string  `json:"color_space"`
		ColorTransfer      string  `json:"color_transfer"`
		ColorPrimaries     string  `json:"color_primaries"`
		FieldOrder         string  `json:"field_order"`
		StartPTS           int64   `json:"start_pts"`
		FirstFrameKeyframe bool    `json:"first_frame_keyframe"`
		ClosedGOP          bool    `json:"closed_gop"`
		KeyframeInterval   int     `json:"keyframe_interval"`
		AudioStreams       int     `json:"audio_streams"`
		AudioCodec         string  `json:"audio_codec"`
		AudioProfile       string  `json:"audio_profile"`
		SampleRate         int     `json:"sample_rate"`
		Channels           int     `json:"channels"`
		ChannelLayout      string  `json:"channel_layout"`
		AudioBitrate       string  `json:"audio_bitrate"`
		AudioTimeBaseNum   int     `json:"audio_time_base_num"`
		AudioTimeBaseDen   int     `json:"audio_time_base_den"`
	} `json:"media"`
}

// HasCanonicalMedia reports whether the receipt carries authoritative structural media facts.
func (r MediaReceipt) HasCanonicalMedia() bool {
	return r.Media.Container != "" && r.Media.Width > 0 && r.Media.Height > 0
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

// Verification policies RenderingGen can request from Chronon. They are the
// wire vocabulary shared by every carrier of the policy:
//
//	RenderRequest.ReceiptVerify / CHRONON_RECEIPT_VERIFY  (worker -> Chronon)
//	<output>.receipt.json verification.requested_policy   (Chronon -> worker)
//	<output>.receipt.json verification.resolved_policy    (Chronon -> worker)
//
// They are declared HERE, next to the numeric encoding, because chronon is the
// module both sides of that boundary already import: the processor's policy
// authority aliases them instead of restating the literals, so a new policy or
// a rename cannot leave the worker and the daemon vocabulary disagreeing.
const (
	ReceiptVerifyFast    = "fast"
	ReceiptVerifyNormal  = "normal"
	ReceiptVerifyCertify = "certify"
)

// RequiresStructuredReply reports whether a verification policy promises proof
// of the render and therefore requires a parsable render reply from the daemon.
// fast is the compatibility tier (a foreign/older daemon may answer with a
// plain "ok" body) and tolerates an unparsable reply; normal and certify claim
// a full decode, so a daemon that cannot describe the result of the render it
// was asked to prove cannot be credited with having rendered it.
func RequiresStructuredReply(policy string) bool {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case ReceiptVerifyNormal, ReceiptVerifyCertify:
		return true
	default:
		return false
	}
}

// verificationPolicyCodes is the canonical numeric encoding of the receipt's
// resolved verification policy for the worker's float64 metric map (the
// queue/report channel carries numbers only; the readable label lives in the
// receipt JSON itself). fast=1, normal=2, certify=3.
var verificationPolicyCodes = map[string]float64{
	ReceiptVerifyFast:    1,
	ReceiptVerifyNormal:  2,
	ReceiptVerifyCertify: 3,
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
