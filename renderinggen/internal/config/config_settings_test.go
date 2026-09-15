package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// minimalConfig is the smallest document Load accepts. Every settings test
// starts from it so the assertions are about the setting under test and never
// about unrelated required fields.
const minimalConfig = `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
`

// TestSettingsDefaults pins the values that replaced the compile-time constants
// and the wiring literals. They must stay identical to what the worker shipped
// before the settings existed, otherwise this change silently alters scheduling
// or cache behaviour on every existing deployment.
func TestSettingsDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, want := cfg.ArtifactStore.L1MaxBytes, int64(256<<20); got != want {
		t.Errorf("artifact_store.l1_max_bytes default = %d, want %d", got, want)
	}
	if got, want := cfg.ArtifactStore.L2MaxBytes, int64(10<<30); got != want {
		t.Errorf("artifact_store.l2_max_bytes default = %d, want %d", got, want)
	}
	if got, want := cfg.Media.FFprobeBinary, "ffprobe"; got != want {
		t.Errorf("media.ffprobe_binary default = %q, want %q", got, want)
	}
	if got, want := cfg.Media.FFmpegBinary, "ffmpeg"; got != want {
		t.Errorf("media.ffmpeg_binary default = %q, want %q", got, want)
	}
	if got, want := cfg.Chronon.StallTimeout, 3*time.Minute; got != want {
		t.Errorf("chronon.stall_timeout default = %v, want %v", got, want)
	}
	pipeline := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"claim_long_poll", cfg.Pipeline.ClaimLongPoll, 25 * time.Second},
		{"claim_retry_delay", cfg.Pipeline.ClaimRetryDelay, 5 * time.Second},
		{"heartbeat_interval", cfg.Pipeline.HeartbeatInterval, 20 * time.Second},
		{"workspace_sweep_interval", cfg.Pipeline.WorkspaceSweepInterval, 10 * time.Minute},
		{"workspace_stale_after", cfg.Pipeline.WorkspaceStaleAfter, time.Hour},
		{"workspace_lease_refresh", cfg.Pipeline.WorkspaceLeaseRefresh, 10 * time.Minute},
		{"workspace_lease_ttl", cfg.Pipeline.WorkspaceLeaseTTL, 2 * time.Hour},
		{"lease_renew_backoff", cfg.Pipeline.LeaseRenewBackoff, 2 * time.Second},
	}
	for _, tc := range pipeline {
		if tc.got != tc.want {
			t.Errorf("pipeline.%s default = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if got, want := cfg.Pipeline.DegradedAfterFailures, 3; got != want {
		t.Errorf("pipeline.degraded_after_failures default = %d, want %d", got, want)
	}
	if got, want := cfg.Pipeline.LeaseRenewAttempts, 3; got != want {
		t.Errorf("pipeline.lease_renew_attempts default = %d, want %d", got, want)
	}
}

// TestSettingsFromYAML pins that the new settings are real configuration: a
// duration is written as a Go duration string and an integer as an integer.
func TestSettingsFromYAML(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
queue:
  endpoint: http://queue:8081
artifact_store:
  endpoint: http://store:9000
  l1_max_bytes: 1048576
  l2_max_bytes: 2097152
media:
  ffprobe_binary: /opt/bin/ffprobe
  ffmpeg_binary: /opt/bin/ffmpeg
chronon:
  stall_timeout: 90s
pipeline:
  claim_long_poll: 45s
  claim_retry_delay: 1s
  heartbeat_interval: 5s
  degraded_after_failures: 9
  workspace_sweep_interval: 30s
  workspace_stale_after: 15m
  workspace_lease_refresh: 45s
  workspace_lease_ttl: 4h
  lease_renew_attempts: 7
  lease_renew_backoff: 250ms
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ArtifactStore.Endpoint != "http://store:9000" {
		t.Errorf("artifact_store.endpoint = %q", cfg.ArtifactStore.Endpoint)
	}
	if cfg.ArtifactStore.L1MaxBytes != 1048576 || cfg.ArtifactStore.L2MaxBytes != 2097152 {
		t.Errorf("cache budgets = %d/%d, want 1048576/2097152", cfg.ArtifactStore.L1MaxBytes, cfg.ArtifactStore.L2MaxBytes)
	}
	if cfg.Media.FFprobeBinary != "/opt/bin/ffprobe" || cfg.Media.FFmpegBinary != "/opt/bin/ffmpeg" {
		t.Errorf("media binaries = %q/%q", cfg.Media.FFprobeBinary, cfg.Media.FFmpegBinary)
	}
	if cfg.Chronon.StallTimeout != 90*time.Second {
		t.Errorf("chronon.stall_timeout = %v, want 90s", cfg.Chronon.StallTimeout)
	}
	if cfg.Pipeline.ClaimLongPoll != 45*time.Second || cfg.Pipeline.ClaimRetryDelay != time.Second {
		t.Errorf("claim timings = %v/%v", cfg.Pipeline.ClaimLongPoll, cfg.Pipeline.ClaimRetryDelay)
	}
	if cfg.Pipeline.HeartbeatInterval != 5*time.Second || cfg.Pipeline.DegradedAfterFailures != 9 {
		t.Errorf("heartbeat settings = %v/%d", cfg.Pipeline.HeartbeatInterval, cfg.Pipeline.DegradedAfterFailures)
	}
	if cfg.Pipeline.WorkspaceSweepInterval != 30*time.Second || cfg.Pipeline.WorkspaceStaleAfter != 15*time.Minute {
		t.Errorf("workspace settings = %v/%v", cfg.Pipeline.WorkspaceSweepInterval, cfg.Pipeline.WorkspaceStaleAfter)
	}
	if cfg.Pipeline.WorkspaceLeaseRefresh != 45*time.Second || cfg.Pipeline.WorkspaceLeaseTTL != 4*time.Hour {
		t.Errorf("lease marker settings = %v/%v", cfg.Pipeline.WorkspaceLeaseRefresh, cfg.Pipeline.WorkspaceLeaseTTL)
	}
	if cfg.Pipeline.LeaseRenewAttempts != 7 || cfg.Pipeline.LeaseRenewBackoff != 250*time.Millisecond {
		t.Errorf("renew settings = %d/%v", cfg.Pipeline.LeaseRenewAttempts, cfg.Pipeline.LeaseRenewBackoff)
	}
}

// TestSettingsRejectNonDurationSyntax pins the fail-closed contract of the new
// duration fields: yaml.v3 refuses to coerce a bare integer into a
// time.Duration, so a document that writes `claim_long_poll: 25` (seconds in the
// author's head) is a load error rather than a 25 NANOSECOND poll.
func TestSettingsRejectNonDurationSyntax(t *testing.T) {
	for _, doc := range []string{
		minimalConfig + "pipeline:\n  claim_long_poll: 25\n",
		minimalConfig + "chronon:\n  stall_timeout: 180\n",
	} {
		if _, err := Load(writeConfig(t, doc)); err == nil {
			t.Errorf("expected a load error for a bare integer duration in:\n%s", doc)
		}
	}
}

// TestSettingsZeroMeansDefault pins the documented meaning of an explicit zero:
// these are plain scalars, so the decoder cannot report whether a zero came from
// the document or from an absent key. Zero therefore means "use the shipment
// default" and must not be an error, while a negative value is rejected (see
// TestSettingsRejectNonsense).
func TestSettingsZeroMeansDefault(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalConfig+`
chronon:
  stall_timeout: 0s
pipeline:
  claim_long_poll: 0s
  heartbeat_interval: 0s
  degraded_after_failures: 0
  lease_renew_attempts: 0
`))
	if err != nil {
		t.Fatalf("an explicit zero must mean 'use the default', got: %v", err)
	}
	if cfg.Chronon.StallTimeout != 3*time.Minute {
		t.Errorf("stall timeout = %v, want the default 3m", cfg.Chronon.StallTimeout)
	}
	if cfg.Pipeline.ClaimLongPoll != 25*time.Second || cfg.Pipeline.HeartbeatInterval != 20*time.Second {
		t.Errorf("claim/heartbeat = %v/%v, want the defaults 25s/20s", cfg.Pipeline.ClaimLongPoll, cfg.Pipeline.HeartbeatInterval)
	}
	if cfg.Pipeline.DegradedAfterFailures != 3 || cfg.Pipeline.LeaseRenewAttempts != 3 {
		t.Errorf("thresholds = %d/%d, want the defaults 3/3", cfg.Pipeline.DegradedAfterFailures, cfg.Pipeline.LeaseRenewAttempts)
	}
}

