package workermetrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", rec.Code)
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestExpositionCarriesPhaseHistogramAndOutcome pins the two things the worker
// had no way to say: how long each pipeline PHASE took, and what happened to the
// job. Both are fed from the processor's existing instrumentation.
func TestExpositionCarriesPhaseHistogramAndOutcome(t *testing.T) {
	m := New()
	m.ObservePhase("render", 1500*time.Millisecond)
	m.CountOutcome(OutcomeCompleted)
	m.CountOutcome(OutcomeFailed)

	body := scrape(t, m)
	for _, want := range []string{
		"renderinggen_worker_phase_duration_seconds_count{phase=\"render\"} 1",
		"renderinggen_worker_phase_duration_seconds_bucket{phase=\"render\",le=\"2\"} 1",
		"renderinggen_worker_job_outcomes_total{outcome=\"completed\"} 1",
		"renderinggen_worker_job_outcomes_total{outcome=\"failed\"} 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition is missing %q\n---\n%s", want, body)
		}
	}
}

// TestUnknownOutcomeFoldsIntoOneBoundedLabel pins that a typo cannot mint an
// unbounded number of series (a caller passing a job id by mistake would
// otherwise create one series per job).
func TestUnknownOutcomeFoldsIntoOneBoundedLabel(t *testing.T) {
	m := New()
	m.CountOutcome("job_1790095581868067261_4eb69ac2")
	m.CountOutcome("")
	body := scrape(t, m)
	if !strings.Contains(body, `renderinggen_worker_job_outcomes_total{outcome="unknown"} 2`) {
		t.Errorf("unknown outcomes must fold into one label; got:\n%s", body)
	}
	for _, forbidden := range []string{"job_1790", `outcome=""`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("exposition leaked %q: labels must stay bounded", forbidden)
		}
	}
}

// TestNilMetricsIsSafeAndHandlerSaysSo pins the unwired contract: a nil collector
// never panics, and its handler reports unavailability instead of exposing an
// empty document that a scrape would read as "no failures, no renders".
func TestNilMetricsIsSafeAndHandlerSaysSo(t *testing.T) {
	var m *Metrics
	m.ObservePhase("render", time.Second)
	m.CountOutcome(OutcomeFailed)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired /metrics = %d, want 503", rec.Code)
	}
}

// TestEmptyPhaseIsNotRecorded pins that an unset stem cannot create a "" series,
// which no dashboard can filter for and which would silently absorb the timing.
func TestEmptyPhaseIsNotRecorded(t *testing.T) {
	m := New()
	m.ObservePhase("   ", time.Second)
	if body := scrape(t, m); strings.Contains(body, `phase=""`) {
		t.Errorf("an empty phase name must not become a series:\n%s", body)
	}
}
