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
	// GPULanes bounds how many Chronon render sessions the worker may run
	// concurrently (each lane renders one queue job at a time). The Chronon
	// daemon IPC must accept concurrent RENDER_JOB sessions for lanes > 1 to
	// add throughput; the default matches the NVENC multi-session baseline.
	GPULanes int `yaml:"gpu_lanes"`
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
	// EncodePreset is the explicit FFmpeg NVENC preset passed to Chronon for
	// native GPU jobs (e.g. "p2" for the throughput tier). Empty preserves the
	// engine default; the worker never invents a preset when none is set.
	EncodePreset        string `yaml:"encode_preset"`
	StrictNativeBackend bool   `yaml:"strict_native_backend"`
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

// ArtifactDBConfig configures the optional worker-local artifact mirror.
// When Path is set, every rendered job writes one ArtifactRecord to a local
// SQLite mirror after the object store accepts the bytes; empty disables the
// mirror (the default). Central queue PostgreSQL is authoritative for the
// artifact, hash, probe facts and metrics.
type ArtifactDBConfig struct {
	Path string `yaml:"path"` // SQLite mirror file; empty = mirror disabled
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
		if c.Chronon.Backend == "" {
			c.Chronon.Backend = "vulkan"
		}
		c.Chronon.NativeOutputProfiles = true
		c.Chronon.Report = true
		if c.Chronon.HardwareEncoder == "" {
			c.Chronon.HardwareEncoder = "nvenc"
		}
		c.Chronon.StrictNativeBackend = true
	case "software-cli":
		if c.Chronon.Backend == "" {
			c.Chronon.Backend = "software"
		}
		if c.Chronon.Mode == "" {
			c.Chronon.Mode = "cli"
		}
	}
	if c.Worker.ID == "" {
		c.Worker.ID = "renderinggen-" + hostname()
	}
	// Multiple pipeline workers overlap CPU/I/O preparation and post-processing.
	if c.Worker.PipelineWorkers <= 0 {
		c.Worker.PipelineWorkers = 3
	}
	// Two GPU lanes match the NVENC multi-session baseline on RTX A4000-class
	// hosts; the pipeline pools keep CPU work behind both lanes.
	if c.Worker.GPULanes <= 0 {
		c.Worker.GPULanes = 2
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
	if c.Worker.GPULanes < 1 || c.Worker.GPULanes > 8 {
		return fmt.Errorf("worker.gpu_lanes must be between 1 and 8")
	}
	if c.Chronon.Profile != "" && c.Chronon.Profile != "gpu-vulkan-native" && c.Chronon.Profile != "software-cli" {
		return fmt.Errorf("chronon.profile must be gpu-vulkan-native or software-cli")
	}
	if c.Chronon.Profile == "gpu-vulkan-native" {
		if c.Chronon.Backend != "vulkan" {
			return fmt.Errorf("gpu-vulkan-native requires chronon backend=vulkan")
		}
		if c.Chronon.HardwareEncoder != "nvenc" {
			return fmt.Errorf("gpu-vulkan-native requires chronon.hardware_encoder=nvenc")
		}
	}
	if c.Chronon.Mode == "ipc" && c.Chronon.SocketPath == "" {
		return fmt.Errorf("chronon mode=ipc requires chronon.socket_path")
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