// TestSettingsRejectNonsense pins that the new settings are validated, not
// merely parsed: a negative budget, a negative timing and a lease TTL that does
// not outlive its refresh are all rejected at load.
func TestSettingsRejectNonsense(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{"negative l1 budget", minimalConfig + "artifact_store:\n  l1_max_bytes: -1\n"},
		{"negative l2 budget", minimalConfig + "artifact_store:\n  l2_max_bytes: -1\n"},
		{"negative stall timeout", minimalConfig + "chronon:\n  stall_timeout: -5s\n"},
		{"negative claim long poll", minimalConfig + "pipeline:\n  claim_long_poll: -1s\n"},
		{"negative heartbeat", minimalConfig + "pipeline:\n  heartbeat_interval: -5s\n"},
		{"negative sweep interval", minimalConfig + "pipeline:\n  workspace_sweep_interval: -1s\n"},
		{"negative stale age", minimalConfig + "pipeline:\n  workspace_stale_after: -1s\n"},
		{"negative degraded threshold", minimalConfig + "pipeline:\n  degraded_after_failures: -1\n"},
		{"negative renew attempts", minimalConfig + "pipeline:\n  lease_renew_attempts: -1\n"},
		{"negative renew backoff", minimalConfig + "pipeline:\n  lease_renew_backoff: -1s\n"},
		// A TTL at or below the refresh period would let the sweeper remove a
		// live render's workspace between two refreshes.
		{"lease ttl not above refresh", minimalConfig + "pipeline:\n  workspace_lease_refresh: 10m\n  workspace_lease_ttl: 10m\n"},
		{"lease ttl below refresh", minimalConfig + "pipeline:\n  workspace_lease_refresh: 10m\n  workspace_lease_ttl: 1m\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tc.doc)); err == nil {
				t.Fatalf("expected a load error for %s", tc.name)
			}
		})
	}
}

