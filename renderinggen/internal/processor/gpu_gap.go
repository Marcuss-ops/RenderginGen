// gpu_gap.go owns the GPU duty-cycle KPI (gpu_gap_us). It is scoped to the
// Processor instance, not to package-level state: the worker's GPU lanes and
// the serial StagedRender path share one Processor, so "the gap between one
// render's end and the next render's start on this worker" is measured per
// processor — never polluted by other Processors (e.g. tests running in the
// same process), and never misattributed across workers.
package processor

import "time"

// recordGPUGap closes the gap since the previous render end recorded on this
// processor (0 on first use). RunGPU calls it with this render's start.
func (p *Processor) recordGPUGap(renderStart time.Time) float64 {
	p.gpuGapMu.Lock()
	defer p.gpuGapMu.Unlock()
	var gapUS float64
	if !p.gpuGapLastRenderEnd.IsZero() && renderStart.After(p.gpuGapLastRenderEnd) {
		gapUS = float64(renderStart.Sub(p.gpuGapLastRenderEnd).Microseconds())
	}
	return gapUS
}

// PutGPURenderEnd stores the completion time of the most recent render on this
// processor. Exported so the concurrent GPU lane in the worker main can record
// render ends exactly like the serial StagedRender path does.
func (p *Processor) PutGPURenderEnd(renderEnd time.Time) {
	p.gpuGapMu.Lock()
	p.gpuGapLastRenderEnd = renderEnd
	p.gpuGapMu.Unlock()
}
