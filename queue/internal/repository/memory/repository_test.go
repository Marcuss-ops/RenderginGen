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

// TestChildrenIndexCoversEveryInsertPath pins the children index against the one
// failure mode it can have: an insert path that forgets to index a child. The
// anchor rule is expressed through that index, so a family whose child arrived
// through an un-indexed path would silently become claimable for rendering — the
// exact double-render the rule exists to prevent — and a test that only used one
// submit path would not notice, because the full scan the index replaced would
// have found it.
func TestChildrenIndexCoversEveryInsertPath(t *testing.T) {
	newParent := func(id string) model.Job {
		return model.Job{ID: id, RenderPlan: json.RawMessage(`{"n":1}`)}
	}
	newChild := func(parent string) model.Job {
		return model.Job{
			ID:          parent + "-chunk-0",
			ParentJobID: parent,
			ChunkIndex:  0,
			FrameRange:  &model.FrameRange{Start: 0, End: 120},
			RenderPlan:  json.RawMessage(`{"n":1}`),
		}
	}

	cases := []struct {
		name   string
		parent string
		insert func(t *testing.T, s *Repository, parent, kid model.Job)
	}{
		{
			name:   "Submit",
			parent: "p-submit",
			insert: func(t *testing.T, s *Repository, parent, kid model.Job) {
				if err := s.Submit(parent); err != nil {
					t.Fatalf("submit parent: %v", err)
				}
				if err := s.Submit(kid); err != nil {
					t.Fatalf("submit child: %v", err)
				}
			},
		},
		{
			name:   "SubmitIdempotent",
			parent: "p-idempotent",
			insert: func(t *testing.T, s *Repository, parent, kid model.Job) {
				if _, _, err := s.SubmitIdempotent(parent); err != nil {
					t.Fatalf("idempotent parent: %v", err)
				}
				if _, _, err := s.SubmitIdempotent(kid); err != nil {
					t.Fatalf("idempotent child: %v", err)
				}
			},
		},
		{
			name:   "SubmitBatch",
			parent: "p-batch",
			insert: func(t *testing.T, s *Repository, parent, kid model.Job) {
				if err := s.SubmitBatch([]model.Job{parent, kid}); err != nil {
					t.Fatalf("submit batch: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(30*time.Second, 3)
			parent, kid := newParent(tc.parent), newChild(tc.parent)
			tc.insert(t, s, parent, kid)

			children, err := s.Children(parent.ID)
			if err != nil {
				t.Fatalf("children: %v", err)
			}
			if len(children) != 1 || children[0].ID != kid.ID {
				t.Fatalf("Children(%s) = %d job(s), want exactly the child %s", parent.ID, len(children), kid.ID)
			}
			if children[0].ParentJobID != parent.ID {
				t.Fatalf("child %s lost its parent id", children[0].ID)
			}

			// The anchor must be withheld from a render claim, and the child —
			// queued after it, so FIFO alone would pick the parent — handed over.
			claimed, _, err := s.Claim("w1")
			if err != nil {
				t.Fatalf("claim: %v", err)
			}
			if claimed == nil || claimed.ID != kid.ID {
				t.Fatalf("claim = %+v, want the child: the parent is an assembly anchor", claimed)
			}
			if got, _, _ := s.Claim("w2"); got != nil {
				t.Fatalf("the anchor must not be claimable for rendering, got %+v", got)
			}
		})
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
