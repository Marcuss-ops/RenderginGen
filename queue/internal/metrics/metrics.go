// Package metrics owns the Prometheus collectors for the central job queue.
// It exposes the operational, realtime aggregates that complement the
// per-job timing already persisted in PostgreSQL (render_attempts and
// processing_metrics): queue depth, render duration, queue wait and lease
// expiry.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

// Metrics holds the registered Prometheus collectors for the queue.
type Metrics struct {
	registry *prometheus.Registry

	JobsPending    prometheus.Gauge
	RenderDuration prometheus.Histogram
	QueueWait      prometheus.Histogram
	LeaseExpired   prometheus.Counter
}

// New constructs a Metrics with its own registry and registers all collectors.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		JobsPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "renderinggen_jobs_pending",
			Help: "Number of jobs currently waiting to be claimed.",
		}),
		RenderDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "renderinggen_render_duration_seconds",
			Help:    "Time from claim to completion for a finished render attempt.",
			Buckets: prometheus.DefBuckets,
		}),
		QueueWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "renderinggen_queue_wait_seconds",
			Help:    "Time a job spent waiting in the queue before being claimed.",
			Buckets: prometheus.DefBuckets,
		}),
		LeaseExpired: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "renderinggen_lease_expired_total",
			Help: "Number of jobs whose lease expired and were requeued or failed.",
		}),
	}
	m.registry.MustRegister(m.JobsPending, m.RenderDuration, m.QueueWait, m.LeaseExpired)
	return m
}

// Handler returns the HTTP handler exposing the metrics in the Prometheus
// text exposition format for scraping.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Gather returns the current metrics snapshot for tests and diagnostics.
func (m *Metrics) Gather() ([]*dto.MetricFamily, error) {
	if m == nil {
		return nil, nil
	}
	return m.registry.Gather()
}
