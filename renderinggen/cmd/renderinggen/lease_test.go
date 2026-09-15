package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/config"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// The lease helpers are the worker's ONLY defence against double rendering: the
// queue requeues a job whose lease expired, so a worker that keeps rendering
// after losing its lease produces a second artifact for one job — exactly the
// outcome a render farm must never have.
//
// These are the first tests in this package. They drive the helpers against a
// real HTTP queue (httptest) so the whole path is exercised — the queue client's
// 409-to-sentinel mapping included — rather than a stub that could agree with a
// wrong implementation.

// fakeQueue is a renewable-job endpoint that counts renew attempts and answers
// with a scripted sequence of statuses.
type fakeQueue struct {
	mu       sync.Mutex
	attempts int
	// status answers attempt n (1-based); the last entry repeats forever. A
	// nil status means 200.
	status []int
	server *httptest.Server
}

func newFakeQueue(t *testing.T, statuses ...int) *fakeQueue {
	t.Helper()
	f := &fakeQueue{status: statuses}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/renew") {
			t.Errorf("unexpected queue request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.mu.Lock()
		f.attempts++
		attempt := f.attempts
		code := http.StatusOK
		if len(f.status) > 0 {
			idx := attempt - 1
			if idx >= len(f.status) {
				idx = len(f.status) - 1
			}
			code = f.status[idx]
		}
		f.mu.Unlock()
		w.WriteHeader(code)
		if code != http.StatusOK && code != http.StatusNoContent {
			_, _ = w.Write([]byte("lease lost"))
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeQueue) client(workerID string) *queue.Client {
	return queue.New(f.server.URL, workerID)
}

func (f *fakeQueue) renewAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// timingsFor builds the renewal budget under test. Attempts/backoff are explicit
// so no test depends on the config defaults.
func timingsFor(attempts int, backoff time.Duration) config.PipelineConfig {
	return config.PipelineConfig{LeaseRenewAttempts: attempts, LeaseRenewBackoff: backoff}
}

// leasedJob is a claimed job holding a lease.
func leasedJob(lease time.Duration) *queue.Job {
	return &queue.Job{ID: "job-1", JobType: queue.JobTypeOverlayRender, Lease: lease}
}

// TestWithLeaseVoidAbortsWorkWhenTheLeaseIsLost is the invariant that matters
// most: a 409 from the queue means another worker owns the job, so the render
// must be cancelled rather than allowed to finish and produce a duplicate.
func TestWithLeaseVoidAbortsWorkWhenTheLeaseIsLost(t *testing.T) {
	f := newFakeQueue(t, http.StatusConflict)
	q := f.client("worker-1")

	jobCtxErr := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = withLeaseVoid(context.Background(), leasedJob(40*time.Millisecond), q, timingsFor(3, time.Millisecond),
			func(jobCtx context.Context) error {
				select {
				case <-jobCtx.Done():
					jobCtxErr <- jobCtx.Err()
					return jobCtx.Err()
				case <-time.After(10 * time.Second):
					jobCtxErr <- errors.New("work ran to completion even though the lease was lost")
					return nil
				}
			})
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("withLeaseVoid never returned after the lease was lost")
	}
	select {
	case err := <-jobCtxErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("job context error = %v, want context.Canceled (the work must be aborted)", err)
		}
	default:
		t.Fatal("the work function never observed the abort")
	}
	if got := f.renewAttempts(); got == 0 {
		t.Fatal("no renewal was attempted; the job would have expired instead of aborting")
	}
}

// TestRenewWithRetryDoesNotRetryAConflict pins that a definitive lease loss is
// attempted exactly ONCE. Retrying a 409 would be the worst possible response:
// the queue has already handed the job to another worker, so every extra attempt
// is time spent rendering a duplicate.
func TestRenewWithRetryDoesNotRetryAConflict(t *testing.T) {
	f := newFakeQueue(t, http.StatusConflict)
	q := f.client("worker-1")

	if held := renewWithRetry(context.Background(), "job-1", q, time.Second, timingsFor(5, time.Millisecond)); held {
		t.Fatal("a conflict must report the lease as lost")
	}
	if got := f.renewAttempts(); got != 1 {
		t.Fatalf("conflict renew attempts = %d, want exactly 1 (a lost lease must not be retried)", got)
	}
}

