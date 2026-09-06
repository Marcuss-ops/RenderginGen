package processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/storage"
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

// Finalize attempts one parent finalization. It returns finalized=false when
// children are incomplete or another worker owns finalization.
func (f *ParentFinalizer) Finalize(ctx context.Context, parentID string, expected int64, start, end int64) (bool, queue.Artifact, error) {
	children, err := f.queue.Children(ctx, parentID)
	if err != nil {
		return false, queue.Artifact{}, err
	}
	if err := queue.ValidateChildren(children, expected, start, end); err != nil {
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
	if err := queue.ValidateChildren(children, expected, start, end); err != nil {
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
		path, _, err := f.store.LocalPath(ctx, artifact.StorageKey)
		if err != nil {
			return false, queue.Artifact{}, err
		}
		published, err := f.publisher.Publish(ctx, drive.PublishRequest{Name: parentID + ".mp4", ContentType: "video/mp4", Path: path, Subfolder: artifact.ArtifactHash})
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

func hashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
	hash, err := hashReader(file)
	if err != nil {
		return queue.Artifact{}, err
	}
	return queue.Artifact{Kind: "parent", StorageKey: hash, ArtifactHash: hash, ContentType: "video/mp4", SizeBytes: info.Size()}, nil
}
