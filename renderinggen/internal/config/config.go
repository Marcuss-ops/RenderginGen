// Package config loads and validates the RenderingGen worker configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the top-level worker configuration.
type Config struct {
	Worker        WorkerConfig     `yaml:"worker"`
	Queue         QueueConfig      `yaml:"queue"`
	ArtifactStore StorageConfig    `yaml:"artifact_store"`
	Chronon       ChrononConfig    `yaml:"chronon"`
	GPU           GPUConfig        `yaml:"gpu"`
	Health        HealthConfig     `yaml:"health"`
	Workspace     WorkspaceConfig  `yaml:"workspace"`
	Drive         DriveConfig      `yaml:"drive"`
	ArtifactDB    ArtifactDBConfig `yaml:"artifact_db"`
}

type WorkerConfig struct {
	ID              string `yaml:"id"`
	PipelineWorkers int    `yaml:"pipeline_workers"`
}

type QueueConfig struct {
	Endpoint string `yaml:"endpoint"`
}

type StorageConfig struct {
	Endpoint      string `yaml:"endpoint"`
	LocalCacheDir string `yaml:"local_cache_dir"`
}

type WorkspaceConfig struct {
	Root string `yaml:"root"`
}

type ChrononConfig struct {
	Profile              string `yaml:"profile"` // software-cli | gpu-vulkan-native
	Backend              string `yaml:"backend"`
	Home                 string `yaml:"home"`
	Mode                 string `yaml:"mode"`        // "cli" (default) | "ipc"
	SocketPath           string `yaml:"socket_path"` // unix socket when Mode == "ipc"
	NativeOutputProfiles bool   `yaml:"native_output_profiles"`
	Report               bool   `yaml:"report"`
	HardwareEncoder      string `yaml:"hardware_encoder"`
}

type GPUConfig struct {
	Device int `yaml:"device"`
}

type HealthConfig struct {
	Addr string `yaml:"addr"`
}

// DriveConfig configures Google Drive publication of rendered artifacts.
// Publication is decoupled from rendering so a failed upload never forces a
// GPU re-render; see the "rendered" job state.
type DriveConfig struct {
	Enabled bool `yaml:"enabled"`

	// Mode selects the publisher: "google" (default) uses a service-account
	// key, "oauth" uses a Google OAuth2 client + user token (the PipelineGen
	// shape), and "mock" uses an in-process publisher for tests/smoke.
	Mode string `yaml:"mode"`

	// Google service-account credentials and the Drive folder to upload into.
	CredentialsFile string `yaml:"credentials_file"`
	ParentFolderID  string `yaml:"parent_folder_id"`

	// OAuth2 user-account publication (Mode == "oauth").
	TokenFile string `yaml:"token_file"`

	// Mock publisher settings (Mode == "mock").
	MockDir       string `yaml:"mock_dir"`        // write uploaded bytes here
	MockFailFirst int    `yaml:"mock_fail_first"` // fail the first N uploads
}

// ArtifactDBConfig configures the artifact ledger (the "DB artifact" step).
// When Path is set, every rendered job writes one ArtifactRecord to a local
// SQLite ledger after the object store accepts the bytes; empty disables the
// ledger (the default). The ledger is the source of truth for what the
// pipeline produced: hash, probe facts, semantic counters and per-phase
// metrics.
type ArtifactDBConfig struct {
	Path string `yaml:"path"` // SQLite ledger file; empty = ledger disabled
}

