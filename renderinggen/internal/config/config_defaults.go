package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chrononbin"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// config_defaults.go owns the default layer: the struct-tag and per-section
// defaults that apply before the YAML file and the environment overlay are
// applied, plus the workspace/host helpers those defaults need.

// DefaultQueueEndpoint is the loopback queue endpoint used by diagnostic
// commands before their flags or the environment override it. It lives here,
// with the other defaults, so no command or library repeats the literal.
const DefaultQueueEndpoint = "http://127.0.0.1:8081"

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
		c.Chronon.Home = chrononbin.DefaultHome
	}
	if c.Chronon.Mode == "" {
		c.Chronon.Mode = "cli"
	}
	// The default names the historical single-daemon socket. A worker given an
	// explicit socket_paths list must not also carry a socket it never dials:
	// an operator reading the effective config would otherwise see a daemon
	// that does not exist in the deployment.
	if c.Chronon.SocketPath == "" && len(c.Chronon.SocketPaths) == 0 {
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
	// Cache budgets: the historical wiring constants, now overridable. The
	// defaults were raised because the L2 budget is what decides whether a
	// repeated asset set is served from the content-addressed local mirror or
	// re-fetched from the object store on every job, and a GPU worker's working
	// set (background videos + portrait plates) crosses the old 10 GiB within a
	// handful of jobs. Both budgets are CAPS enforced by LRU eviction, so a
	// larger value costs idle disk, never correctness: an object that does not
	// fit is still fetched on demand.
	if c.ArtifactStore.L1MaxBytes == 0 {
		c.ArtifactStore.L1MaxBytes = 512 << 20 // 512 MiB small-object RAM cache
	}
	if c.ArtifactStore.L2MaxBytes == 0 {
		c.ArtifactStore.L2MaxBytes = 40 << 30 // 40 GiB NVMe cache
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
	applyLoggingDefaults(&c.Logging)
}

// applyLoggingDefaults fills the shipped log settings: info level in JSON, with
// per-job logs kept for 48 h and capped at 2000 files. The retention window is
// the one the chain's triage actually uses (the collector reads journals and job
// logs for the last 1–3 hours); 48 h leaves room to look at yesterday's run
// without letting a tmpfs jobs root grow unboundedly.
func applyLoggingDefaults(l *LoggingConfig) {
	if strings.TrimSpace(l.Level) == "" {
		l.Level = "info"
	}
	if strings.TrimSpace(l.Format) == "" {
		l.Format = workerlog.FormatJSON
	}
	if l.JobLogRetention == 0 {
		l.JobLogRetention = 48 * time.Hour
	}
	if l.JobLogMaxFiles == 0 {
		l.JobLogMaxFiles = 2000
	}
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
