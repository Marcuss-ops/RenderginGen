// gpu_gap_hook.go owns the optional GPU duty-cycle observer: the sibling of
// Options.PhaseHook that carries the value behind the per-job gpu_gap_us KPI to
// a scrapeable surface.
//
// Why it exists: gpu_gap_us already answers "did the GPU idle between two
// renders, and for how long?" — but only per job, inside the artifact metrics
// map. An operator triaging throughput had to query per-job rows to learn the
// distribution, which is exactly the question a histogram answers. The value is
// NOT re-measured here: the hook receives the same number recordGPUGap returned
// to the job, so no second clock exists and the series cannot drift from the
// per-job metric.
//
// WHY PROCESSOR-SCOPED. gpuGapLastRenderEnd is per Processor (the worker's GPU
// lanes and the serial StagedRender path share one), so a process-wide hook
// would let two processors overwrite each other's observer. The hook is set at
// construction (Options.GPUGapHook) before any pool goroutine claims a job;
// readers only nil-check the value afterwards.
package processor

import "time"

// noteGPUGap publishes one measured gap to the observer, if any. A negative or
// zero gap is still published: "the previous render ended just now" is a real
// observation (back-to-back pipelining), and the first render on a fresh
// processor reports 0 for the same reason the per-job metric does.
func (p *Processor) noteGPUGap(gap time.Duration) {
	if p == nil || p.gpuGapHook == nil {
		return
	}
	p.gpuGapHook(gap)
}
