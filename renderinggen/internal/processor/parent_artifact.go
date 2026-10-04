package processor

import (
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
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
