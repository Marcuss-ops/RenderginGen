// outcome_hook.go owns the process-wide terminal-outcome observer.
//
// It exists because the worker had no scrapeable surface at all: the queue
// exposes render_duration_seconds while the process that spends the GPU time
// answered 404 on /metrics. The phase hook (SetPhaseHook) already carries the
// per-phase timings; this is its sibling for the outcome, so a wiring site in
// cmd/renderinggen can turn both into Prometheus series without the processor
// depending on a metrics library.
//
// WHY PROCESS-WIDE. ReportComplete/ReportFailure are free functions: they are the
// single terminal-report funnel every worker pool calls, and they hold no
// Processor. Making the hook process-wide keeps the funnel single and means a
// new pool cannot forget to instrument its own completion path. The setter is
// called once during startup, before any worker pool goroutine exists, and the
// reader only ever does a nil-check on the value — so the hook is safe for the
// concurrent readers that follow.
package processor

// Job outcome labels passed to the hook. They mirror the worker's reported
// outcomes, not the queue's internal lifecycle.
const (
	JobOutcomeCompleted = "completed"
	JobOutcomeFailed    = "failed"
)

// jobOutcomeHook, when non-nil, receives every terminal outcome the worker
// reports to the queue. nil means "not observed".
var jobOutcomeHook func(outcome string)

// SetJobOutcomeHook installs the terminal-outcome observer. Call it during
// startup, before the worker pools start claiming jobs; passing nil removes the
// observer.
func SetJobOutcomeHook(fn func(outcome string)) {
	jobOutcomeHook = fn
}

// noteJobOutcome publishes one terminal outcome to the observer, if any.
func noteJobOutcome(outcome string) {
	if jobOutcomeHook != nil {
		jobOutcomeHook(outcome)
	}
}
