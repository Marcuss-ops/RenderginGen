package chronon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadTimingSidecarDropsPerFrameArray(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "result.mp4")
	sidecar := output + ".timing.json"
	// A minimal sidecar mirroring the chronon3d.frame-timing.v1 shape: the
	// per-frame array is deep-profiling detail, the job/summary sections are
	// the source of truth the worker ingests.
	fixture := `{
  "schema": "chronon3d.frame-timing.v1",
  "wall_time_ms": 5123.0,
  "frames_total": 150,
  "frame_times_ms": [{"frame": 0}, {"frame": 1}],
  "summary": {"p50_frame_ms": 12.3, "end_to_end_fps": 29.2},
  "job": {"plan_compile_ms": 4.1, "graph_compile_ms": 2.2}
}`
	if err := os.WriteFile(sidecar, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := ReadTimingSidecar(output)
	if err != nil {
		t.Fatalf("ReadTimingSidecar: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if _, ok := doc["frame_times_ms"]; ok {
		t.Fatalf("frame_times_ms must be dropped from the ingested document")
	}
	for _, key := range []string{"schema", "wall_time_ms", "frames_total", "summary", "job"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("ingested document lost key %q: %s", key, got)
		}
	}
}

func TestReadTimingSidecarMissingFile(t *testing.T) {
	if _, err := ReadTimingSidecar(filepath.Join(t.TempDir(), "result.mp4")); err == nil {
		t.Fatal("expected an error for a missing sidecar")
	}
}

// TestReadMediaReceiptTiming verifies the worker parses Chronon's receipt
// timing_ms (the policy-controlled verification phases) and projects the
// measured phases onto the chronon_receipt_* metric namespace, omitting the
// decode phase that Chronon skips under the default fast policy (-1 sentinel)
// so reports never see a fabricated decode measurement.
func TestReadMediaReceiptTiming(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "out.mp4")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(output+name, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write(".receipt.json", `{
	  "schema": "chronon3d.render-receipt.v1",
	  "output": {"bytes": 12345, "sha256": "abc123"},
	  "timing_ms": {"sha256_ms": 9.5, "probe_ms": 41.25, "decode_ms": -1.0, "total_ms": 52.0}
	}`)

	receipt, err := ReadMediaReceipt(output)
	if err != nil {
		t.Fatalf("ReadMediaReceipt: %v", err)
	}
	metrics := receipt.ReceiptTimingMetrics()
	if got := metrics["chronon_receipt_sha256_ms"]; got != 9.5 {
		t.Errorf("sha256_ms = %v, want 9.5", got)
	}
	if got := metrics["chronon_receipt_probe_ms"]; got != 41.25 {
		t.Errorf("probe_ms = %v, want 41.25", got)
	}
	if got := metrics["chronon_receipt_total_ms"]; got != 52.0 {
		t.Errorf("total_ms = %v, want 52.0", got)
	}
	if _, present := metrics["chronon_receipt_decode_ms"]; present {
		t.Errorf("decode_ms present (%v), want omitted for the fast-policy -1 sentinel", metrics["chronon_receipt_decode_ms"])
	}

	// Certify/normal policy measured a real decode + count-frames pass: both
	// phases are projected, and the verification block labels the run.
	write(".receipt.json", `{
	  "schema": "chronon3d.render-receipt.v1",
	  "output": {"bytes": 12345, "sha256": "abc123"},
	  "verification": {"requested_policy": "certify", "resolved_policy": "certify", "status": "pass"},
	  "timing_ms": {"sha256_ms": 9.5, "probe_ms": 240.0, "count_frames_ms": 780.5, "decode_ms": 3100.75, "total_ms": 4140.0}
	}`)
	receipt, err = ReadMediaReceipt(output)
	if err != nil {
		t.Fatalf("ReadMediaReceipt (certify): %v", err)
	}
	metrics = receipt.ReceiptTimingMetrics()
	if got := metrics["chronon_receipt_decode_ms"]; got != 3100.75 {
		t.Errorf("decode_ms = %v, want 3100.75", got)
	}
	if got := metrics["chronon_receipt_count_frames_ms"]; got != 780.5 {
		t.Errorf("count_frames_ms = %v, want 780.5", got)
	}
	vmetrics := receipt.VerificationMetrics()
	if got := vmetrics["chronon_receipt_verification_policy"]; got != 3 {
		t.Errorf("verification_policy = %v, want 3 (certify)", got)
	}
	if got := vmetrics["chronon_receipt_verification_status"]; got != 1 {
		t.Errorf("verification_status = %v, want 1 (pass)", got)
	}
}

// TestReadMediaReceiptFastLabelsPolicy verifies the fast policy (the
// production default) is recorded on the receipt so reports can label the run
// without inferring the policy from the absence of decode timings.
func TestReadMediaReceiptFastLabelsPolicy(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "out.mp4")
	fixture := `{
	  "schema": "chronon3d.render-receipt.v1",
	  "output": {"bytes": 12345, "sha256": "abc123"},
	  "verification": {"requested_policy": "", "resolved_policy": "fast", "status": "pass"},
	  "timing_ms": {"sha256_ms": 9.5, "probe_ms": 41.25, "count_frames_ms": -1.0, "decode_ms": -1.0, "total_ms": 52.0}
	}`
	if err := os.WriteFile(output+".receipt.json", []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	receipt, err := ReadMediaReceipt(output)
	if err != nil {
		t.Fatalf("ReadMediaReceipt: %v", err)
	}
	vmetrics := receipt.VerificationMetrics()
	if got := vmetrics["chronon_receipt_verification_policy"]; got != 1 {
		t.Errorf("verification_policy = %v, want 1 (fast)", got)
	}
	// Decode/count-frames never ran under fast: the -1 sentinels are omitted.
	metrics := receipt.ReceiptTimingMetrics()
	for _, absent := range []string{"chronon_receipt_decode_ms", "chronon_receipt_count_frames_ms"} {
		if _, present := metrics[absent]; present {
			t.Errorf("%s present (%v), want omitted under fast", absent, metrics[absent])
		}
	}
}

// TestReadMediaReceiptLegacyCountFramesAbsent pins the schema-upgrade
// boundary: a receipt written before count_frames_ms existed has no such key;
// it must decode to the -1 "did not run" sentinel (never a fabricated 0 ms
// measurement) and therefore project nothing.
func TestReadMediaReceiptLegacyCountFramesAbsent(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "out.mp4")
	fixture := `{
	  "schema": "chronon3d.render-receipt.v1",
	  "output": {"bytes": 12345, "sha256": "abc123"},
	  "timing_ms": {"sha256_ms": 9.5, "probe_ms": 41.25, "decode_ms": -1.0, "total_ms": 52.0}
	}`
	if err := os.WriteFile(output+".receipt.json", []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	receipt, err := ReadMediaReceipt(output)
	if err != nil {
		t.Fatalf("ReadMediaReceipt: %v", err)
	}
	if receipt.Timing.CountFramesMS != -1 {
		t.Errorf("CountFramesMS = %v, want -1 for a pre-schema receipt", receipt.Timing.CountFramesMS)
	}
	if _, present := receipt.ReceiptTimingMetrics()["chronon_receipt_count_frames_ms"]; present {
		t.Errorf("count_frames_ms projected from a legacy receipt without the key")
	}
}
