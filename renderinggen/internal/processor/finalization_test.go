package processor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

type finalizerQueue struct {
	children  []*queue.Job
	claimed   bool
	completed *queue.Artifact
	// events records the protocol order — every child-family read and the
	// claim — so a test can assert how many reads the completion path costs and
	// that the claim sits between them.
	events []string
}

func (q *finalizerQueue) Children(context.Context, string) ([]*queue.Job, error) {
	q.events = append(q.events, "children")
	return q.children, nil
}
func (q *finalizerQueue) ClaimFinalization(context.Context, string) (*queue.Job, bool, error) {
	q.events = append(q.events, "claim")
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

// copySafeChunkArtifact mirrors what the production lane certifies on a
// completed chunk: the structural probe ran (a job carrying a FrameRange is
// always probed) and the copy-safety facts are proven.
func copySafeChunkArtifact(storageKey string) *queue.Artifact {
	return &queue.Artifact{StorageKey: storageKey, CopyEligible: true, ClosedGOP: true, FirstFrameKeyframe: true}
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
		{ID: "c0", ChunkIndex: 0, FrameRange: &queue.FrameRange{Start: 0, End: 10}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-a")},
		{ID: "c1", ChunkIndex: 1, FrameRange: &queue.FrameRange{Start: 10, End: 20}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-b")},
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

// TestFinalizeFromChildrenDerivesRangeAndReadsOnceBeforeTheClaim pins the
// completion-path contract the worker relies on. The worker used to read the
// child family itself just to learn first/last frame, then hand the finalizer
// bounds it immediately re-read — one duplicate Children() round-trip per
// completed chunk. FinalizeFromChildren derives the range from the same read it
// validates, so the protocol is exactly children → claim → children.
//
// A third read means the duplicate round-trip is back; a derived range that
// disagrees with the family's own span would assemble the wrong window.
func TestFinalizeFromChildrenDerivesRangeAndReadsOnceBeforeTheClaim(t *testing.T) {
	ctx := context.Background()
	store := storage.New(storage.NewMemory(), storage.Options{L2Dir: t.TempDir()})
	if err := store.Put(ctx, "chunk-a", []byte("A")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "chunk-b", []byte("B")); err != nil {
		t.Fatal(err)
	}
	children := []*queue.Job{
		{ID: "c0", ChunkIndex: 0, FrameRange: &queue.FrameRange{Start: 0, End: 10}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-a")},
		{ID: "c1", ChunkIndex: 1, FrameRange: &queue.FrameRange{Start: 10, End: 20}, State: queue.StateCompleted, Artifact: copySafeChunkArtifact("chunk-b")},
	}
	q := &finalizerQueue{children: children}
	f := NewParentFinalizer(q, store, finalizerAssembler{}, nil, "worker", t.TempDir())

	finalized, _, err := f.FinalizeFromChildren(ctx, "parent")
	if err != nil {
		t.Fatalf("finalize from children: %v", err)
	}
	if !finalized || q.completed == nil {
		t.Fatalf("finalized=%t completed=%+v", finalized, q.completed)
	}
	want := []string{"children", "claim", "children"}
	if strings.Join(q.events, ",") != strings.Join(want, ",") {
		t.Fatalf("protocol = %v, want %v (a third read is the duplicate round-trip this entry point exists to remove)", q.events, want)
	}
	// The derived range must be the family's span: assembling [0,20) is only
	// correct if start/end came from children[0].Start and children[last].End.
	if q.completed.SizeBytes != 2 {
		t.Fatalf("assembled artifact = %+v, want both chunks concatenated", q.completed)
	}
}

// TestFinalizeFromChildrenRejectsAFamilyWithoutARange pins the failure mode: a
// child family with no frame range is not finalizable, and the finalizer must
// fail BEFORE claiming the parent. A claim without a range would leave the
// parent owned by this worker with nothing to assemble.
func TestFinalizeFromChildrenRejectsAFamilyWithoutARange(t *testing.T) {
	cases := map[string][]*queue.Job{
		"empty family": nil,
		"boundary child without a frame range": {
			{ID: "c0", ChunkIndex: 0, State: queue.StateCompleted, Artifact: &queue.Artifact{StorageKey: "chunk-a"}},
		},
	}
	for name, children := range cases {
		t.Run(name, func(t *testing.T) {
			q := &finalizerQueue{children: children}
			f := NewParentFinalizer(q, nil, finalizerAssembler{}, nil, "worker", t.TempDir())
			finalized, _, err := f.FinalizeFromChildren(context.Background(), "parent")
			if err == nil || finalized {
				t.Fatalf("finalized=%t err=%v, want a refusal", finalized, err)
			}
			for _, event := range q.events {
				if event == "claim" {
					t.Fatalf("the parent must not be claimed for a family that cannot be assembled: %v", q.events)
				}
			}
		})
	}
}
