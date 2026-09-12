// Package config loads and validates the RenderingGen worker configuration.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
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

// The two supported Chronon profiles. They are named constants because the
// wiring used to re-compare the raw `profile` string in three places (main.go)
// in addition to the two switch arms here.
const (
	ProfileSoftwareCLI     = "software-cli"
	ProfileGPUVulkanNative = "gpu-vulkan-native"
)

type ChrononConfig struct {
	Profile              string `yaml:"profile"` // software-cli | gpu-vulkan-native
	Backend              string `yaml:"backend"`
	Home                 string `yaml:"home"`
	Binary               string `yaml:"binary"`      // explicit chronon3d_cli path
	Mode                 string `yaml:"mode"`        // "cli" (default) | "ipc"
	SocketPath           string `yaml:"socket_path"` // unix socket when Mode == "ipc"
	NativeOutputProfiles bool   `yaml:"native_output_profiles"`
	Report               bool   `yaml:"report"`
	HardwareEncoder      string `yaml:"hardware_encoder"`
	// EncodePreset is the explicit FFmpeg NVENC preset passed to Chronon for
	// native GPU jobs (e.g. "p2" for the throughput tier). Empty preserves the
	// engine default; the worker never invents a preset when none is set.
	EncodePreset string `yaml:"encode_preset"`
	// PipePixFmt selects the host-frame pipe format used by GPU compositions.
	// NV12 avoids an unnecessary RGBA conversion on the normal 8-bit path;
	// P010 is available for 10-bit workflows.
	PipePixFmt          string `yaml:"pipe_pixfmt"`
	StrictNativeBackend bool   `yaml:"strict_native_backend"`
}

type GPUConfig struct {
	Device int `yaml:"device"`
}

type HealthConfig struct {
	Addr string `yaml:"addr"`
}

// DriveConfig configures the worker's Google Drive publication CAPABILITY.
// enabled is a capability/configuration constraint only — it declares that
// the worker holds Drive credentials — and never decides a job's intent.
// Intent (who delivers a rendered segment to Drive) is resolved canonically
// by processor.ResolvePublicationPolicy: queue-served render segments are
// object-store-only because the submitter (PipelineGen master) publishes
// clips; the worker's Drive capability serves the parent assembler and any
// job whose submitter explicitly declared object_store_and_drive.
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

	// KnownFields(true) turns a typo like `gpu_lane:` or a renamed key into a
	// hard parse error instead of a silent fallback to the default (which can
	// silently downgrade the worker from NVENC to software encoding or change
	// the lane count). Fail-fast at load, never on the first job.
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(&cfg)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// StrictNative reports whether this configuration demands the native GPU hot
// path. It is the SINGLE derivation of that rule on the configuration side:
// applyDefaults already folds the profile into StrictNativeBackend, and the
// wiring (main.go) asks this method instead of re-comparing the profile string
// with `|| cfg.Chronon.Profile == "gpu-vulkan-native"` in three places.
func (c ChrononConfig) StrictNative() bool {
	return c.StrictNativeBackend || c.Profile == ProfileGPUVulkanNative
}

func applyDefaults(c *Config) {
	switch c.Chronon.Profile {
	case ProfileGPUVulkanNative:
		if c.Chronon.Backend == "" {
			c.Chronon.Backend = "vulkan"
		}
		c.Chronon.NativeOutputProfiles = true
		c.Chronon.Report = true
		if c.Chronon.HardwareEncoder == "" {
			c.Chronon.HardwareEncoder = chronon.DefaultHardwareEncoder
		}
		if c.Chronon.PipePixFmt == "" {
			c.Chronon.PipePixFmt = "nv12"
		}
		c.Chronon.StrictNativeBackend = true
	case ProfileSoftwareCLI:
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
	if c.Chronon.Profile != "" && c.Chronon.Profile != ProfileGPUVulkanNative && c.Chronon.Profile != ProfileSoftwareCLI {
		return fmt.Errorf("chronon.profile must be %s or %s", ProfileGPUVulkanNative, ProfileSoftwareCLI)
	}
	if c.Chronon.Profile == ProfileGPUVulkanNative {
		if c.Chronon.Backend != "vulkan" {
			return fmt.Errorf("%s requires chronon backend=vulkan", ProfileGPUVulkanNative)
		}
		if c.Chronon.HardwareEncoder != chronon.DefaultHardwareEncoder {
			return fmt.Errorf("%s requires chronon.hardware_encoder=%s", ProfileGPUVulkanNative, chronon.DefaultHardwareEncoder)
		}
	}
	// hardware_encoder reaches the CLI as --hardware on every GPU-required
	// path. An unsupported value (vaapi, qsv, ...) used to be accepted here and
	// then silently replaced by the native default at the render boundary, so
	// the worker rendered with a different encoder than the config declared.
	// Fail at load instead: the vocabulary is exactly what the worker can
	// forward.
	switch c.Chronon.HardwareEncoder {
	case "", chronon.DefaultHardwareEncoder, chronon.HardwareEncoderNone:
	default:
		return fmt.Errorf("chronon.hardware_encoder must be %q, %q or empty (engine default), got %q",
			chronon.DefaultHardwareEncoder, chronon.HardwareEncoderNone, c.Chronon.HardwareEncoder)
	}
	if c.Chronon.Mode == "ipc" && c.Chronon.SocketPath == "" {
		return fmt.Errorf("chronon mode=ipc requires chronon.socket_path")
	}
	// encode_preset reaches the native NVENC encoder as --encode-preset, so a
	// mistyped preset must fail at config load instead of on the first GPU
	// job. Empty is valid and preserves the engine default.
	if err := chronon.ValidateEncodePreset(c.Chronon.EncodePreset); err != nil {
		return err
	}
	switch c.Chronon.PipePixFmt {
	case "", "nv12", "p010":
	default:
		return fmt.Errorf("chronon.pipe_pixfmt must be empty, %q or %q, got %q", "nv12", "p010", c.Chronon.PipePixFmt)
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
