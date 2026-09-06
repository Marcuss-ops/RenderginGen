package processor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
)

type finalizerQueue struct {
	children  []*queue.Job
	claimed   bool
	completed *queue.Artifact
}

func (q *finalizerQueue) Children(context.Context, string) ([]*queue.Job, error) {
	return q.children, nil
}
func (q *finalizerQueue) ClaimFinalization(context.Context, string) (*queue.Job, bool, error) {
	if q.claimed {
		return nil, false, nil
	}
	q.claimed = true
	return &queue.Job{ID: "parent"}, true, nil
}
func (q *finalizerQueue) Complete(_ context.Context, _ string, artifact queue.Artifact) error {
	q.completed = &artifact
	return nil
}

type finalizerAssembler struct{}

func (finalizerAssembler) Assemble(_ context.Context, req chronon.AssembleRequest) error {
	out, err := os.Create(req.Output)
	if err != nil {
		return err
	}
	defer out.Close()
	for _, input := range req.Inputs {
		in, err := os.Open(input)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			return err
		}
		in.Close()
	}
	return nil
}

func TestParentFinalizerStoresPublishesAndCompletes(t *testing.T) {
	ctx := context.Background()
	store := storage.New(storage.NewMemory(), storage.Options{L2Dir: t.TempDir()})
	if err := store.Put(ctx, "chunk-a", []byte("A")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "chunk-b", []byte("B")); err != nil {
		t.Fatal(err)
	}
	children := []*queue.Job{
		{ID: "c0", ChunkIndex: 0, FrameRange: &queue.FrameRange{Start: 0, End: 10}, State: queue.StateCompleted, Artifact: &queue.Artifact{StorageKey: "chunk-a"}},
		{ID: "c1", ChunkIndex: 1, FrameRange: &queue.FrameRange{Start: 10, End: 20}, State: queue.StateCompleted, Artifact: &queue.Artifact{StorageKey: "chunk-b"}},
	}
	q := &finalizerQueue{children: children}
	driveDir := t.TempDir()
	f := NewParentFinalizer(q, store, finalizerAssembler{}, drive.NewMock(driveDir, 0), "worker", t.TempDir())
	finalized, artifact, err := f.Finalize(ctx, "parent", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !finalized || q.completed == nil {
		t.Fatalf("finalized=%t completed=%+v", finalized, q.completed)
	}
	if artifact.StorageKey == "" || artifact.DriveFileID == "" || artifact.SizeBytes != 2 {
		t.Fatalf("artifact=%+v", artifact)
	}
	if _, err := os.Stat(filepath.Join(driveDir, artifact.ArtifactHash)); err != nil {
		t.Fatalf("published output missing: %v", err)
	}
}
