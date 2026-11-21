package config

import (
	"time"
)

// config_types.go declares the worker configuration once: every section is a
// struct whose tags carry the YAML key and the default that applyDefaults
// applies. Loading lives in config.go, defaults in config_defaults.go and the
// cross-field checks in config_validate.go.

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
	Logging       LoggingConfig    `yaml:"logging"`
}

// LoggingConfig configures the worker's log surface (see internal/workerlog).
//
// It is configuration because the level decides what the GPU worker records
// about a render — a noisy host wants info, a post-mortem wants debug — and
// because the per-job log files are a deployment size decision: they live under
// the jobs root, which is frequently tmpfs (RAM). Before this, the worker had no
// level at all (everything was an un-levelled log.Printf) and no record of its
// own, so the collector depended entirely on the host's journald retention.
type LoggingConfig struct {
	Level  string `yaml:"level"`  // debug | info (default) | warn | error
	Format string `yaml:"format"` // json (default) | text
	// JobLogRetention is how long a finished job's durable log file is kept
	// under <workspace.root>/.workerlogs before the startup prune removes it.
	JobLogRetention time.Duration `yaml:"job_log_retention"`
	// JobLogMaxFiles caps how many durable job logs are kept; the oldest are
	// pruned first once the count is exceeded.
	JobLogMaxFiles int `yaml:"job_log_max_files"`
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

	// ReceiptVerify is the output-verification POLICY the worker requests from
	// Chronon and enforces from its receipt: "" (fast), "fast", "normal" or
	// "certify". It is a setting because it decides whether the worker demands
	// proof of the render, and because the value has to reach the engine
	// explicitly — the historical arrangement read RENDERINGGEN_RECEIPT_VERIFY
	// inside the processor, so "what policy did this worker run under" was
	// answerable only by inspecting the process environment. The accepted
	// spelling is chronon's wire vocabulary (internal/chronon owns it); config
	// validates it here so a typo fails at load rather than degrading to fast.
	ReceiptVerify string `yaml:"receipt_verify"`

	// DeepVisualValidation enables the sampled ffmpeg visual gate for jobs
	// whose plan carries an authored overlay. Off by default: it costs an extra
	// decode of the finished output. It is a setting for the same reason as
	// ReceiptVerify — it was an undiscoverable RENDERINGGEN_DEEP_VISUAL read.
	DeepVisualValidation bool `yaml:"deep_visual_validation"`

	// KeepWorkspace leaves a finished job's workspace on disk for inspection.
	// Off by default; a debugging aid that must be turned on deliberately,
	// because the jobs root is often tmpfs (RAM).
	KeepWorkspace bool `yaml:"keep_workspace"`

	// NativeAVCCSHA256 is the opt-in NVENC recertification allowlist. Each
	// entry is an avcC (SPS/PPS) SHA-256 that a canonical output profile may
	// accept without the x264 re-encode. Empty keeps the shipped x264-only
	// gate; add a hash only after the downstream assembly contract accepts
	// the same parameter sets.
	NativeAVCCSHA256 []string `yaml:"native_avcc_sha256"`
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
	// Token is the object store's shared bearer token, sent as
	// `Authorization: Bearer` on every artifact request. Empty means the
	// store is open (local/dev posture); when the store sets
	// OBJECTSTORE_TOKEN, this must match or every fetch/store fails with a
	// 401 that names this field. Supplied via artifact_store.token (inline),
	// artifact_store.token_file (preferred) or
	// RENDERINGGEN_ARTIFACT_STORE_TOKEN. An explicit Token always wins over
	// TokenFile, so an environment override can still rotate the secret
	// without editing the file.
	Token string `yaml:"token"`
	// TokenFile is a path to a file containing the bearer token. It is the
	// preferred deployment spelling: the secret stays out of the process
	// environment and out of the shipped YAML, while the path remains visible
	// in the configuration. Load reads it once, after the environment overlay,
	// and only when Token is empty; a missing or empty file fails startup
	// instead of silently leaving the store unauthenticated.
	TokenFile string `yaml:"token_file"`
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
	Profile    string `yaml:"profile"` // software-cli | gpu-vulkan-native
	Backend    string `yaml:"backend"`
	Home       string `yaml:"home"`
	Binary     string `yaml:"binary"`      // explicit chronon3d_cli path
	Mode       string `yaml:"mode"`        // "cli" (default) | "ipc"
	SocketPath string `yaml:"socket_path"` // unix socket when Mode == "ipc"
	// SocketPaths lists ONE unix socket per daemon when Mode == "ipc": renders
	// spread round-robin over them (see chronon.SpreadAcrossSockets), so
	// gpu_lanes > 1 across N sockets means N daemons rendering concurrently
	// instead of one daemon queueing every lane. It is the "add a daemon" half
	// of the lane/daemon parity rule: raising gpu_lanes against a single daemon
	// only grows gpu_lane_wait_ms. When set it takes precedence over the single
	// SocketPath (which is then left empty, since the worker never dials it).
	SocketPaths          []string `yaml:"socket_paths"`
	NativeOutputProfiles bool     `yaml:"native_output_profiles"`
	Report               bool     `yaml:"report"`
	HardwareEncoder      string   `yaml:"hardware_encoder"`
	EncoderBackend       string   `yaml:"encoder_backend"`
	// EncodePreset is the explicit FFmpeg NVENC preset passed to Chronon for
	// native GPU jobs (e.g. "p2" for the throughput tier). Empty preserves the
	// engine default; the worker never invents a preset when none is set.
	EncodePreset string `yaml:"encode_preset"`
	// PipePixFmt selects the host-frame pipe format used by GPU compositions.
	// NV12 avoids an unnecessary RGBA conversion on the normal 8-bit path;
	// P010 is available for 10-bit workflows.
	PipePixFmt string `yaml:"pipe_pixfmt"`
	// NoDirtyRects disables the dirty-rectangle invalidation optimization for
	// every render this worker performs. It is a per-deployment switch because
	// the optimization trades correctness at moving layer boundaries for a
	// smaller per-frame update; a workload that pans overlapping full-canvas
	// images can show a torn band where the previous frame and the new one
	// share no pixels. Default false preserves the historical behavior.
	NoDirtyRects        bool `yaml:"no_dirty_rects"`
	StrictNativeBackend bool `yaml:"strict_native_backend"`
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

	// ChunkBytes is the resumable-upload chunk size. Zero uses the publisher's
	// default (large enough to cover a rendered segment in one request). It is
	// a setting because upload throughput against a quota-limited account is a
	// deployment property, and the historical
	// RENDERINGGEN_DRIVE_CHUNK_BYTES read lived inside the publisher where no
	// operator could see what it was set to.
	ChunkBytes int64 `yaml:"chunk_bytes"`
}

// ArtifactDBConfig configures the optional worker-local artifact mirror.
// When Path is set, every rendered job writes one ArtifactRecord to a local
// SQLite mirror after the object store accepts the bytes; empty disables the
// mirror (the default). Central queue PostgreSQL is authoritative for the
// artifact, hash, probe facts and metrics.
type ArtifactDBConfig struct {
	Path string `yaml:"path"` // SQLite mirror file; empty = mirror disabled
}