// TestRenewWithRetryRetriesTransientFailures pins the other side: a queue
// restart or network blip must NOT abort a render that has been running for
// minutes. The retry budget is spent first, and only then is the lease reported
// lost.
func TestRenewWithRetryRetriesTransientFailures(t *testing.T) {
	// Two transient failures, then success — inside a 3-attempt budget.
	f := newFakeQueue(t, http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusOK)
	q := f.client("worker-1")

	if held := renewWithRetry(context.Background(), "job-1", q, time.Second, timingsFor(3, time.Millisecond)); !held {
		t.Fatal("a renewal that succeeded inside the retry budget must report the lease as held")
	}
	if got := f.renewAttempts(); got != 3 {
		t.Fatalf("renew attempts = %d, want 3 (two retries then the success)", got)
	}
}

// TestRenewWithRetryGivesUpAfterTheBudget pins the failure side of the same
// rule: retries are bounded, so a permanently broken queue ends the render
// instead of renewing forever.
func TestRenewWithRetryGivesUpAfterTheBudget(t *testing.T) {
	f := newFakeQueue(t, http.StatusInternalServerError)
	q := f.client("worker-1")

	if held := renewWithRetry(context.Background(), "job-1", q, time.Second, timingsFor(3, time.Millisecond)); held {
		t.Fatal("an exhausted retry budget must report the lease as lost")
	}
	if got := f.renewAttempts(); got != 3 {
		t.Fatalf("renew attempts = %d, want the whole budget of 3", got)
	}
}

