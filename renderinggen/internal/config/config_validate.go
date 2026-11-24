package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// config_validate.go owns validation: the cross-field checks that make a typo
// or a contradictory pair fail at load instead of on the first job, and the
// per-section checks they delegate to.

func (c *Config) validate() error {
	if err := c.validateWorker(); err != nil {
		return err
	}
	if err := c.validateChrononProfile(); err != nil {
		return err
	}
	if err := c.validateChrononTransport(); err != nil {
		return err
	}
	if err := c.validateEndpointsAndStorage(); err != nil {
		return err
	}
	if err := c.Pipeline.validate(); err != nil {
		return err
	}
	if err := c.Logging.validate(); err != nil {
		return err
	}
	return c.validateDrive()
}

func (c *Config) validateWorker() error {
	if c.Worker.PipelineWorkers < 1 || c.Worker.PipelineWorkers > 16 {
		return fmt.Errorf("worker.pipeline_workers must be between 1 and 16")
	}
	if c.Worker.GPULanes < 1 || c.Worker.GPULanes > 8 {
		return fmt.Errorf("worker.gpu_lanes must be between 1 and 8")
	}
	return nil
}

func (c *Config) validateChrononProfile() error {
	if c.Chronon.Profile != "" && c.Chronon.Profile != ProfileGPUVulkanNative && c.Chronon.Profile != ProfileSoftwareCLI {
		return fmt.Errorf("chronon.profile must be %s or %s", ProfileGPUVulkanNative, ProfileSoftwareCLI)
	}
	if !c.Chronon.StrictNative() {
		return nil
	}
	if c.Chronon.Backend != "vulkan" {
		return fmt.Errorf("strict native backend requires chronon.backend=vulkan")
	}
	if c.Chronon.HardwareEncoder != chronon.DefaultHardwareEncoder {
		return fmt.Errorf("strict native backend requires chronon.hardware_encoder=%s", chronon.DefaultHardwareEncoder)
	}
	if c.Chronon.Mode != "ipc" {
		return fmt.Errorf("strict native backend requires chronon.mode=ipc")
	}
	if c.Chronon.EncoderBackend != "" && c.Chronon.EncoderBackend != "native" {
		return fmt.Errorf("strict native backend requires chronon.encoder_backend=native")
	}
	return nil
}

func (c *Config) validateChrononTransport() error {
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
	switch c.Chronon.EncoderBackend {
	case "", "native", "pipe":
	default:
		return fmt.Errorf("chronon.encoder_backend must be empty, %q or %q, got %q", "native", "pipe", c.Chronon.EncoderBackend)
	}
	if err := c.validateSocketPaths(); err != nil {
		return err
	}
	if c.Chronon.Mode == "ipc" && c.Chronon.SocketPath == "" && len(c.Chronon.SocketPaths) == 0 {
		return fmt.Errorf("chronon mode=ipc requires chronon.socket_path or chronon.socket_paths")
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
	return nil
}

// validateSocketPaths rejects blank or repeated daemon sockets. The ipc
// transport spreads lanes across this list, so a doubtful entry would silently
// halve throughput instead of failing at load.
func (c *Config) validateSocketPaths() error {
	seenSockets := make(map[string]struct{}, len(c.Chronon.SocketPaths))
	for _, socket := range c.Chronon.SocketPaths {
		trimmed := strings.TrimSpace(socket)
		if trimmed == "" {
			return fmt.Errorf("chronon.socket_paths must not contain an empty socket path")
		}
		if _, duplicate := seenSockets[trimmed]; duplicate {
			return fmt.Errorf("chronon.socket_paths repeats socket %q; each socket is one daemon", trimmed)
		}
		seenSockets[trimmed] = struct{}{}
	}
	return nil
}

func (c *Config) validateEndpointsAndStorage() error {
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
	return nil
}

func (c *Config) validateDrive() error {
	if !c.Drive.Enabled {
		return nil
	}
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
	return nil
}

// validate rejects timings the pipeline cannot honour. It runs after the
// defaults, so a zero here already means "use the shipment default" and every
// value is positive: what this rejects is a NEGATIVE setting, which can only
// come from the file or the environment and is never meant, plus the lease
// TTL/refresh relationship.
func isLowerHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (p PipelineConfig) validate() error {
	for _, hash := range p.NativeAVCCSHA256 {
		if !isLowerHexSHA256(hash) {
			return fmt.Errorf("pipeline.native_avcc_sha256 entry %q must be 64 lowercase hex characters", hash)
		}
	}
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
	// The accepted set is chronon's receipt-verify vocabulary. Declaring it here
	// as literals (rather than importing the engine client) keeps config free of
	// a dependency on the renderer while still failing a typo at load: silently
	// degrading an unknown value to fast would mean an operator who asked for
	// proof ran without it.
	switch p.ReceiptVerify {
	case "", "fast", "normal", "sample", "certify":
	default:
		return fmt.Errorf("pipeline.receipt_verify %q must be one of \"\" (fast), fast, normal, sample, certify", p.ReceiptVerify)
	}
	return nil
}

// validate rejects a log level or format the worker cannot honour. The level
// spelling is owned by internal/workerlog (ParseLevel), so config cannot accept
// a value the logger would reject at startup — a worker that fails to install
// its log surface must fail at LOAD, where the operator sees the file they got
// wrong.
func (l LoggingConfig) validate() error {
	if _, err := workerlog.ParseLevel(l.Level); err != nil {
		return fmt.Errorf("logging.level: %w", err)
	}
	switch l.Format {
	case workerlog.FormatJSON, workerlog.FormatText:
	default:
		return fmt.Errorf("logging.format %q must be %q or %q", l.Format, workerlog.FormatJSON, workerlog.FormatText)
	}
	if l.JobLogRetention < 0 {
		return fmt.Errorf("logging.job_log_retention must not be negative, got %v", l.JobLogRetention)
	}
	if l.JobLogMaxFiles < 0 {
		return fmt.Errorf("logging.job_log_max_files must not be negative, got %d", l.JobLogMaxFiles)
	}
	return nil
}
