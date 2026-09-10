// submit.go owns the I/O half of batch submission: it walks the derived job
// list and submits each job through the queue client, classifying every 409
// (job already exists) as an idempotent success so a replayed manifest is a
// no-op instead of a double render.
package batch

import (
	"context"
	"errors"
	"fmt"
	"log"

	queue "github.com/Marcuss-ops/RenderginGen/queue/client"
)

// Submitter submits expanded jobs to the central queue. Submit expects the
// queue client's idempotent-submit semantics: a 409 maps to
// queue.ErrJobExists and is resolved against the already-queued job.
type Submitter interface {
	Submit(ctx context.Context, job queue.Job) error
	Get(ctx context.Context, id string) (queue.Job, error)
}

// ClientSubmitter adapts the real queue client to Submitter.
type ClientSubmitter struct {
	Client *queue.Client
}

// Submit submits one job through the queue.
func (s ClientSubmitter) Submit(ctx context.Context, job queue.Job) error {
	return s.Client.Submit(ctx, job)
}

// Get fetches one job's state through the queue.
func (s ClientSubmitter) Get(ctx context.Context, id string) (queue.Job, error) {
	return s.Client.Get(ctx, id)
}

// SubmitAll submits every job in order and stops on the first NON-idempotent
// error (network failure, queue 5xx, validation reject). A 409 is counted as
// Existing: the queue's unique idempotency-key index guarantees the existing
// job carries the same content, so the replay is safe to skip.
//
// The returned Result distinguishes Submitted (this call enqueued them) from
// Existing (already queued before this call) so operators can tell a fresh
// batch from a replay at a glance.
func SubmitAll(ctx context.Context, s Submitter, jobs []queue.Job) (Result, error) {
	res := Result{}
	for _, job := range jobs {
		err := s.Submit(ctx, job)
		switch {
		case err == nil:
			res.Submitted = append(res.Submitted, job.ID)
		case errors.Is(err, queue.ErrJobExists):
			// Idempotent replay: verify the existing job is actually
			// reachable (a 409 for a job we cannot read back is a queue
			// inconsistency worth surfacing, not silently ignoring).
			if _, getErr := s.Get(ctx, job.ID); getErr != nil {
				return res, fmt.Errorf("batch: job %s reported existing but cannot be read back: %w", job.ID, getErr)
			}
			res.Existing = append(res.Existing, job.ID)
			log.Printf("batch: job %s already queued (idempotent replay, skipped)", job.ID)
		default:
			return res, fmt.Errorf("batch: submit job %s: %w", job.ID, err)
		}
	}
	return res, nil
}
