package processor

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
)

// fakeReportQueue records the terminal reports a worker issues and can be
// programmed to fail specific ones, mirroring what the real queue server
// would do (409 on a lost lease, 5xx on a transient blip).
type fakeReportQueue struct {
	completed []queue.Artifact
	rendered  []queue.Artifact
	failed    []string

	completeErr error
	renderedErr error
	failErr     error
}

func (f *fakeReportQueue) Complete(_ context.Context, _ string, artifact queue.Artifact) error {
	if f.completeErr != nil {
		return f.completeErr
	}
	f.completed = append(f.completed, artifact)
	return nil
}

func (f *fakeReportQueue) Rendered(_ context.Context, _ string, _ string, artifact queue.Artifact) error {
	if f.renderedErr != nil {
		return f.renderedErr
	}
	f.rendered = append(f.rendered, artifact)
	return nil
}

func (f *fakeReportQueue) Fail(_ context.Context, _ string, reason string) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.failed = append(f.failed, reason)
	return nil
}

func durableArtifact(key string) queue.Artifact {
	return queue.Artifact{
		StorageKey:   key,
		ArtifactHash: key,
		ContentType:  "video/mp4",
		SizeBytes:    1024,
	}
}

// TestReportCompleteDurableFallback pins finding 6.3: when the render finished
// and the artifact is durable (bytes in L3), a failed Complete report must
// transition the job to Rendered with the artifact attached — never leave it
// to expire back to pending and be re-rendered on the next claim.
func TestReportCompleteDurableFallback(t *testing.T) {
	q := &fakeReportQueue{completeErr: context.DeadlineExceeded}
	artifact := durableArtifact("sha-abc")

	ReportComplete(context.Background(), q, "job-1", artifact, true)

	if len(q.rendered) != 1 {
		t.Fatalf("durable complete failure: want 1 rendered report, got %d", len(q.rendered))
	}
	if q.rendered[0].StorageKey != "sha-abc" {
		t.Fatalf("rendered artifact = %+v, want storage_key sha-abc", q.rendered[0])
	}
	if len(q.completed) != 0 {
		t.Fatalf("complete must not be recorded when it failed; got %d", len(q.completed))
	}
}

// TestReportCompleteNoFallbackWithoutDurability: a failed Complete on a job
// whose artifact is NOT durable (or durable=false, the prepare warm-up path)
// must not fabricate a Rendered transition — such a job genuinely needs a
// re-render, so it is left for the queue's normal expiry/requeue.
func TestReportCompleteNoFallbackWithoutDurability(t *testing.T) {
	for name, tc := range map[string]struct {
		artifact queue.Artifact
		durable  bool
	}{
		"durable=false with storage key":   {artifact: durableArtifact("sha-abc"), durable: false},
		"durable=true without storage key": {artifact: queue.Artifact{SizeBytes: 1}, durable: true},
	} {
		t.Run(name, func(t *testing.T) {
			q := &fakeReportQueue{completeErr: context.DeadlineExceeded}
			ReportComplete(context.Background(), q, "job-1", tc.artifact, tc.durable)
			if len(q.rendered) != 0 {
				t.Fatalf("want no rendered fallback, got %d", len(q.rendered))
			}
		})
	}
}

// TestReportCompleteSuccess: a successful Complete needs no fallback.
func TestReportCompleteSuccess(t *testing.T) {
	q := &fakeReportQueue{}
	artifact := durableArtifact("sha-abc")

	ReportComplete(context.Background(), q, "job-1", artifact, true)

	if len(q.completed) != 1 || q.completed[0].StorageKey != "sha-abc" {
		t.Fatalf("completed = %+v, want the artifact", q.completed)
	}
	if len(q.rendered) != 0 {
		t.Fatalf("successful complete must not fall back to rendered; got %d", len(q.rendered))
	}
}

// TestReportFailureWithDurableArtifact: a re-claimed rendered job (artifact
// attached to the claim) that fails its publication retry must stay in the
// rendered state — Fail would drop the artifact and force a re-render.
func TestReportFailureWithDurableArtifact(t *testing.T) {
	job := &queue.Job{ID: "job-1"}
	artifact := durableArtifact("sha-abc")
	job.Artifact = &artifact

	q := &fakeReportQueue{}
	ReportFailure(context.Background(), q, job, context.DeadlineExceeded)

	if len(q.rendered) != 1 || q.rendered[0].StorageKey != "sha-abc" {
		t.Fatalf("rendered = %+v, want the durable artifact", q.rendered)
	}
	if len(q.failed) != 0 {
		t.Fatalf("durable failure must not call Fail; got %v", q.failed)
	}
}

// TestReportFailureWithoutArtifact: a failure on a job that never rendered
// (no artifact) is a plain render failure.
func TestReportFailureWithoutArtifact(t *testing.T) {
	q := &fakeReportQueue{}
	ReportFailure(context.Background(), q, &queue.Job{ID: "job-1"}, context.DeadlineExceeded)

	if len(q.failed) != 1 {
		t.Fatalf("want 1 fail report, got %d", len(q.failed))
	}
	if len(q.rendered) != 0 {
		t.Fatalf("non-durable failure must not report rendered; got %d", len(q.rendered))
	}
}

// TestReportFailureWithArtifactSeparate covers the post-pool shape, where the
// durable artifact was produced by FinalizeJob but never attached to the
// claimed job: a late publication failure on durable bytes -> Rendered, and a
// pre-durable failure -> Fail.
func TestReportFailureWithArtifactSeparate(t *testing.T) {
	t.Run("durable -> rendered", func(t *testing.T) {
		q := &fakeReportQueue{}
		ReportFailureWithArtifact(context.Background(), q, "job-1", durableArtifact("sha-abc"), context.DeadlineExceeded)
		if len(q.rendered) != 1 || len(q.failed) != 0 {
			t.Fatalf("rendered=%d failed=%d, want rendered=1 failed=0", len(q.rendered), len(q.failed))
		}
	})
	t.Run("not durable -> fail", func(t *testing.T) {
		q := &fakeReportQueue{}
		ReportFailureWithArtifact(context.Background(), q, "job-1", queue.Artifact{}, context.DeadlineExceeded)
		if len(q.failed) != 1 || len(q.rendered) != 0 {
			t.Fatalf("rendered=%d failed=%d, want rendered=0 failed=1", len(q.rendered), len(q.failed))
		}
	})
}