// TestEnvOverridesEveryKind pins that the overlay applies each supported scalar
// kind, including a Go duration string.
func TestEnvOverridesEveryKind(t *testing.T) {
	t.Setenv("RENDERINGGEN_WORKER_ID", "renderinggen-env")
	t.Setenv("RENDERINGGEN_WORKER_GPU_LANES", "5")
	t.Setenv("RENDERINGGEN_ARTIFACT_STORE_L2_MAX_BYTES", "1234")
	t.Setenv("RENDERINGGEN_DRIVE_ENABLED", "true")
	t.Setenv("RENDERINGGEN_DRIVE_MODE", "mock")
	t.Setenv("RENDERINGGEN_CHRONON_STALL_TIMEOUT", "75s")
	t.Setenv("RENDERINGGEN_PIPELINE_CLAIM_LONG_POLL", "40s")
	t.Setenv("RENDERINGGEN_PIPELINE_DEGRADED_AFTER_FAILURES", "6")
	t.Setenv("RENDERINGGEN_MEDIA_FFPROBE_BINARY", "/env/ffprobe")

	cfg, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Worker.ID != "renderinggen-env" {
		t.Errorf("worker id = %q", cfg.Worker.ID)
	}
	if cfg.Worker.GPULanes != 5 {
		t.Errorf("gpu_lanes = %d, want 5", cfg.Worker.GPULanes)
	}
	if cfg.ArtifactStore.L2MaxBytes != 1234 {
		t.Errorf("l2_max_bytes = %d, want 1234", cfg.ArtifactStore.L2MaxBytes)
	}
	if !cfg.Drive.Enabled {
		t.Error("drive.enabled should be true")
	}
	if cfg.Chronon.StallTimeout != 75*time.Second {
		t.Errorf("stall timeout = %v, want 75s", cfg.Chronon.StallTimeout)
	}
	if cfg.Pipeline.ClaimLongPoll != 40*time.Second {
		t.Errorf("claim long poll = %v, want 40s", cfg.Pipeline.ClaimLongPoll)
	}
	if cfg.Pipeline.DegradedAfterFailures != 6 {
		t.Errorf("degraded threshold = %d, want 6", cfg.Pipeline.DegradedAfterFailures)
	}
	if cfg.Media.FFprobeBinary != "/env/ffprobe" {
		t.Errorf("ffprobe binary = %q", cfg.Media.FFprobeBinary)
	}
}