// Load reads the YAML config file at path, applies defaults and validates it.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(&cfg)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(c *Config) {
	switch c.Chronon.Profile {
	case "gpu-vulkan-native":
		c.Chronon.Backend = "vulkan"
		c.Chronon.Mode = "ipc"
		c.Chronon.NativeOutputProfiles = true
		c.Chronon.Report = true
		if c.Chronon.HardwareEncoder == "" {
			c.Chronon.HardwareEncoder = "nvenc"
		}
	case "software-cli":
		c.Chronon.Backend = "software"
		c.Chronon.Mode = "cli"
	}
	if c.Worker.ID == "" {
		c.Worker.ID = "renderinggen-" + hostname()
	}
	// Multiple pipeline workers overlap CPU/I/O preparation and post-processing.
	// Chronon itself remains serialized by Processor's single GPU lane.
	if c.Worker.PipelineWorkers <= 0 {
		c.Worker.PipelineWorkers = 3
	}
	if c.Chronon.Backend == "" {
		c.Chronon.Backend = "software"
	}
	if c.Chronon.Home == "" {
		c.Chronon.Home = "/opt/chronon3d"
	}
	if c.Chronon.Mode == "" {
		c.Chronon.Mode = "cli"
	}
	if c.Chronon.SocketPath == "" {
		c.Chronon.SocketPath = "/var/run/chronon3d/chronon.sock"
	}
	if c.ArtifactStore.LocalCacheDir == "" {
		c.ArtifactStore.LocalCacheDir = "/var/lib/renderinggen/cache"
	}
	if c.Workspace.Root == "" {
		c.Workspace.Root = defaultWorkspaceRoot()
	}
	if c.Health.Addr == "" {
		c.Health.Addr = ":8080"
	}
	if c.Drive.Mode == "" {
		c.Drive.Mode = "google"
	}
}

func (c *Config) validate() error {
	if c.Worker.PipelineWorkers < 1 || c.Worker.PipelineWorkers > 16 {
		return fmt.Errorf("worker.pipeline_workers must be between 1 and 16")
	}
	if c.Chronon.Profile != "" && c.Chronon.Profile != "gpu-vulkan-native" && c.Chronon.Profile != "software-cli" {
		return fmt.Errorf("chronon.profile must be gpu-vulkan-native or software-cli")
	}
	if c.Chronon.Profile == "gpu-vulkan-native" {
		if c.Chronon.Backend != "vulkan" || c.Chronon.Mode != "ipc" {
			return fmt.Errorf("gpu-vulkan-native requires chronon backend=vulkan and mode=ipc")
		}
		if c.Chronon.HardwareEncoder != "nvenc" {
			return fmt.Errorf("gpu-vulkan-native requires chronon.hardware_encoder=nvenc")
		}
		if c.Chronon.SocketPath == "" {
			return fmt.Errorf("gpu-vulkan-native requires chronon.socket_path")
		}
	}
	if c.Queue.Endpoint == "" {
		return fmt.Errorf("queue.endpoint is required")
	}
	if c.ArtifactStore.Endpoint == "" {
		return fmt.Errorf("artifact_store.endpoint is required")
	}
	if c.Drive.Enabled {
		switch c.Drive.Mode {
		case "google":
			if c.Drive.CredentialsFile == "" {
				return fmt.Errorf("drive.credentials_file is required when drive is enabled (mode=google)")
			}
		case "oauth":
			if c.Drive.CredentialsFile == "" || c.Drive.TokenFile == "" {
				return fmt.Errorf("drive.credentials_file and drive.token_file are required when drive is enabled (mode=oauth)")
			}
		case "mock":
			// no credentials needed
		default:
			return fmt.Errorf("drive.mode must be 'google', 'oauth' or 'mock', got %q", c.Drive.Mode)
		}
	}
	return nil
}

func defaultWorkspaceRoot() string {
	const shmRoot = "/dev/shm"
	if info, err := os.Stat(shmRoot); err == nil && info.IsDir() && isWritableDirectory(shmRoot) {
		return filepath.Join(shmRoot, "renderinggen", "jobs")
	}
	return "/var/lib/renderinggen/jobs"
}

func isWritableDirectory(path string) bool {
	probe, err := os.CreateTemp(path, ".renderinggen-write-test-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return false
	}
	return os.Remove(name) == nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
