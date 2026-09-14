// gpu_lane_wait.go owns the GPU admission wait: how long a fully prepared job
// sat in the prep→GPU rendezvous before a lane picked it up.
//
// WHY IT EXISTS
// -------------
// The worker's three-stage pipeline (prep pool → GPU lanes → post pool) makes
// `total_ms` cover MORE than the measured phases: `totalStart` is taken at the
// beginning of PrepareJob, so everything a job spends queued behind other jobs
// for the GPU lane lands in `total_ms` and in NO phase metric. On a real
// 5-clip batch a single clip reported
//
//	total_ms = 36694   (RenderingGen worker wall)
//	chronon job wall   =  8201  (the daemon's own measured render)
//	measured phases    ≈  1090  (prepare + probe + publish)
//
// i.e. ~27 s — 74% of that clip's wall — was the lane wait, invisible. That is
// the number that decides whether more GPU lanes help: the RenderingGen lane
// count only matters while the downstream Chronon daemon can actually run
// renders concurrently, and the wait is what grows when it cannot.
//
// OWNERSHIP
// ---------
// The wait is measured in the worker main (the rendezvous lives there) and
// recorded on the job's own metrics map through this helper, so the artifact
// ledger and the queue carry it next to the phases it explains. It is a
// diagnostic nested inside the worker wall: consumers must never add it on top
// of `total_ms`.
package processor

import (
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
)

// RecordGPULaneWait records the prep→GPU rendezvous wait on the prepared job's
// metrics, in both declared spellings (the `_us` form is what the artifact
// ledger projects, the `_ms` form is what the benchmark surface reads). A
// negative or absurd input is ignored rather than written as a bogus phase.
func RecordGPULaneWait(prepared *PreparedJob, wait time.Duration) {
	if prepared == nil || wait < 0 {
		return
	}
	if prepared.Metrics == nil {
		prepared.Metrics = map[string]float64{}
	}
	us := float64(wait.Microseconds())
	prepared.Metrics[metricnames.GPULaneWaitUS] = us
	prepared.Metrics[metricnames.GPULaneWaitMS] = us / 1000
}
