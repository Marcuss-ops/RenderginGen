package chronon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingLane counts the renders it was handed, so the spreading policy can
// be proven rather than asserted.
type recordingLane struct {
	id    int
	calls atomic.Int64
}

func (l *recordingLane) Render(context.Context, RenderRequest) error {
	l.calls.Add(1)
	return nil
}

// TestSpreadAcrossLanesRoundRobinsInOrder pins the distribution: consecutive
// renders rotate over the lanes, so N sockets each receive ~1/N of the work
// instead of all of it landing on the first socket.
func TestSpreadAcrossLanesRoundRobinsInOrder(t *testing.T) {
	lanes := []*recordingLane{{id: 0}, {id: 1}, {id: 2}}
	renderers := make([]Renderer, 0, len(lanes))
	for _, lane := range lanes {
		renderers = append(renderers, lane)
	}
	spread := spreadAcrossLanes(renderers)
	for i := 0; i < 6; i++ {
		if err := spread.Render(context.Background(), RenderRequest{}); err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
	}
	for _, lane := range lanes {
		if got := lane.calls.Load(); got != 2 {
			t.Errorf("lane %d served %d renders, want 2 (round-robin)", lane.id, got)
		}
	}
}

// blockingLane blocks every render until release closes, recording the peak
// in-flight count it ever saw.
type blockingLane struct {
	release  <-chan struct{}
	inflight atomic.Int64
	peak     atomic.Int64
}

func (l *blockingLane) Render(context.Context, RenderRequest) error {
	current := l.inflight.Add(1)
	for {
		peak := l.peak.Load()
		if current <= peak || l.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	<-l.release
	l.inflight.Add(-1)
	return nil
}

// TestSpreadCapsConcurrencyPerSocket proves the per-socket cap: four renders
// over two single-lane sockets run two at a time, one on each socket, and the
// queued pair waits for the slot instead of opening a third session.
func TestSpreadCapsConcurrencyPerSocket(t *testing.T) {
	release := make(chan struct{})
	lanes := []*blockingLane{{release: release}, {release: release}}
	spread := spreadAcrossLanes([]Renderer{
		LimitConcurrency(lanes[0], 1),
		LimitConcurrency(lanes[1], 1),
	})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = spread.Render(context.Background(), RenderRequest{})
		}()
	}

	// Wait for both sockets to admit their single render.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lanes[0].inflight.Load() == 1 && lanes[1].inflight.Load() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for i, lane := range lanes {
		if got := lane.inflight.Load(); got != 1 {
			t.Fatalf("socket lane %d has %d renders in flight, want exactly 1", i, got)
		}
	}

	close(release)
	wg.Wait()
	for i, lane := range lanes {
		if got := lane.peak.Load(); got != 1 {
			t.Errorf("socket lane %d peak concurrency = %d, want 1", i, got)
		}
	}
}

// heldLane blocks its first render until release closes, so a test can make
// one lane genuinely busy while the policy routes around it.
type heldLane struct {
	release chan struct{}
	calls   atomic.Int64
}

func (l *heldLane) Render(context.Context, RenderRequest) error {
	l.calls.Add(1)
	<-l.release
	return nil
}

// waitCalls polls until the lane has been assigned n renders.
func waitCalls(t *testing.T, lane *recordingLane, n int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lane.calls.Load() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("lane never reached %d assignments (has %d)", n, lane.calls.Load())
}

