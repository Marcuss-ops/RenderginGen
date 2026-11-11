package processor

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

type generationQueue struct {
	generations []int64
}

func (q *generationQueue) Complete(_ context.Context, _ string, generation int64, _ queue.Artifact) error {
	q.generations = append(q.generations, generation)
	return nil
}

func (q *generationQueue) Rendered(_ context.Context, _ string, generation int64, _ string, _ queue.Artifact) error {
	q.generations = append(q.generations, generation)
	return nil
}

func (q *generationQueue) Fail(_ context.Context, _ string, generation int64, _ string) error {
	q.generations = append(q.generations, generation)
	return nil
}

func (q *generationQueue) FailPermanently(_ context.Context, _ string, generation int64, _ string) error {
	q.generations = append(q.generations, generation)
	return nil
}

func TestTerminalReportsCarryLeaseGeneration(t *testing.T) {
	q := &generationQueue{}
	job := &queue.Job{ID: "job-1", LeaseGeneration: 7}

	ReportFailure(context.Background(), q, job, errors.New("render failed"))
	ReportFailure(context.Background(), q, &queue.Job{
		ID:              job.ID,
		LeaseGeneration: 7,
		Artifact:        &queue.Artifact{StorageKey: "sha-abc"},
	}, errors.New("publication failed"))
	ReportFailure(context.Background(), q, job, ErrDrivePublicationPermanent)
	ReportComplete(context.Background(), q, job.ID, job.LeaseGeneration, queue.Artifact{}, false)

	if len(q.generations) != 4 {
		t.Fatalf("reports = %d, want 4", len(q.generations))
	}
	for i, generation := range q.generations {
		if generation != 7 {
			t.Fatalf("report %d generation = %d, want 7", i, generation)
		}
	}
}