// TestRenewWithRetryHonoursCancellation pins that a cancelled job abandons the
// renewal immediately, without waiting out the backoff: once the worker has
// decided to stop, the retry budget is pure delay, and the caller is blocked on
// this call while it runs.
//
// The backoff is deliberately enormous (10s) and the elapsed time is what is
// asserted, because that is the only way to observe "no sleep happened": the
// request itself never reaches the queue once the context is cancelled, so the
// server cannot be asked to count attempts.
func TestRenewWithRetryHonoursCancellation(t *testing.T) {
	f := newFakeQueue(t, http.StatusInternalServerError)
	q := f.client("worker-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	held := renewWithRetry(ctx, "job-1", q, time.Second, timingsFor(5, 10*time.Second))
	elapsed := time.Since(start)

	if held {
		t.Fatal("a cancelled context must report the lease as lost")
	}
	if elapsed > time.Second {
		t.Fatalf("renewWithRetry took %v after cancellation; it waited out the backoff instead of returning", elapsed)
	}
}

// TestWithLeaseVoidRenewsWhileWorkRuns pins the happy path: a job under a lease
// keeps it alive for as long as the work runs, and the work sees a live context.
func TestWithLeaseVoidRenewsWhileWorkRuns(t *testing.T) {
	f := newFakeQueue(t)
	q := f.client("worker-1")

	var sawCancelled atomic.Bool
	err := withLeaseVoid(context.Background(), leasedJob(40*time.Millisecond), q, timingsFor(3, time.Millisecond),
		func(jobCtx context.Context) error {
			// The lease interval is half the lease, so ~100ms covers at least
			// two renewal ticks.
			deadline := time.After(150 * time.Millisecond)
			for {
				select {
				case <-jobCtx.Done():
					sawCancelled.Store(true)
					return jobCtx.Err()
				case <-deadline:
					return nil
				}
			}
		})
	if err != nil {
		t.Fatalf("withLeaseVoid: %v", err)
	}
	if sawCancelled.Load() {
		t.Fatal("the job context was cancelled while the lease was being renewed successfully")
	}
	if got := f.renewAttempts(); got < 1 {
		t.Fatalf("renew attempts = %d, want at least 1", got)
	}
}

// TestWithLeaseVoidWithoutLeaseRunsWithoutRenewal pins the documented shortcut:
// a job that arrived without a lease (a direct caller, or prepare-only work the
// queue does not track) is run as-is, with no HTTP traffic and no context of its
// own to cancel.
func TestWithLeaseVoidWithoutLeaseRunsWithoutRenewal(t *testing.T) {
	f := newFakeQueue(t)
	q := f.client("worker-1")

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var gotCtx context.Context
	err := withLeaseVoid(parent, leasedJob(0), q, timingsFor(3, time.Millisecond), func(jobCtx context.Context) error {
		gotCtx = jobCtx
		return nil
	})
	if err != nil {
		t.Fatalf("withLeaseVoid: %v", err)
	}
	if gotCtx != parent {
		t.Error("a job without a lease must be handed the caller's context, not a new cancellable one")
	}
	if got := f.renewAttempts(); got != 0 {
		t.Fatalf("renew attempts = %d, want 0 for a job without a lease", got)
	}
}

// TestWithLeaseReturnsTheArtifactAndTheError pins both return paths of the
// artifact-returning wrapper, which is what the prepare-only and publication
// paths use.
func TestWithLeaseReturnsTheArtifactAndTheError(t *testing.T) {
	t.Run("success passes the artifact through", func(t *testing.T) {
		f := newFakeQueue(t)
		want := queue.Artifact{StorageKey: "l2/abc.mp4", ArtifactHash: strings.Repeat("a", 64), Width: 1920}
		got, err := withLease(context.Background(), leasedJob(40*time.Millisecond), f.client("worker-1"),
			timingsFor(3, time.Millisecond), func(context.Context) (queue.Artifact, error) {
				return want, nil
			})
		if err != nil {
			t.Fatalf("withLease: %v", err)
		}
		if got.StorageKey != want.StorageKey || got.ArtifactHash != want.ArtifactHash || got.Width != want.Width {
			t.Fatalf("artifact = %+v, want %+v", got, want)
		}
	})
	t.Run("failure returns the error and no artifact", func(t *testing.T) {
		f := newFakeQueue(t)
		wantErr := errors.New("prepare failed")
		got, err := withLease(context.Background(), leasedJob(40*time.Millisecond), f.client("worker-1"),
			timingsFor(3, time.Millisecond), func(context.Context) (queue.Artifact, error) {
				return queue.Artifact{}, wantErr
			})
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want %v", err, wantErr)
		}
		if got.StorageKey != "" || got.ArtifactHash != "" {
			t.Fatalf("artifact = %+v, want the zero value on failure", got)
		}
	})
}

// TestConcurrentLeasesDoNotShareState runs several leased jobs through the
// helpers at once, the way the GPU lanes do. Under -race (CI compiles with it)
// this is the check that the renewal bookkeeping holds no shared mutable state;
// without it, the assertions still pin that each job's outcome is independent of
// its neighbours'.
func TestConcurrentLeasesDoNotShareState(t *testing.T) {
	const jobs = 8

	// One handler, distinguishing jobs by path: half lose their lease, half
	// keep it. A shared-state bug shows up as a job reporting the wrong outcome.
	var conflicts sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "lost") {
			w.WriteHeader(http.StatusConflict)
			return
		}
		conflicts.LoadOrStore(r.URL.Path, struct{}{})
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	q := queue.New(server.URL, "worker-1")

	var wg sync.WaitGroup
	results := make([]error, jobs)
	for i := 0; i < jobs; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := "kept"
			if i%2 == 1 {
				id = "lost"
			}
			job := &queue.Job{ID: id, JobType: queue.JobTypeOverlayRender, Lease: 20 * time.Millisecond}
			results[i] = withLeaseVoid(context.Background(), job, q, timingsFor(2, time.Millisecond),
				func(jobCtx context.Context) error {
					select {
					case <-jobCtx.Done():
						return jobCtx.Err()
					case <-time.After(60 * time.Millisecond):
						return nil
					}
				})
		}()
	}
	wg.Wait()

	for i, err := range results {
		if i%2 == 1 {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("job %d (lease lost) returned %v, want context.Canceled", i, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("job %d (lease held) returned %v, want nil", i, err)
		}
	}
}
