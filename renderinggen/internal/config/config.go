// Package config loads and validates the RenderingGen worker configuration.
//
// Every setting is declared here ONCE and can be supplied from three places,
// applied in this order (later wins):
//
//  1. the struct-tag defaults in applyDefaults
//  2. the YAML file at the path passed to Load
//  3. the environment, through the single RENDERINGGEN_* overlay in
//     config_env.go (whose key is the uppercased yaml path, so
//     artifact_store.l1_max_bytes is RENDERINGGEN_ARTIFACT_STORE_L1_MAX_BYTES)
//
// The overlay exists because the runtime knobs used to be compile-time
// constants (cache budgets in the wiring, poll/heartbeat/lease intervals inside
// the pool functions) or pointwise os.Getenv reads scattered across packages
// (CHRONON_STALL_TIMEOUT). An operator can now change any of them without a
// rebuild, and a typo fails at load instead of on the first job.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	Media         MediaConfig      `yaml:"media"`
	Pipeline      PipelineConfig   `yaml:"pipeline"`
}

// MediaConfig locates the external certification tools. They are settings, not
// literals: the worker image, a host deployment and CI can each ship a
// different ffmpeg/ffprobe (a distro package, a static build, a wrapper), and a
// hardcoded "ffprobe" string made that choice invisible and untestable.
type MediaConfig struct {
	FFprobeBinary string `yaml:"ffprobe_binary"`
	FFmpegBinary  string `yaml:"ffmpeg_binary"`
}

