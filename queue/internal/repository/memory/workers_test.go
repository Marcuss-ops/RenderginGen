package memory

import (
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
)

func TestWorkerRegisterHeartbeatListHealth(t *testing.T) {
	r := New(30*time.Second, 3)

	if err := r.Register(model.Worker{ID: "w1", Hostname: "h1", Status: model.WorkerStatusReady}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(model.Worker{ID: "w2", Status: model.WorkerStatusBusy}); err != nil {
		t.Fatal(err)
	}

	workers, err := r.List()
	if err != nil || len(workers) != 2 {
		t.Fatalf("list: got %d workers, err=%v", len(workers), err)
	}
	if workers[0].ID != "w1" || workers[0].Hostname != "h1" {
		t.Fatalf("worker w1 not round-tripped: %+v", workers[0])
	}

	now := time.Now()
	h, err := r.Health(now, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if h.Ready != 1 || h.Busy != 1 || h.Offline != 0 || h.Total != 2 {
		t.Fatalf("health: %+v", h)
	}

	// Simulate a stale heartbeat on w1.
	r.mu.Lock()
	w := r.workers["w1"]
	w.LastHeartbeatAt = now.Add(-2 * time.Minute)
	r.workers["w1"] = w
	r.mu.Unlock()

	h, err = r.Health(now, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if h.Ready != 0 || h.Busy != 1 || h.Offline != 1 || h.Total != 2 {
		t.Fatalf("health after staleness: %+v", h)
	}
}

func TestWorkerHeartbeatUnregisteredFails(t *testing.T) {
	r := New(30*time.Second, 3)
	if err := r.Heartbeat("missing"); err == nil {
		t.Fatal("heartbeat on unregistered worker should fail")
	}
}
