package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
)

func TestWorkerRegisterHeartbeatListHealth(t *testing.T) {
	r, db := setupRepo(t, 30*time.Second, 3)
	if _, err := db.ExecContext(context.Background(), `TRUNCATE rendering_workers CASCADE`); err != nil {
		t.Fatal(err)
	}

	if err := r.Register(model.Worker{ID: "w1", Hostname: "h1", Status: model.WorkerStatusReady, RenderingGenVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(model.Worker{ID: "w2", Status: model.WorkerStatusBusy}); err != nil {
		t.Fatal(err)
	}

	if err := r.Heartbeat("w1"); err != nil {
		t.Fatal(err)
	}

	workers, err := r.List()
	if err != nil || len(workers) != 2 {
		t.Fatalf("list: got %d workers, err=%v", len(workers), err)
	}
	if workers[0].ID != "w1" || workers[0].Hostname != "h1" || workers[0].RenderingGenVersion != "v1" {
		t.Fatalf("worker w1 not round-tripped: %+v", workers[0])
	}
	if workers[0].LastHeartbeatAt.IsZero() {
		t.Fatal("w1 should have a heartbeat timestamp")
	}

	h, err := r.Health(time.Now(), 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if h.Ready != 1 || h.Busy != 1 || h.Offline != 0 || h.Total != 2 {
		t.Fatalf("health: %+v", h)
	}

	// A heartbeat row must have been appended to the ledger.
	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM worker_heartbeats WHERE worker_id = 'w1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want 1 heartbeat row for w1, got %d", count)
	}
}

func TestWorkerHeartbeatUnregisteredFails(t *testing.T) {
	r, _ := setupRepo(t, 30*time.Second, 3)
	if err := r.Heartbeat("missing"); err == nil {
		t.Fatal("heartbeat on unregistered worker should fail")
	}
}