// PipelineConfig holds the timings of the claim/render/finalize pipeline.
//
// These were spread as unnamed constants: the claim long-poll and its error
// retry inside runPrepPool, the heartbeat interval and degraded threshold in
// startup_helpers.go, the workspace sweeper interval/age there too, the GPU
// workspace lease refresh and TTL inside runGPULane/PrepareJob, and the lease
// renewal attempts/backoff inside renewWithRetry. They are configuration
// because they are deployment facts — a host with a slow object store wants a
// longer claim retry, a host with big workspaces wants a faster sweep — and
// because a value spread over five call sites cannot be reasoned about as one
// policy.
type PipelineConfig struct {
	// ClaimLongPoll is how long a claim request may wait server-side for work
	// before it returns empty (the queue's long-poll window).
	ClaimLongPoll time.Duration `yaml:"claim_long_poll"`
	// ClaimRetryDelay is the pause after a failed claim before retrying.
	ClaimRetryDelay time.Duration `yaml:"claim_retry_delay"`
	// HeartbeatInterval is the queue liveness heartbeat period.
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	// DegradedAfterFailures is the number of consecutive heartbeat failures
	// after which /health reports degraded instead of ready.
	DegradedAfterFailures int `yaml:"degraded_after_failures"`
	// WorkspaceSweepInterval is how often crashed-run workspaces are reaped.
	WorkspaceSweepInterval time.Duration `yaml:"workspace_sweep_interval"`
	// WorkspaceStaleAfter is the age without a valid lease marker after which a
	// workspace is sweepable.
	WorkspaceStaleAfter time.Duration `yaml:"workspace_stale_after"`
	// WorkspaceLeaseRefresh is how often the GPU lane refreshes the workspace
	// liveness marker for the render it is running.
	WorkspaceLeaseRefresh time.Duration `yaml:"workspace_lease_refresh"`
	// WorkspaceLeaseTTL is the validity window written into the marker on each
	// refresh. It must comfortably exceed WorkspaceLeaseRefresh so a single
	// missed refresh cannot make a live render sweepable.
	WorkspaceLeaseTTL time.Duration `yaml:"workspace_lease_ttl"`
	// LeaseRenewAttempts is the queue-lease renewal retry budget for one tick.
	LeaseRenewAttempts int `yaml:"lease_renew_attempts"`
	// LeaseRenewBackoff is the first retry delay; it doubles per attempt up to
	// half the lease interval.
	LeaseRenewBackoff time.Duration `yaml:"lease_renew_backoff"`
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
	// L1MaxBytes caps the in-memory (RAM) cache and L2MaxBytes the on-disk
	// (NVMe) cache. Both were compile-time constants in the wiring, so an
	// operator on a host with a different RAM/NVMe balance could not rebalance
	// them without editing and rebuilding the worker. 0 means unbounded, which
	// is exactly the storage.Options contract.
	L1MaxBytes int64 `yaml:"l1_max_bytes"`
	L2MaxBytes int64 `yaml:"l2_max_bytes"`
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
	// StallTimeout is the maximum duration a render may produce no output at
	// all before the watchdog aborts it. It is a configuration value, not a
	// package constant plus an unpublished CHRONON_STALL_TIMEOUT read: the
	// legacy variable is still honored as an alias of this setting (see
	// config_env.go), so existing deployments keep their override while the
	// worker keeps ONE authority for the value.
	StallTimeout time.Duration `yaml:"stall_timeout"`
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

// Load reads the YAML config file at path, applies the environment overlay,
// fills defaults and validates the result.
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

	// Environment before defaults: an operator override must beat the file,
	// and a field the environment did NOT set must still receive the
	// profile-aware default below.
	if err := applyEnvOverrides(&cfg); err != nil {
		return nil, err
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
		// The warm daemon is the GPU profile's default transport, because this
		// profile IS the native hot path: every shipped GPU config (config.yaml,
		// infra/native/*, infra/docker/worker-config*) declares mode: ipc, and
		// the CLI transport re-initializes the whole engine per job. Measured on
		// the RTX A4000 host (2026-09-13): chronon_job_backend_init_ms 488-570 ms
		// on EVERY render, i.e. ~2 s over a 4-clip batch (~7% of the render-plane
		// wall) purely to re-open device/pipeline/glyph-atlas state. Leaving the
		// transport to the global "cli" default silently dropped the daemon for
		// any GPU-profile config that omitted the key. A deployment that really
		// wants a cold spawn still opts out with an explicit `mode: cli`
		// (pinned by TestGPUVulkanNativeProfilePreservesExplicitCLITransport).
		if c.Chronon.Mode == "" {
			c.Chronon.Mode = "ipc"
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
	// Cache budgets: the historical wiring constants, now overridable.
	if c.ArtifactStore.L1MaxBytes == 0 {
		c.ArtifactStore.L1MaxBytes = 256 << 20 // 256 MiB small-object RAM cache
	}
	if c.ArtifactStore.L2MaxBytes == 0 {
		c.ArtifactStore.L2MaxBytes = 10 << 30 // 10 GiB NVMe cache
	}
	// Certification tools. The bare command name preserves the historical
	// PATH lookup, so an unspecified deployment behaves exactly as before.
	if c.Media.FFprobeBinary == "" {
		c.Media.FFprobeBinary = "ffprobe"
	}
	if c.Media.FFmpegBinary == "" {
		c.Media.FFmpegBinary = "ffmpeg"
	}
	if c.Chronon.StallTimeout == 0 {
		c.Chronon.StallTimeout = chronon.DefaultStallTimeout
	}
	applyPipelineDefaults(&c.Pipeline)
}

// applyPipelineDefaults fills the pipeline timings with the values the worker
// shipped as unnamed constants, so an unmodified deployment keeps byte-for-byte
// the same scheduling behaviour.
//
// Zero means "not configured, use the shipment default"; a NEGATIVE value is
// rejected by validate. The distinction matters because these fields are plain
// scalars: the decoder cannot report whether a zero came from the document or
// from an absent key, so treating zero as an error would reject every
// deployment that does not spell out all ten timings. A negative value, by
// contrast, can only be deliberate, and no deployment can mean it.
func applyPipelineDefaults(p *PipelineConfig) {
	if p.ClaimLongPoll == 0 {
		p.ClaimLongPoll = 25 * time.Second
	}
	if p.ClaimRetryDelay == 0 {
		p.ClaimRetryDelay = 5 * time.Second
	}
	if p.HeartbeatInterval == 0 {
		p.HeartbeatInterval = 20 * time.Second
	}
	if p.DegradedAfterFailures == 0 {
		p.DegradedAfterFailures = 3
	}
	if p.WorkspaceSweepInterval == 0 {
		p.WorkspaceSweepInterval = 10 * time.Minute
	}
	if p.WorkspaceStaleAfter == 0 {
		p.WorkspaceStaleAfter = time.Hour
	}
	if p.WorkspaceLeaseRefresh == 0 {
		p.WorkspaceLeaseRefresh = 10 * time.Minute
	}
	if p.WorkspaceLeaseTTL == 0 {
		p.WorkspaceLeaseTTL = 2 * time.Hour
	}
	if p.LeaseRenewAttempts == 0 {
		p.LeaseRenewAttempts = 3
	}
	if p.LeaseRenewBackoff == 0 {
		p.LeaseRenewBackoff = 2 * time.Second
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
		if c.Chronon.Mode != "ipc" {
			return fmt.Errorf("%s requires chronon.mode=ipc (CLI mode disallowed for production GPU profile)", ProfileGPUVulkanNative)
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
	// Negative cache budgets are nonsense (0 means unbounded, so they cannot be
	// used to disable a tier); a negative timer is a value the pipeline would
	// treat as "use the default", silently discarding the operator's intent.
	if c.ArtifactStore.L1MaxBytes < 0 || c.ArtifactStore.L2MaxBytes < 0 {
		return fmt.Errorf("artifact_store cache budgets must be >= 0 (0 = unbounded), got l1=%d l2=%d", c.ArtifactStore.L1MaxBytes, c.ArtifactStore.L2MaxBytes)
	}
	if c.Media.FFprobeBinary == "" || c.Media.FFmpegBinary == "" {
		return fmt.Errorf("media.ffprobe_binary and media.ffmpeg_binary must be non-empty")
	}
	if c.Chronon.StallTimeout < 0 {
		return fmt.Errorf("chronon.stall_timeout must not be negative, got %v", c.Chronon.StallTimeout)
	}
	if err := c.Pipeline.validate(); err != nil {
		return err
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

// validate rejects timings the pipeline cannot honour. It runs after the
// defaults, so a zero here already means "use the shipment default" and every
// value is positive: what this rejects is a NEGATIVE setting, which can only
// come from the file or the environment and is never meant, plus the lease
// TTL/refresh relationship.
func (p PipelineConfig) validate() error {
	for name, value := range map[string]time.Duration{
		"pipeline.claim_long_poll":          p.ClaimLongPoll,
		"pipeline.claim_retry_delay":        p.ClaimRetryDelay,
		"pipeline.heartbeat_interval":       p.HeartbeatInterval,
		"pipeline.workspace_sweep_interval": p.WorkspaceSweepInterval,
		"pipeline.workspace_stale_after":    p.WorkspaceStaleAfter,
		"pipeline.workspace_lease_refresh":  p.WorkspaceLeaseRefresh,
		"pipeline.workspace_lease_ttl":      p.WorkspaceLeaseTTL,
		"pipeline.lease_renew_backoff":      p.LeaseRenewBackoff,
	} {
		if value < 0 {
			return fmt.Errorf("%s must not be negative, got %v", name, value)
		}
	}
	if p.DegradedAfterFailures < 0 {
		return fmt.Errorf("pipeline.degraded_after_failures must not be negative, got %d", p.DegradedAfterFailures)
	}
	if p.LeaseRenewAttempts < 0 {
		return fmt.Errorf("pipeline.lease_renew_attempts must not be negative, got %d", p.LeaseRenewAttempts)
	}
	// A lease TTL at or below the refresh period leaves a live render
	// sweepable between two refreshes: the workspace would be removed under the
	// render that owns it.
	if p.WorkspaceLeaseTTL <= p.WorkspaceLeaseRefresh {
		return fmt.Errorf("pipeline.workspace_lease_ttl (%v) must exceed pipeline.workspace_lease_refresh (%v)", p.WorkspaceLeaseTTL, p.WorkspaceLeaseRefresh)
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
