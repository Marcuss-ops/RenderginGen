package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

func submit(t *testing.T, s *Repository, id string) {
	t.Helper()
	job := model.Job{ID: id, RenderPlan: json.RawMessage(`{"n":1}`)}
	if err := s.Submit(job); err != nil {
		t.Fatalf("submit: %v", err)
	}
}

func TestClaimIsFIFOAndExclusive(t *testing.T) {
	s := New(30*time.Second, 3)
	submit(t, s, "job-1")
	submit(t, s, "job-2")

	first, lease, err := s.Claim("w1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if first == nil || first.ID != "job-1" {
		t.Fatalf("want job-1, got %+v", first)
	}
	if lease != 30*time.Second {
		t.Fatalf("want lease 30s, got %s", lease)
	}

	second, _, _ := s.Claim("w2")
	if second == nil || second.ID != "job-2" {
		t.Fatalf("want job-2, got %+v", second)
	}

	if got, _, _ := s.Claim("w3"); got != nil {
		t.Fatalf("queue should be empty, got %+v", got)
	}
}

// TestClaimSkipsAssemblyAnchorParent pins the assembly-anchor rule: a queued
// job that owns chunk children is assembled FROM them and must never be handed
// to a render worker. Without this the anchor (queued first, so FIFO would pick
// it first) renders the full plan on the GPU on top of its children.
func TestClaimSkipsAssemblyAnchorParent(t *testing.T) {
	s := New(30*time.Second, 3)
	submit(t, s, "anchor")
	if err := s.Submit(model.Job{
		ID:          "anchor-chunk-0",
		ParentJobID: "anchor",
		ChunkIndex:  0,
		FrameRange:  &model.FrameRange{Start: 0, End: 120},
		RenderPlan:  json.RawMessage(`{"n":1}`),
	}); err != nil {
		t.Fatalf("submit child: %v", err)
	}
	submit(t, s, "job-2")

	first, _, err := s.Claim("w1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if first == nil || first.ID != "anchor-chunk-0" {
		t.Fatalf("want the chunk, got %+v", first)
	}
	second, _, _ := s.Claim("w2")
	if second == nil || second.ID != "job-2" {
		t.Fatalf("want job-2, got %+v", second)
	}
	if got, _, _ := s.Claim("w3"); got != nil {
		t.Fatalf("the anchor must not be claimable for rendering, got %+v", got)
	}

	// The anchor is still claimable by the FINALIZER, which is the only claim
	// allowed to take it.
	anchor, claimed, err := s.ClaimFinalization("anchor", "finalizer")
	if err != nil {
		t.Fatalf("finalize claim: %v", err)
	}
	if !claimed || anchor == nil {
		t.Fatalf("finalizer must still claim the anchor: claimed=%v job=%+v", claimed, anchor)
	}
}

func TestLeaseExpiryRequeues(t *testing.T) {
	s := New(10*time.Millisecond, 3)
	submit(t, s, "job-1")

	job, _, _ := s.Claim("w1")
	if job == nil {
		t.Fatal("claim returned nil")
	}

	time.Sleep(20 * time.Millisecond)
	n, err := s.RequeueExpired(time.Now())
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 requeued, got %d", n)
	}

	again, _, _ := s.Claim("w2")
	if again == nil || again.ID != "job-1" {
		t.Fatalf("job should be claimable again, got %+v", again)
	}
	if again.Attempts != 2 {
		t.Fatalf("want attempts=2, got %d", again.Attempts)
	}
}

func TestComplete(t *testing.T) {
	s := New(30*time.Second, 3)
	submit(t, s, "job-1")
	if _, _, err := s.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete("job-1", "w1", model.Artifact{}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if s.Stats().Completed != 1 {
		t.Fatalf("want 1 completed, got %+v", s.Stats())
	}
}

func TestCompleteWrongWorkerFails(t *testing.T) {
	s := New(30*time.Second, 3)
	submit(t, s, "job-1")
	if _, _, err := s.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete("job-1", "w2", model.Artifact{}); err == nil {
		t.Fatal("expected error completing with wrong worker")
	}
}

func TestFailRequeuesUntilMaxAttempts(t *testing.T) {
	s := New(30*time.Second, 2)
	submit(t, s, "job-1")

	// Attempt 1 -> requeue.
	if _, _, err := s.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail("job-1", "w1", "boom"); err != nil {
		t.Fatal(err)
	}

	// Attempt 2 -> permanent fail.
	if _, _, err := s.Claim("w2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail("job-1", "w2", "boom"); err != nil {
		t.Fatal(err)
	}

	stats := s.Stats()
	if stats.Failed != 1 || stats.Pending != 0 {
		t.Fatalf("want 1 failed, 0 pending; got %+v", stats)
	}
}

func TestRenewExtendsLease(t *testing.T) {
	s := New(100*time.Millisecond, 3)
	submit(t, s, "job-1")
	if _, _, err := s.Claim("w1"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(60 * time.Millisecond) // near the end of the original lease
	if err := s.Renew("job-1", "w1"); err != nil {
		t.Fatalf("renew: %v", err)
	}
	time.Sleep(60 * time.Millisecond) // past original lease, within renewed lease

	n, err := s.RequeueExpired(time.Now())
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if n != 0 {
		t.Fatalf("job should still be running after renew, got %d requeued", n)
	}
}

func TestRenewWrongWorkerFails(t *testing.T) {
	s := New(30*time.Second, 3)
	submit(t, s, "job-1")
	if _, _, err := s.Claim("w1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Renew("job-1", "w2"); err == nil {
		t.Fatal("expected error renewing with wrong worker")
	}
}

func TestFinalizationLeaseRecoversAfterWorkerLoss(t *testing.T) {
	s := New(10*time.Millisecond, 3)
	submit(t, s, "parent-1")

	claimed, ok, err := s.ClaimFinalization("parent-1", "w1")
	if err != nil || !ok || claimed == nil {
		t.Fatalf("claim finalization: job=%+v ok=%t err=%v", claimed, ok, err)
	}
	if claimed.LeaseUntil.IsZero() {
		t.Fatal("finalizing parent must have a lease")
	}

	time.Sleep(20 * time.Millisecond)
	n, err := s.RequeueExpired(time.Now())
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if n != 1 {
		t.Fatalf("want one finalizing parent recovered, got %d", n)
	}
	if _, ok, err := s.ClaimFinalization("parent-1", "w2"); err != nil || !ok {
		t.Fatalf("reclaimed parent: ok=%t err=%v", ok, err)
	}
}