// TestSpreadAvoidsBusyLanes pins the load-aware half of the policy: while lane
// 0 is mid-render, assignments must go to idle lanes even though the
// historical round-robin cursor would point back at lane 0.
func TestSpreadAvoidsBusyLanes(t *testing.T) {
	held := &heldLane{release: make(chan struct{})}
	lanes := []*recordingLane{{id: 1}, {id: 2}}
	spread := spreadAcrossLanes([]Renderer{held, lanes[0], lanes[1]})

	// Occupy lane 0 (the cursor's first target) with a blocked render.
	done := make(chan error, 1)
	go func() { done <- spread.Render(context.Background(), RenderRequest{}) }()
	deadline := time.Now().Add(2 * time.Second)
	for held.calls.Load() != 1 {
		if !time.Now().Before(deadline) {
			t.Fatal("held lane was never assigned the first render")
		}
		time.Sleep(time.Millisecond)
	}
	// Allow the assignment's atomic bookkeeping to settle before asserting
	// the NEXT pick routes around lane 0.
	time.Sleep(20 * time.Millisecond)

	// Render 2 must skip lane 0 instead of queueing behind it.
	if err := spread.Render(context.Background(), RenderRequest{}); err != nil {
		t.Fatalf("render 2: %v", err)
	}
	if got := lanes[0].calls.Load(); got != 1 {
		t.Fatalf("lane 1 served %d renders, want 1 while lane 0 is busy", got)
	}
	// Render 3 takes the remaining idle lane.
	if err := spread.Render(context.Background(), RenderRequest{}); err != nil {
		t.Fatalf("render 3: %v", err)
	}
	if got := lanes[1].calls.Load(); got != 1 {
		t.Fatalf("lane 2 served %d renders, want 1 while lane 0 is busy", got)
	}
	if held.calls.Load() != 1 {
		t.Fatalf("busy lane 0 was double-assigned: %d renders", held.calls.Load())
	}

	close(held.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("held render: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("held render did not drain")
	}
}

// TestSpreadEvenWhenIdle pins the fairness half: with nothing in flight the
// cursored min-scan still rotates assignment evenly across the lanes.
func TestSpreadEvenWhenIdle(t *testing.T) {
	lanes := []*recordingLane{{id: 0}, {id: 1}, {id: 2}}
	renderers := make([]Renderer, 0, len(lanes))
	for _, lane := range lanes {
		renderers = append(renderers, lane)
	}
	spread := spreadAcrossLanes(renderers)
	for i := 0; i < 9; i++ {
		if err := spread.Render(context.Background(), RenderRequest{}); err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
	}
	for _, lane := range lanes {
		if got := lane.calls.Load(); got != 3 {
			t.Errorf("lane %d served %d renders, want 3", lane.id, got)
		}
	}
}

// TestSpreadAcrossLanesWithoutLanes: a spread with nothing to spread over is an
// error, never a nil dereference or a silently dropped render.
func TestSpreadAcrossLanesWithoutLanes(t *testing.T) {
	if err := spreadAcrossLanes(nil).Render(context.Background(), RenderRequest{}); err == nil {
		t.Fatal("a spread with no lanes must fail rather than drop the render")
	}
}

// TestSpreadAcrossSocketsValidatesItsInput pins the fail-closed constructor:
// each socket is one daemon, so a doubtful list must be rejected at startup
// instead of halving throughput at runtime.
func TestSpreadAcrossSocketsValidatesItsInput(t *testing.T) {
	if _, err := SpreadAcrossSockets(nil, 2); err == nil {
		t.Error("an empty socket list must be rejected")
	}
	if _, err := SpreadAcrossSockets([]string{""}, 2); err == nil {
		t.Error("an empty socket path must be rejected")
	}
	if _, err := SpreadAcrossSockets([]string{"/run/a.sock", "  /run/a.sock  "}, 2); err == nil {
		t.Error("a repeated socket must be rejected: each socket is one daemon")
	}
	spread, err := SpreadAcrossSockets([]string{"/run/a.sock", "/run/b.sock"}, 2)
	if err != nil {
		t.Fatalf("two distinct sockets must be accepted: %v", err)
	}
	if spread == nil {
		t.Fatal("expected a renderer")
	}
}

// TestLanesPerSocketSplitsWhatTheWorkerWasConfiguredFor pins the arithmetic:
// the worker's gpu_lanes are divided over the sockets, and a single socket keeps
// every lane (the historical one-daemon behaviour, unchanged).
func TestLanesPerSocketSplitsWhatTheWorkerWasConfiguredFor(t *testing.T) {
	cases := []struct{ total, sockets, want int }{
		{2, 1, 2}, {1, 1, 1}, {3, 2, 2}, {2, 3, 1}, {4, 2, 2}, {0, 2, 1}, {8, 4, 2},
	}
	for _, tc := range cases {
		if got := LanesPerSocket(tc.total, tc.sockets); got != tc.want {
			t.Errorf("LanesPerSocket(%d, %d) = %d, want %d", tc.total, tc.sockets, got, tc.want)
		}
	}
}
