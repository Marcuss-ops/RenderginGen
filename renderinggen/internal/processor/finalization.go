package processor

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
)

// ParentFinalizer is the minimal orchestration boundary for assembling a
// completed chunk parent. Queue ownership is acquired atomically before any
// media work begins.
type ParentQueue interface {
	ClaimFinalization(context.Context, string) (*queue.Job, bool, error)
	Children(context.Context, string) ([]*queue.Job, error)
	Complete(context.Context, string, queue.Artifact) error
}

type ParentFinalizer struct {
	queue     ParentQueue
	store     *storage.Client
	assembler chronon.Assembler
	publisher drive.Publisher
	workerID  string
	outputDir string
}

func NewParentFinalizer(q ParentQueue, store *storage.Client, assembler chronon.Assembler, publisher drive.Publisher, workerID, outputDir string) *ParentFinalizer {
	return &ParentFinalizer{queue: q, store: store, assembler: assembler, publisher: publisher, workerID: workerID, outputDir: outputDir}
}

// Finalize attempts one parent finalization for a caller that already knows the
// frame range the child family must cover. It returns finalized=false when
// children are incomplete or another worker owns finalization.
//
// The validation happens twice — once against the pre-claim read, once against
// a fresh read after the atomic claim — so a child family that changes between
// the optimistic check and the ownership hand-off is re-checked before any
// assembly work. ValidateChildren needs no expected-count parameter: dense
// chunk indices plus exact half-open frame coverage of [start, end) are the
// verifiable completeness contract (see queue.ValidateChildren).
func (f *ParentFinalizer) Finalize(ctx context.Context, parentID string, start, end int64) (bool, queue.Artifact, error) {
	children, err := f.queue.Children(ctx, parentID)
	if err != nil {
		return false, queue.Artifact{}, err
	}
	return f.finalize(ctx, parentID, start, end, children)
}

// FinalizeFromChildren is the worker's completion-path entry point. The
// expected frame range is a property of the child family, so learning it meant
// reading the family and then handing the finalizer bounds it immediately
// re-read anyway — one duplicate Children() round-trip per completed chunk on
// the post-processing path. Deriving the range here, from the same read that is
// validated, removes that round-trip without changing the contract: the range
// the pre-claim read validates against is still the range the observed family
// spans.
func (f *ParentFinalizer) FinalizeFromChildren(ctx context.Context, parentID string) (bool, queue.Artifact, error) {
	children, err := f.queue.Children(ctx, parentID)
	if err != nil {
		return false, queue.Artifact{}, err
	}
	start, end, ok := childFrameRange(children)
	if !ok {
		return false, queue.Artifact{}, fmt.Errorf("parent finalizer: child family of %s has no frame range to assemble", parentID)
	}
	return f.finalize(ctx, parentID, start, end, children)
}

// childFrameRange returns the half-open [start, end) frame range a child family
// spans, or false when the family is empty or a boundary child carries no frame
// range. It deliberately takes the family as an argument: the range is only
// meaningful relative to the read it came from.
func childFrameRange(children []*queue.Job) (int64, int64, bool) {
	if len(children) == 0 {
		return 0, 0, false
	}
	first, last := children[0], children[len(children)-1]
	if first == nil || last == nil || first.FrameRange == nil || last.FrameRange == nil {
		return 0, 0, false
	}
	return first.FrameRange.Start, last.FrameRange.End, true
}

// finalize runs the finalization protocol against an already-read pre-claim
// child family, so the caller never pays for the same read twice.
func (f *ParentFinalizer) finalize(ctx context.Context, parentID string, start, end int64, children []*queue.Job) (bool, queue.Artifact, error) {
	if err := queue.ValidateChildren(children, start, end); err != nil {
		return false, queue.Artifact{}, err
	}
	_, claimed, err := f.queue.ClaimFinalization(ctx, parentID)
	if err != nil || !claimed {
		return false, queue.Artifact{}, err
	}
	children, err = f.queue.Children(ctx, parentID)
	if err != nil {
		return false, queue.Artifact{}, err
	}
	if err := queue.ValidateChildren(children, start, end); err != nil {
		return false, queue.Artifact{}, err
	}
	if f.assembler == nil {
		return false, queue.Artifact{}, fmt.Errorf("parent finalizer: assembler is required")
	}
	if f.outputDir == "" {
		return false, queue.Artifact{}, fmt.Errorf("parent finalizer: output directory is required")
	}
	if err := os.MkdirAll(f.outputDir, 0o755); err != nil {
		return false, queue.Artifact{}, err
	}
	inputs := make([]string, 0, len(children))
	for _, child := range children {
		path, _, err := f.store.LocalPath(ctx, child.Artifact.StorageKey)
		if err != nil {
			return false, queue.Artifact{}, fmt.Errorf("chunk %d local path: %w", child.ChunkIndex, err)
		}
		inputs = append(inputs, path)
	}
	output := filepath.Join(f.outputDir, parentID+".mp4")
	// The assembled parent is a temporary staging file: it is uploaded to L3
	// (and optionally Drive) below and must never accumulate on the worker.
	// The workspace root is frequently /dev/shm — i.e. RAM — so leaving the
	// assembled parent behind is a memory leak, not just a disk one. A failed
	// removal is logged: it is a leak that must be visible, never silent.
	defer func() {
		if err := os.Remove(output); err != nil && !os.IsNotExist(err) {
			log.Printf("parent %s: remove staging output %s: %v", parentID, output, err)
		}
	}()
	if err := f.assembler.Assemble(ctx, chronon.AssembleRequest{Inputs: inputs, Output: output}); err != nil {
		return false, queue.Artifact{}, err
	}
	artifact, err := artifactFromFile(output)
	if err != nil {
		return false, queue.Artifact{}, err
	}
	if f.store != nil {
		if err := f.store.PutFile(ctx, artifact.ArtifactHash, output); err != nil {
			return false, queue.Artifact{}, err
		}
	}
	if f.publisher != nil {
		// The parent is published through the SAME verified path as a segment
		// artifact (publishAndVerify): resolving the content-addressed L2 file
		// verifies store_sha == db_sha and the provider must report the same
		// size. The historical direct drive.Publisher.Publish call here was the
		// only publication in the worker without that invariant and without a
		// resolvable policy (audit P0-3).
		published, err := publishAndVerify(ctx, f.store, f.publisher, parentID+".mp4", artifact)
		if err != nil {
			return false, queue.Artifact{}, err
		}
		artifact.DriveFileID, artifact.DriveLink = published.FileID, published.WebViewLink
	}
	if err := f.queue.Complete(ctx, parentID, artifact); err != nil {
		return false, queue.Artifact{}, err
	}
	return true, artifact, nil
}

func artifactFromFile(path string) (queue.Artifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return queue.Artifact{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return queue.Artifact{}, err
	}
	hash, _, err := hashio.Reader(file)
	if err != nil {
		return queue.Artifact{}, err
	}
	return queue.Artifact{Kind: "parent", StorageKey: hash, ArtifactHash: hash, ContentType: "video/mp4", SizeBytes: info.Size()}, nil
}
