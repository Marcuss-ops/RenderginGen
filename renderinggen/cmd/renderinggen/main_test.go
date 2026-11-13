package main

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

type recordingWorkerRegistrar struct {
	calls int
	got   queue.Worker
}

func (r *recordingWorkerRegistrar) Register(_ context.Context, worker queue.Worker) error {
	r.calls++
	r.got = worker
	return nil
}

// The queue upsert replaces every column, so the drain update must carry the
// full registration rather than only {id,status}. Losing hostname or versions
// here would make a draining worker indistinguishable in the registry.
func TestMarkWorkerDrainingPreservesIdentity(t *testing.T) {
	registration := queue.Worker{
		ID:                   "worker-1",
		Hostname:             "render-host",
		Status:               queue.WorkerStatusReady,
		RenderingGenVersion:  "1.2.3",
		ChrononVersion:       "4.5.6",
		OverlaySchemaVersion: 7,
		GPUBackend:           "vulkan",
		GPUDevice:            "0",
		GPUDriver:            "nvidia",
	}
	registrar := &recordingWorkerRegistrar{}

	if err := markWorkerDraining(context.Background(), registrar, registration); err != nil {
		t.Fatalf("markWorkerDraining: %v", err)
	}
	if registrar.calls != 1 {
		t.Fatalf("register calls = %d, want 1", registrar.calls)
	}
	if registrar.got.Status != queue.WorkerStatusDraining {
		t.Fatalf("status = %q, want %q", registrar.got.Status, queue.WorkerStatusDraining)
	}
	if registrar.got.ID != registration.ID || registrar.got.Hostname != registration.Hostname {
		t.Fatalf("identity changed: got %+v", registrar.got)
	}
	if registrar.got.RenderingGenVersion != registration.RenderingGenVersion ||
		registrar.got.ChrononVersion != registration.ChrononVersion ||
		registrar.got.OverlaySchemaVersion != registration.OverlaySchemaVersion {
		t.Fatalf("versions changed: got %+v", registrar.got)
	}
	if registrar.got.GPUBackend != registration.GPUBackend ||
		registrar.got.GPUDevice != registration.GPUDevice ||
		registrar.got.GPUDriver != registration.GPUDriver {
		t.Fatalf("gpu identity changed: got %+v", registrar.got)
	}
}
