package service

import (
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/metrics"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository/memory"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestServiceWorkerHealthMetrics(t *testing.T) {
	mem := memory.New(30*time.Second, 3)
	svc := New(mem)
	svc.SetWorkerRepository(mem, 90*time.Second)
	m := metrics.New()
	svc.SetMetrics(m)

	if err := svc.RegisterWorker(model.Worker{ID: "w1", Status: model.WorkerStatusReady}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RegisterWorker(model.Worker{ID: "w2", Status: model.WorkerStatusBusy}); err != nil {
		t.Fatal(err)
	}

	if got := testutil.ToFloat64(m.WorkersReady); got != 1 {
		t.Fatalf("workers_ready: want 1, got %v", got)
	}
	if got := testutil.ToFloat64(m.WorkersOffline); got != 0 {
		t.Fatalf("workers_offline: want 0, got %v", got)
	}

	if err := svc.WorkerHeartbeat("w1"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(m.WorkersReady); got != 1 {
		t.Fatalf("workers_ready after heartbeat: want 1, got %v", got)
	}
}
