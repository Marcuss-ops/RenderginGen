package service

import (
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/metrics"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository/memory"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestServiceRecordsQueueMetrics(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	m := metrics.New()
	svc.SetMetrics(m)

	if err := svc.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Submit(model.Job{ID: "job-2"}); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(m.JobsPending); got != 2 {
		t.Fatalf("pending: want 2, got %v", got)
	}

	if _, _, err := svc.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(m.JobsPending); got != 1 {
		t.Fatalf("pending after claim: want 1, got %v", got)
	}

	if err := svc.Complete("job-1", "w1", model.Artifact{StorageKey: "sha", ArtifactHash: "sha", SizeBytes: 1, ContentType: "video/mp4"}); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(m.JobsPending); got != 1 {
		t.Fatalf("pending after complete: want 1 (job-2 still queued), got %v", got)
	}

	if n := histogramSamples(t, m, "renderinggen_queue_wait_seconds"); n != 1 {
		t.Fatalf("queue_wait samples: want 1, got %d", n)
	}
	if n := histogramSamples(t, m, "renderinggen_render_duration_seconds"); n != 1 {
		t.Fatalf("render_duration samples: want 1, got %d", n)
	}
}

func TestServiceRejectsCompletionWithoutArtifact(t *testing.T) {
	svc := New(memory.New(30*time.Second, 3))
	if err := svc.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Complete("job-1", "w1", model.Artifact{}); err == nil {
		t.Fatal("expected empty artifact to be rejected")
	}
	job, err := svc.Get("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.StateRunning {
		t.Fatalf("job state after rejected completion = %q, want running", job.State)
	}
}

func TestServiceLeaseExpiredMetric(t *testing.T) {
	svc := New(memory.New(10*time.Millisecond, 3))
	m := metrics.New()
	svc.SetMetrics(m)

	if err := svc.Submit(model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim("w1"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(30 * time.Millisecond)
	n, err := svc.RequeueExpired(time.Now())
	if err != nil || n != 1 {
		t.Fatalf("requeue expired: n=%d err=%v", n, err)
	}
	if got := testutil.ToFloat64(m.LeaseExpired); got != 1 {
		t.Fatalf("lease_expired: want 1, got %v", got)
	}
}

func histogramSamples(t *testing.T, m *metrics.Metrics, name string) uint64 {
	t.Helper()
	families, err := m.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		if len(f.Metric) == 0 {
			return 0
		}
		return f.Metric[0].GetHistogram().GetSampleCount()
	}
	return 0
}
