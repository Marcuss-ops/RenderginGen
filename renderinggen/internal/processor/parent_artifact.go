package processor

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/storage"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
	"os"
)

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

func parentArtifactFromIdentity(hash string, size int64) queue.Artifact {
	return queue.Artifact{Kind: "parent", StorageKey: hash, ArtifactHash: hash, ContentType: "video/mp4", SizeBytes: size}
}

// persistParentArtifact publishes the assembled parent in one pass when the
// store supports streaming staging, and falls back to the classic hash-then-PUT
// path when it does not. The staged path is an optimization on top of the same
// content-addressing contract: the store verifies the address, and the returned
// size is checked against the stat the upload started from.
func persistParentArtifact(ctx context.Context, store *storage.Client, jobID, path string) (queue.Artifact, error) {
	if store != nil {
		hash, size, err := store.PutStagedFile(ctx, stagingSessionID("parent:"+jobID), path)
		if err == nil {
			return parentArtifactFromIdentity(hash, size), nil
		}
		if !errors.Is(err, storage.ErrStagingUnsupported) {
			workerlog.ByJobID(jobID).Warnf("parent staged upload failed; falling back to hash-then-PUT: %v", err)
		}
	}
	artifact, err := artifactFromFile(path)
	if err != nil {
		return queue.Artifact{}, err
	}
	if store != nil {
		if err := store.PutFile(ctx, artifact.ArtifactHash, path); err != nil {
			return queue.Artifact{}, err
		}
	}
	return artifact, nil
}
