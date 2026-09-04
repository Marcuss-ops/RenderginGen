package processor

import (
	"encoding/json"
	"testing"
)

// The sidecar shape mirrors chronon3d.frame-timing.v1: nested sections with
// numeric leaves. mergeChrononNumericMetrics projects every numeric leaf onto
// the artifact metrics with a chronon_ prefix and a flattened path, so
// PipelineGen can read render_ms/encode_ms/composite_ms-class KPIs directly
// from GET /jobs/{id} without a SQL migration per new Chronon metric.
func TestMergeChrononNumericMetricsFlattensNestedNumbers(t *testing.T) {
	raw := json.RawMessage(`{
	  "summary": {"render_ms": 1234.5, "encode_ms": 567.25, "frames": 450},
	  "gpu": {"cuda": {"composite_frames": 450}, "vram_peak_mb": 812.5},
	  "zero_copy": {"gpu_readback_bytes": 0, "nv12_to_rgba_frames": 0, "rgba_to_nv12_frames": 0}
	}`)
	dst := map[string]float64{}
	mergeChrononNumericMetrics(dst, raw)

	want := map[string]float64{
		"chronon_summary_render_ms":             1234.5,
		"chronon_summary_encode_ms":             567.25,
		"chronon_summary_frames":                450,
		"chronon_gpu_cuda_composite_frames":     450,
		"chronon_gpu_vram_peak_mb":              812.5,
		"chronon_zero_copy_gpu_readback_bytes":  0,
		"chronon_zero_copy_nv12_to_rgba_frames": 0,
		"chronon_zero_copy_rgba_to_nv12_frames": 0,
	}
	for key, val := range want {
		got, ok := dst[key]
		if !ok {
			t.Fatalf("missing projected metric %q (got %v)", key, dst)
		}
		if got != val {
			t.Fatalf("%s = %v, want %v", key, got, val)
		}
	}
}

func TestMergeChrononNumericMetricsSkipsNonNumerics(t *testing.T) {
	raw := json.RawMessage(`{
	  "summary": {"backend": "vulkan", "degraded": false, "phases": [1, 2, 3], "render_ms": 42}
	}`)
	dst := map[string]float64{}
	mergeChrononNumericMetrics(dst, raw)

	if _, ok := dst["chronon_summary_backend"]; ok {
		t.Fatalf("string leaves must not be projected: %v", dst)
	}
	if _, ok := dst["chronon_summary_degraded"]; ok {
		t.Fatalf("bool leaves must not be projected: %v", dst)
	}
	if _, ok := dst["chronon_summary_phases"]; ok {
		t.Fatalf("array leaves must not be projected: %v", dst)
	}
	if dst["chronon_summary_render_ms"] != 42 {
		t.Fatalf("numeric sibling lost: %v", dst)
	}
}

func TestMergeChrononNumericMetricsInvalidJSONIsNoop(t *testing.T) {
	dst := map[string]float64{"keep": 1}
	mergeChrononNumericMetrics(dst, json.RawMessage(`{not json`))
	if len(dst) != 1 || dst["keep"] != 1 {
		t.Fatalf("invalid JSON must be a no-op: %v", dst)
	}
}

func TestMergeChrononNumericMetricsEmptyInputs(t *testing.T) {
	// Empty raw sidecar and nil dst must both be safe no-ops.
	mergeChrononNumericMetrics(nil, json.RawMessage(`{"a": 1}`))
	dst := map[string]float64{}
	mergeChrononNumericMetrics(dst, nil)
	mergeChrononNumericMetrics(dst, json.RawMessage{})
	if len(dst) != 0 {
		t.Fatalf("empty inputs must not populate dst: %v", dst)
	}
}

// The acceptance gate for the GPU hot path reads these projected names: a
// DirectYUV render must show zero readback and zero colorspace-conversion
// fallback counters alongside a non-zero CUDA composite count.
func TestMergeChrononNumericMetricsDirectYuvAcceptanceShape(t *testing.T) {
	raw := json.RawMessage(`{
	  "summary": {"render_ms": 9000, "encode_ms": 2200, "effective_backend": "vulkan"},
	  "gpu": {"cuda": {"composite_frames": 900}},
	  "zero_copy": {"gpu_readback_bytes": 0, "nv12_to_rgba_frames": 0, "rgba_to_nv12_frames": 0},
	  "fallback": {"software_fallback_nodes": 0}
	}`)
	dst := map[string]float64{}
	mergeChrononNumericMetrics(dst, raw)

	if dst["chronon_gpu_cuda_composite_frames"] <= 0 {
		t.Fatalf("cuda_composite_frames must be > 0 for the DirectYUV path: %v", dst)
	}
	for _, zeroKey := range []string{
		"chronon_zero_copy_gpu_readback_bytes",
		"chronon_zero_copy_nv12_to_rgba_frames",
		"chronon_zero_copy_rgba_to_nv12_frames",
		"chronon_fallback_software_fallback_nodes",
	} {
		if v, ok := dst[zeroKey]; !ok || v != 0 {
			t.Fatalf("%s must exist and be 0 (DirectYUV = zero copy, zero fallback): %v", zeroKey, dst)
		}
	}
	if dst["chronon_summary_render_ms"] == 0 || dst["chronon_summary_encode_ms"] == 0 {
		t.Fatalf("render_ms/encode_ms must be present and non-zero: %v", dst)
	}
}
