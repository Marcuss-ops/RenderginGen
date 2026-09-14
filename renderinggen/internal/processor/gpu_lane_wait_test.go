package processor

import (
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
)

// TestRecordGPULaneWaitWritesBothSpellings pins the GPU admission wait onto the
// job's own metrics map: the `_us` form is what the artifact ledger projects,
// the `_ms` form is what the benchmark surface reads, and both must exist or
// the phase would be visible to only one of the two consumers.
func TestRecordGPULaneWaitWritesBothSpellings(t *testing.T) {
	prepared := &PreparedJob{Metrics: map[string]float64{}}
	RecordGPULaneWait(prepared, 27*time.Second)

	if got := prepared.Metrics[metricnames.GPULaneWaitUS]; got != 27_000_000 {
		t.Errorf("%s = %v, want 27000000", metricnames.GPULaneWaitUS, got)
	}
	if got := prepared.Metrics[metricnames.GPULaneWaitMS]; got != 27_000 {
		t.Errorf("%s = %v, want 27000", metricnames.GPULaneWaitMS, got)
	}
	if !metricnames.Declared(metricnames.GPULaneWaitUS) || !metricnames.Declared(metricnames.GPULaneWaitMS) {
		t.Fatal("gpu_lane_wait must be part of the declared metric vocabulary")
	}
	if !metricnames.StemHasPair(metricnames.GPULaneWaitStem) {
		t.Fatal("gpu_lane_wait must declare both unit spellings")
	}
}

// TestRecordGPULaneWaitIsNilSafeAndNeverNegative pins the fail-safe contract:
// a nil prepared job or a negative duration must be a no-op, never a panic and
// never a negative phase (a negative wall would corrupt the report's
// reconciliation instead of merely being absent).
func TestRecordGPULaneWaitIsNilSafeAndNeverNegative(t *testing.T) {
	RecordGPULaneWait(nil, time.Second) // must not panic

	prepared := &PreparedJob{}
	RecordGPULaneWait(prepared, time.Millisecond)
	if prepared.Metrics == nil {
		t.Fatal("a prepared job without a metrics map must get one, not be skipped")
	}
	if got := prepared.Metrics[metricnames.GPULaneWaitMS]; got != 1 {
		t.Errorf("%s = %v, want 1", metricnames.GPULaneWaitMS, got)
	}

	negative := &PreparedJob{Metrics: map[string]float64{}}
	RecordGPULaneWait(negative, -time.Second)
	if len(negative.Metrics) != 0 {
		t.Errorf("a negative wait must not be recorded, got %v", negative.Metrics)
	}
}
