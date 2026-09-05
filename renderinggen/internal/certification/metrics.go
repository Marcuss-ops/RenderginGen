// Package certification contains renderer-neutral certification gates.
package certification

import "fmt"

// FrameMetrics is the per-frame lifecycle sample emitted by a Chronon adapter.
type FrameMetrics struct {
	Frame              int64 `json:"frame"`
	FrameTransientLive int64 `json:"frame_transient_live"`
	JobPersistentLive  int64 `json:"job_persistent_live"`
	VRAMBytes          int64 `json:"vram_bytes"`
	SurfacesCreated    int64 `json:"surfaces_created"`
	SurfacesReleased   int64 `json:"surfaces_released"`
}

// MemoryGate rejects the known transient-surface leak and unbounded VRAM
// growth. linearGrowthLimit is an absolute byte budget for the sample window.
func MemoryGate(samples []FrameMetrics, linearGrowthLimit int64) error {
	if len(samples) == 0 {
		return fmt.Errorf("memory metrics are missing")
	}
	for _, s := range samples {
		if s.FrameTransientLive != 0 {
			return fmt.Errorf("frame %d has %d live FrameTransient resources", s.Frame, s.FrameTransientLive)
		}
		if s.SurfacesReleased > s.SurfacesCreated {
			return fmt.Errorf("frame %d released more surfaces than created", s.Frame)
		}
	}
	if live := samples[len(samples)-1].JobPersistentLive; live != 0 {
		return fmt.Errorf("job end has %d live persistent resources", live)
	}
	if linearGrowthLimit >= 0 && len(samples) > 1 {
		growth := samples[len(samples)-1].VRAMBytes - samples[0].VRAMBytes
		if growth > linearGrowthLimit {
			return fmt.Errorf("VRAM grew by %d bytes, limit is %d", growth, linearGrowthLimit)
		}
	}
	return nil
}
