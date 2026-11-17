package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

type fakeFamilyReaper struct {
	children  []*queue.Job
	err       error
	cancelled []string
	cancelErr map[string]error
}

func (f *fakeFamilyReaper) Children(context.Context, string) ([]*queue.Job, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.children, nil
}

func (f *fakeFamilyReaper) Cancel(_ context.Context, id string) error {
	if err := f.cancelErr[id]; err != nil {
		return err
	}
	f.cancelled = append(f.cancelled, id)
	return nil
}

func chunkJob(id string, index int, state queue.State, queuedAt time.Time) *queue.Job {
	return &queue.Job{ID: id, ChunkIndex: index, State: state, QueuedAt: queuedAt}
}

// TestReapDeadFamilyCancelsAFamilyThatCanNeverAssemble pins the fix for the
// 2026-11-16 orphan-chunk block: one chunk failed permanently, so the parent
// could never be assembled, but the surviving chunks stayed claimable and the
// recovery sweep re-logged the same warning until an operator cancelled them.
func TestReapDeadFamilyCancelsAFamilyThatCanNeverAssemble(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	q := &fakeFamilyReaper{children: []*queue.Job{
		chunkJob("chunk-0", 0, queue.StateFailed, now.Add(-2*time.Hour)),
		chunkJob("chunk-1", 1, queue.StatePending, now.Add(-2*time.Hour)),
		chunkJob("chunk-2", 2, queue.StateCompleted, now.Add(-2*time.Hour)),
	}}

	if !reapDeadFamily(context.Background(), q, "parent", now) {
		t.Fatal("dead family was not reaped")
	}
	want := []string{"parent", "chunk-1"}
	if len(q.cancelled) != len(want) {
		t.Fatalf("cancelled = %v, want %v", q.cancelled, want)
	}
	for i, id := range want {
		if q.cancelled[i] != id {
			t.Fatalf("cancelled = %v, want %v", q.cancelled, want)
		}
	}
}

// TestReapDeadFamilyLeavesAHealthyFamilyAlone keeps the sweep out of the normal
// fan-in: pending/running/rendered children mean finalization is not due yet.
func TestReapDeadFamilyLeavesAHealthyFamilyAlone(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	q := &fakeFamilyReaper{children: []*queue.Job{
		chunkJob("chunk-0", 0, queue.StateCompleted, now.Add(-time.Minute)),
		chunkJob("chunk-1", 1, queue.StateRunning, now.Add(-time.Minute)),
		chunkJob("chunk-2", 2, queue.StatePending, now.Add(-time.Minute)),
	}}
	if reapDeadFamily(context.Background(), q, "parent", now) {
		t.Fatal("healthy family was reaped")
	}
	if len(q.cancelled) != 0 {
		t.Fatalf("cancelled %v, want nothing", q.cancelled)
	}
}

// TestReapDeadFamilyRespectsTheGraceWindow protects an operator Retry: a
// terminal child that just died is not reaped until the grace period elapsed.
func TestReapDeadFamilyRespectsTheGraceWindow(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	q := &fakeFamilyReaper{children: []*queue.Job{
		chunkJob("chunk-0", 0, queue.StateFailed, now.Add(-time.Minute)),
		chunkJob("chunk-1", 1, queue.StatePending, now.Add(-time.Minute)),
	}}
	if reapDeadFamily(context.Background(), q, "parent", now) {
		t.Fatal("family inside the grace window was reaped")
	}
	if len(q.cancelled) != 0 {
		t.Fatalf("cancelled %v, want nothing", q.cancelled)
	}
}

// TestReapDeadFamilyTakesACancelledChunkAsDead covers the operator path: a
// cancelled chunk means the family is dead even though nothing "failed".
func TestReapDeadFamilyTakesACancelledChunkAsDead(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	q := &fakeFamilyReaper{children: []*queue.Job{
		chunkJob("chunk-0", 0, queue.StateCancelled, now.Add(-time.Hour)),
		chunkJob("chunk-1", 1, queue.StateRendered, now.Add(-time.Hour)),
	}}
	if !reapDeadFamily(context.Background(), q, "parent", now) {
		t.Fatal("family with a cancelled chunk was not reaped")
	}
}

// TestReapDeadFamilyIsBestEffortPerJob: one refused cancel must not stop the
// rest of the family from being cleared.
func TestReapDeadFamilyIsBestEffortPerJob(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	q := &fakeFamilyReaper{
		children: []*queue.Job{
			chunkJob("chunk-0", 0, queue.StateFailed, now.Add(-time.Hour)),
			chunkJob("chunk-1", 1, queue.StatePending, now.Add(-time.Hour)),
			chunkJob("chunk-2", 2, queue.StatePending, now.Add(-time.Hour)),
		},
		cancelErr: map[string]error{"parent": errors.New("cannot cancel terminal job")},
	}
	if !reapDeadFamily(context.Background(), q, "parent", now) {
		t.Fatal("family was not reaped")
	}
	if len(q.cancelled) != 2 {
		t.Fatalf("cancelled %v, want both pending siblings despite the parent error", q.cancelled)
	}
}

// TestReapDeadFamilySurvivesAChildrenReadFailure: an unreadable family is not a
// licence to cancel anything.
func TestReapDeadFamilySurvivesAChildrenReadFailure(t *testing.T) {
	q := &fakeFamilyReaper{err: errors.New("queue unavailable")}
	if reapDeadFamily(context.Background(), q, "parent", time.Now()) {
		t.Fatal("reaped a family it could not read")
	}
	if len(q.cancelled) != 0 {
		t.Fatalf("cancelled %v, want nothing", q.cancelled)
	}
}
