package processor

import (
	"encoding/json"
	"testing"
)

// The bounded telemetry summary (chronon3d.render-telemetry-summary.v1) has a
// DOCUMENTED numeric subset: mergeTelemetrySummaryMetrics projects exactly the
// documented keys onto the artifact metrics with the chronon_ prefix, so
// PipelineGen can read render/GPU KPI-class numbers directly from
// GET /jobs/{id} without a SQL migration per new Chronon metric — and never
// scrapes arbitrary internal leaves (observability ownership, Phase 10).
func TestMergeTelemetrySummaryMetricsProjectsDocumentedSubset(t *testing.T) {
	summary := json.RawMessage(`{
	  "schema": "chronon3d.render-telemetry-summary.v1",
	  "version": 1,
	  "summary": {"render_loop_fps": 29.2, "end_to_end_fps": 28.9, "p95_frame_ms": 41.2, "frames_over_budget": 3},
	  "job": {
	    "process_wall_ms": 5304.0, "render_loop_wall_ms": 2932.0, "mux_finalize_ms": 88.1,
	    "gpu": {"nvenc_frames": 450, "vulkan_frames": 450, "software_fallback_nodes": 0,
	            "cpu_readback_frames": 0, "gpu_readback_bytes": 0, "gpu_upload_bytes": 2048,
	            "cuda_host_upload_bytes": 0, "nv12_to_rgba_frames": 0, "rgba_to_nv12_frames": 0},
	    "encoder": {"backpressure_wait_ms": 1.25}
	  },
	  "outcome": {"status": "ok"}
	}`)
	dst := map[string]float64{}
	mergeTelemetrySummaryMetrics(dst, summary)

	want := map[string]float64{
		"chronon_summary_render_loop_fps":      29.2,
		"chronon_summary_end_to_end_fps":       28.9,
		"chronon_summary_p95_frame_ms":         41.2,
		"chronon_summary_frames_over_budget":   3,
		"chronon_job_process_wall_ms":          5304.0,
		"chronon_job_render_loop_wall_ms":      2932.0,
		"chronon_job_mux_finalize_ms":          88.1,
		"chronon_job_gpu_nvenc_frames":         450,
		"chronon_job_gpu_vulkan_frames":        450,
		"chronon_job_gpu_software_fallback_nodes": 0,
		"chronon_job_gpu_cpu_readback_frames":  0,
		"chronon_job_gpu_gpu_readback_bytes":   0,
		"chronon_job_gpu_gpu_upload_bytes":     2048,
		"chronon_job_gpu_cuda_host_upload_bytes": 0,
		"chronon_job_gpu_nv12_to_rgba_frames":  0,
		"chronon_job_gpu_rgba_to_nv12_frames":  0,
		"chronon_job_encoder_backpressure_wait_ms": 1.25,
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

func TestMergeTelemetrySummaryMetricsIgnoresUndocumentedLeaves(t *testing.T) {
	// Internal/undocumented leaves (strings, arrays, unlisted numbers) must
	// never appear: the projection is an explicit contract, not a scrape.
	summary := json.RawMessage(`{
	  "schema": "chronon3d.render-telemetry-summary.v1",
	  "summary": {"backend": "vulkan", "internal_debug_hint_ms": 999.0},
	  "job": {"gpu": {"undocumented_frames": 12}},
	  "outcome": {"status": "ok"}
	}`)
	dst := map[string]float64{}
	mergeTelemetrySummaryMetrics(dst, summary)
	if len(dst) != 0 {
		t.Fatalf("undocumented leaves must not be projected: %v", dst)
	}
}

func TestMergeTelemetrySummaryMetricsInvalidJSONIsNoop(t *testing.T) {
	dst := map[string]float64{"keep": 1}
	mergeTelemetrySummaryMetrics(dst, json.RawMessage(`{not json`))
	if len(dst) != 1 || dst["keep"] != 1 {
		t.Fatalf("invalid JSON must be a no-op: %v", dst)
	}
}

func TestMergeTelemetrySummaryMetricsEmptyInputs(t *testing.T) {
	// Empty raw summary and nil dst must both be safe no-ops.
	mergeTelemetrySummaryMetrics(nil, json.RawMessage(`{"a": 1}`))
	dst := map[string]float64{}
	mergeTelemetrySummaryMetrics(dst, nil)
	mergeTelemetrySummaryMetrics(dst, json.RawMessage{})
	if len(dst) != 0 {
		t.Fatalf("empty inputs must not populate dst: %v", dst)
	}
}

// The acceptance gate for the GPU hot path reads these projected names: a
// DirectYUV render must show zero readback and zero colorspace-conversion
// fallback counters alongside full NVENC frame counts.
func TestMergeTelemetrySummaryMetricsDirectYuvAcceptanceShape(t *testing.T) {
	summary := json.RawMessage(`{
	  "schema": "chronon3d.render-telemetry-summary.v1",
	  "summary": {"render_loop_fps": 60.0},
	  "job": {
	    "execution_path": "direct_yuv",
	    "gpu": {"nvenc_frames": 900, "software_fallback_nodes": 0,
	            "cpu_readback_frames": 0, "gpu_readback_bytes": 0,
	            "nv12_to_rgba_frames": 0, "rgba_to_nv12_frames": 0}
	  }
	}`)
	dst := map[string]float64{}
	mergeTelemetrySummaryMetrics(dst, summary)

	if dst["chronon_job_gpu_nvenc_frames"] <= 0 {
		t.Fatalf("nvenc_frames must be > 0 for the native path: %v", dst)
	}
	for _, zeroKey := range []string{
		"chronon_job_gpu_gpu_readback_bytes",
		"chronon_job_gpu_nv12_to_rgba_frames",
		"chronon_job_gpu_rgba_to_nv12_frames",
		"chronon_job_gpu_software_fallback_nodes",
	} {
		if v, ok := dst[zeroKey]; !ok || v != 0 {
			t.Fatalf("%s must exist and be 0 (DirectYUV = zero copy, zero fallback): %v", zeroKey, dst)
		}
	}
	if dst["chronon_summary_render_loop_fps"] == 0 {
		t.Fatalf("render_loop_fps must be present and non-zero: %v", dst)
	}
}
