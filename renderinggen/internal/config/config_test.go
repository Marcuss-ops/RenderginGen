package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFullConfig(t *testing.T) {
	path := writeConfig(t, `
worker:
  id: renderinggen-77
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
  local_cache_dir: /tmp/cache
chronon:
  backend: software
  home: /opt/chronon3d
  mode: ipc
  socket_path: /tmp/chronon.sock
gpu:
  device: 0
health:
  addr: ":9090"
workspace:
  root: /tmp/jobs
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Worker.ID != "renderinggen-77" {
		t.Fatalf("worker id = %q", cfg.Worker.ID)
	}
	if cfg.Queue.Endpoint != "http://queue:8081" {
		t.Fatalf("queue endpoint = %q", cfg.Queue.Endpoint)
	}
	if cfg.ArtifactStore.Endpoint != "http://store:9000" {
		t.Fatalf("store endpoint = %q", cfg.ArtifactStore.Endpoint)
	}
	if cfg.Chronon.Backend != "software" {
		t.Fatalf("chronon backend = %q", cfg.Chronon.Backend)
	}
	if cfg.Chronon.Home != "/opt/chronon3d" {
		t.Fatalf("chronon home = %q", cfg.Chronon.Home)
	}
	if cfg.Chronon.Mode != "ipc" {
		t.Fatalf("chronon mode = %q", cfg.Chronon.Mode)
	}
	if cfg.Chronon.SocketPath != "/tmp/chronon.sock" {
		t.Fatalf("chronon socket path = %q", cfg.Chronon.SocketPath)
	}
	if cfg.Health.Addr != ":9090" {
		t.Fatalf("health addr = %q", cfg.Health.Addr)
	}
	if cfg.GPU.Device != 0 {
		t.Fatalf("gpu device = %d", cfg.GPU.Device)
	}
	if cfg.Workspace.Root != "/tmp/jobs" {
		t.Fatalf("workspace root = %q", cfg.Workspace.Root)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Worker.ID == "" {
		t.Fatal("worker id should default to a hostname-based value")
	}
	if cfg.Chronon.Backend != "software" {
		t.Fatalf("chronon backend default = %q", cfg.Chronon.Backend)
	}
	if cfg.Chronon.Home != "/opt/chronon3d" {
		t.Fatalf("chronon home default = %q", cfg.Chronon.Home)
	}
	if cfg.Chronon.Mode != "cli" {
		t.Fatalf("chronon mode default = %q", cfg.Chronon.Mode)
	}
	if cfg.Chronon.SocketPath != "/var/run/chronon3d/chronon.sock" {
		t.Fatalf("chronon socket path default = %q", cfg.Chronon.SocketPath)
	}
	if cfg.ArtifactStore.LocalCacheDir != "/var/lib/renderinggen/cache" {
		t.Fatalf("cache dir default = %q", cfg.ArtifactStore.LocalCacheDir)
	}
	if cfg.Health.Addr != ":8080" {
		t.Fatalf("health addr default = %q", cfg.Health.Addr)
	}
	if cfg.Workspace.Root != defaultWorkspaceRoot() {
		t.Fatalf("workspace root default = %q, want %q", cfg.Workspace.Root, defaultWorkspaceRoot())
	}
}

func TestLoadGPUVulkanNativeProfile(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
chronon:
  profile: gpu-vulkan-native
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Chronon.Backend != "vulkan" || cfg.Chronon.Mode != "ipc" ||
		!cfg.Chronon.NativeOutputProfiles || !cfg.Chronon.Report {
		t.Fatalf("profile not applied: %+v", cfg.Chronon)
	}
}

func TestLoadArtifactDBConfig(t *testing.T) {
	path := writeConfig(t, `
worker:
  id: renderinggen-77
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
artifact_db:
  path: /var/lib/renderinggen/artifacts.db
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ArtifactDB.Path != "/var/lib/renderinggen/artifacts.db" {
		t.Fatalf("artifact_db.path = %q", cfg.ArtifactDB.Path)
	}
}

func TestLoadArtifactDBDisabledByDefault(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ArtifactDB.Path != "" {
		t.Fatalf("artifact_db.path default = %q, want empty (ledger disabled)", cfg.ArtifactDB.Path)
	}
}

func TestLoadMissingQueueEndpoint(t *testing.T) {
	path := writeConfig(t, `
artifact_store:
  endpoint: http://store:9000
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for missing queue.endpoint")
	}
}

func TestLoadMissingStoreEndpoint(t *testing.T) {
	path := writeConfig(t, `
queue:
  endpoint: http://queue:8081
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for missing artifact_store.endpoint")
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeConfig(t, "[1, 2, 3")
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}
