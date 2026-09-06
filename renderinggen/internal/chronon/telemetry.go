package chronon

import (
	"encoding/json"
	"fmt"
	"os"
)

// ReadTimingSidecar reads the frame-timing sidecar Chronon writes next to the
// rendered output (`<output>.timing.json`, emitted by the video pipe exporter
// without requiring --report) and returns it as a JSON document for the
// worker's artifact ledger.
//
// Chronon is the source of truth for plan/graph/GPU/encoder timing, so the
// worker ingests the document verbatim (Chronon owns the schema) and only
// records its own distributive phases (materialize, sha256, uploads, total)
// separately. The unbounded per-frame `frame_times_ms` array is dropped here:
// it is deep-profiling detail that stays in the sidecar file itself, keeping
// the ledger row bounded regardless of frame count.
func ReadTimingSidecar(outputPath string) (json.RawMessage, error) {
	return ReadTimingSidecarFile(outputPath + ".timing.json")
}

// ReadTimingSidecarFile reads a timing sidecar from an explicit path.
func ReadTimingSidecarFile(path string) (json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: %w", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: decode: %w", err)
	}
	delete(doc, "frame_times_ms")
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("chronon timing sidecar: re-encode: %w", err)
	}
	return out, nil
}

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

// ReadMediaReceipt reads Chronon's media receipt next to the rendered output.
func ReadMediaReceipt(outputPath string) (MediaReceipt, error) {
	var receipt MediaReceipt
	data, err := os.ReadFile(outputPath + ".receipt.json")
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
