// Package workermetrics owns the native GPU worker's Prometheus surface.
//
// Why it exists: the QUEUE exposes /metrics (renderinggen_jobs_pending,
// render_duration_seconds, …) while the WORKER — the process that actually
// spends the GPU time — answered 404 on its admin port. An operator triaging
// "where did the time go" could therefore see how much work was queued and never
// how long the phases inside a render took, which is the one thing the worker
// knows and nobody else does.
//
// The surface is deliberately fed from EXISTING instrumentation instead of a
// second measurement: the processor already reports every phase through
// SetPhaseHook (processor.recordPhase) and reports every terminal outcome
// through ReportComplete/ReportFailure. This package only carries those
// observations to Prometheus, so nothing is timed twice and no series exists
// here that the pipeline does not really produce. Series that were not wired are
// not declared — an always-empty counter reads as "no failures" to a dashboard
// operator and is worse than its absence.
package workermetrics

import (
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Namespace is the metric prefix. It matches the queue's `renderinggen_` prefix
// so both surfaces can be read with one query family; the subsystem segment keeps
// the worker's series distinguishable from the queue's.
const Namespace = "renderinggen"

// Outcome labels for CountOutcome. They name what the WORKER reported, which is
// per attempt, not per distinct job: the queue owns the job-level lifecycle and
// already counts it (renderinggen_jobs_*), and duplicating that here would give
// two sources for one number.
const (
	OutcomeCompleted = "completed"
	OutcomeFailed    = "failed"
)

// Metrics is the worker's collector set. A nil *Metrics is safe: every method
// tolerates it, so an unwired worker keeps running exactly as it did.
type Metrics struct {
	outcomesTotal *prometheus.CounterVec
	phaseSecond   *prometheus.HistogramVec
	registry      *prometheus.Registry
}

// PhaseBuckets covers a phase from ~50 ms to ~2 min. Render phases on the
// measured jobs range from tens of milliseconds (hashing, object-store upload)
// to tens of seconds (GPU render), so a single bucketing that resolves both is
// the point of using a histogram: percentiles aggregate across workers without
// the workers having to agree on a quantile first.
var PhaseBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120}

// New builds the collector set. It uses a dedicated registry rather than the
// process default, so the worker's series cannot be polluted by (or pollute) an
// unrelated package that registers on the default one — and so the exposition
// contains exactly the worker's metrics plus the Go/process collectors.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry: reg,
		outcomesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: "worker",
			Name:      "job_outcomes_total",
			Help:      "Terminal outcomes the worker reported to the queue (per attempt, not per distinct job).",
		}, []string{"outcome"}),
		phaseSecond: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Subsystem: "worker",
			Name:      "phase_duration_seconds",
			Help:      "Wall-clock duration of each pipeline phase, as measured by the processor's phase hook.",
			Buckets:   PhaseBuckets,
		}, []string{"phase"}),
	}
	reg.MustRegister(m.outcomesTotal, m.phaseSecond)
	reg.MustRegister(prometheus.NewGoCollector())
	return m
}

// Handler returns the /metrics exposition handler for the worker's admin port.
func (m *Metrics) Handler() http.Handler {
	if m == nil || m.registry == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "metrics are not wired on this worker", http.StatusServiceUnavailable)
		})
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// CountOutcome records one terminal outcome the worker reported. An unknown
// value is folded into one bounded label so a typo cannot mint unbounded series.
func (m *Metrics) CountOutcome(outcome string) {
	if m == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case OutcomeCompleted:
		m.outcomesTotal.WithLabelValues(OutcomeCompleted).Inc()
	case OutcomeFailed:
		m.outcomesTotal.WithLabelValues(OutcomeFailed).Inc()
	default:
		m.outcomesTotal.WithLabelValues("unknown").Inc()
	}
}

// ObservePhase records one phase duration under its canonical stem.
func (m *Metrics) ObservePhase(phase string, d time.Duration) {
	if m == nil {
		return
	}
	phase = strings.TrimSpace(phase)
	if phase == "" {
		return
	}
	m.phaseSecond.WithLabelValues(phase).Observe(d.Seconds())
}

// PhaseHook adapts ObservePhase to processor.SetPhaseHook, so the worker's
// metrics are fed by the pipeline's existing measurement rather than a parallel
// one.
func (m *Metrics) PhaseHook() func(phase string, d time.Duration) {
	return func(phase string, d time.Duration) { m.ObservePhase(phase, d) }
}

// OutcomeHook adapts CountOutcome to processor.SetJobOutcomeHook.
func (m *Metrics) OutcomeHook() func(outcome string) {
	return func(outcome string) { m.CountOutcome(outcome) }
}