// TestEnvBeatsYAML pins the documented precedence: the environment is the last
// word, because it is what a systemd unit or a container runtime sets.
func TestEnvBeatsYAML(t *testing.T) {
	t.Setenv("RENDERINGGEN_QUEUE_ENDPOINT", "http://from-env:8081")
	t.Setenv("RENDERINGGEN_CHRONON_STALL_TIMEOUT", "45s")
	cfg, err := Load(writeConfig(t, minimalConfig+`
chronon:
  stall_timeout: 5m
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Queue.Endpoint != "http://from-env:8081" {
		t.Errorf("queue endpoint = %q, want the environment value", cfg.Queue.Endpoint)
	}
	if cfg.Chronon.StallTimeout != 45*time.Second {
		t.Errorf("stall timeout = %v, want the environment value 45s", cfg.Chronon.StallTimeout)
	}
}

// TestLegacyStallTimeoutAliasStillWorks pins the compatibility promise for the
// pre-existing CHRONON_STALL_TIMEOUT variable: the value used to be read with
// os.Getenv inside the chronon client, so a deployment that sets it (and no
// RENDERINGGEN_* variable) must keep its override.
func TestLegacyStallTimeoutAliasStillWorks(t *testing.T) {
	t.Setenv("CHRONON_STALL_TIMEOUT", "2m30s")
	cfg, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Chronon.StallTimeout != 150*time.Second {
		t.Errorf("stall timeout = %v, want the legacy CHRONON_STALL_TIMEOUT value 2m30s", cfg.Chronon.StallTimeout)
	}

	// The canonical name wins when both are set: the alias is a fallback, never
	// an override of the documented setting.
	t.Setenv("RENDERINGGEN_CHRONON_STALL_TIMEOUT", "10s")
	cfg, err = Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("load with both names: %v", err)
	}
	if cfg.Chronon.StallTimeout != 10*time.Second {
		t.Errorf("stall timeout = %v, want the canonical RENDERINGGEN_ value 10s", cfg.Chronon.StallTimeout)
	}
}

// TestEnvRejectsInvalidValue pins that a malformed environment value names the
// variable and the expected format, instead of silently falling back to the
// default (the failure mode of the pointwise os.Getenv reads this overlay
// replaces).
func TestEnvRejectsInvalidValue(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"duration", "RENDERINGGEN_CHRONON_STALL_TIMEOUT", "soon"},
		{"duration without unit", "RENDERINGGEN_PIPELINE_CLAIM_LONG_POLL", "25"},
		{"integer", "RENDERINGGEN_WORKER_GPU_LANES", "two"},
		{"boolean", "RENDERINGGEN_DRIVE_ENABLED", "yesplease"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := Load(writeConfig(t, minimalConfig))
			if err == nil {
				t.Fatalf("%s=%q was accepted; want a load error", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error %q does not name the offending variable %s", err, tc.key)
			}
		})
	}
}

// TestEverySettingHasAnEnvName is the guard against the drift this overlay
// exists to prevent: a setting added to Config without a derivable environment
// name would be the one knob an operator cannot change, invisible until someone
// needed it. The overlay is driven by a capturing lookup, so the assertion is
// about REACHABILITY of every declared scalar, not about any cross-field
// validation rule.
func TestEverySettingHasAnEnvName(t *testing.T) {
	requested := map[string]bool{}
	lookup := func(key string) (string, bool) {
		requested[key] = true
		return "", false // "not set": nothing is applied, only discovered
	}
	var cfg Config
	if err := applyEnvToValue(reflect.ValueOf(&cfg).Elem(), nil, lookup); err != nil {
		t.Fatalf("overlay walk: %v", err)
	}
	declared := scalarEnvNames(reflect.TypeOf(Config{}), nil, map[string]bool{})
	for name := range declared {
		if !requested[name] {
			t.Errorf("setting %s is declared but the overlay never asks for it", name)
		}
	}
	// Legacy alias names are probed as fallbacks and intentionally do not name a
	// declared setting, so only canonical keys are checked in this direction.
	for name := range requested {
		if !strings.HasPrefix(name, EnvPrefix) {
			continue
		}
		if !declared[name] {
			t.Errorf("the overlay asks for %s, which no longer names a declared scalar setting", name)
		}
	}
	if len(declared) == 0 {
		t.Fatal("no scalar settings discovered; the guard would pass vacuously")
	}
}

// scalarEnvNames derives the RENDERINGGEN_* name of every scalar leaf of a
// configuration type, mirroring the overlay's own path construction.
func scalarEnvNames(t reflect.Type, path []string, out map[string]bool) map[string]bool {
	durationType := reflect.TypeOf(time.Duration(0))
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		next := append(append([]string(nil), path...), tag)
		if field.Type.Kind() == reflect.Struct && field.Type != durationType {
			scalarEnvNames(field.Type, next, out)
			continue
		}
		out[EnvPrefix+strings.ToUpper(strings.Join(next, "_"))] = true
	}
	return out
}
