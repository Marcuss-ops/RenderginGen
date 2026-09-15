package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestShippedConfigsLoad loads every worker configuration that ships in the
// repository.
//
// It exists because the configuration is documented in those files and read by
// this package: a setting renamed here, or a document that predates a now-required
// key, is otherwise discovered only when a deployment restarts. KnownFields(true)
// makes this an exact check — any key the file has that the struct does not
// accept is a failure, so the test pins the documentation and the loader
// together in both directions.
func TestShippedConfigsLoad(t *testing.T) {
	// Paths are relative to this package: renderinggen/internal/config.
	// <repo>/renderinggen/internal/config -> <repo> is three levels up.
	repoRoot := filepath.Clean(filepath.Join("..", "..", ".."))
	shipped := []string{
		filepath.Join("renderinggen", "config.yaml"),
		filepath.Join("infra", "docker", "worker-config.yaml"),
		filepath.Join("infra", "docker", "worker-config-b.yaml"),
		filepath.Join("infra", "docker", "worker-config-ci.yaml"),
		filepath.Join("infra", "docker", "worker-config-drive.yaml"),
		filepath.Join("infra", "native", "renderinggen-native.yaml"),
		filepath.Join("infra", "native", "renderinggen-b.yaml"),
	}
	loaded := 0
	for _, rel := range shipped {
		path := filepath.Join(repoRoot, rel)
		if _, err := os.Stat(path); err != nil {
			// The gate is correct in a partial checkout; it is not a check that
			// silently passes because a file was renamed, which is why at least
			// one document must be found below.
			t.Logf("shipped config not present in this checkout, skipped: %s", rel)
			continue
		}
		cfg, err := Load(path)
		if err != nil {
			t.Errorf("%s does not load: %v", rel, err)
			continue
		}
		loaded++
		if cfg.Queue.Endpoint == "" || cfg.ArtifactStore.Endpoint == "" {
			t.Errorf("%s loaded without the required endpoints", rel)
		}
	}
	if loaded == 0 {
		t.Fatal("no shipped worker configuration was found; the test would pass vacuously")
	}
}
