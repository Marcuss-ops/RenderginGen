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
//
// File layout: this file owns Load and the environment overlay entry points,
// config_types.go the declared sections, config_defaults.go the default layer,
// config_validate.go the checks and config_env.go the RENDERINGGEN_* overlay.
package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

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
	if err := cfg.ArtifactStore.resolveToken(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// resolveToken loads the shared bearer token from TokenFile when no token was
// supplied directly. Reading happens at Load, not per request: a bad path must
// fail the worker at startup, and the artifact hot path must not stat and read
// a secret file for every fetch or store operation.
func (s *StorageConfig) resolveToken() error {
	if s.Token != "" || s.TokenFile == "" {
		return nil
	}
	data, err := os.ReadFile(s.TokenFile)
	if err != nil {
		return fmt.Errorf("artifact_store.token_file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return fmt.Errorf("artifact_store.token_file %s is empty", s.TokenFile)
	}
	s.Token = token
	return nil
}

// StrictNative reports whether this configuration demands the native GPU hot
// path. It is the SINGLE derivation of that rule on the configuration side:
// applyDefaults already folds the profile into StrictNativeBackend, and the
// wiring (main.go) asks this method instead of re-comparing the profile string
// with `|| cfg.Chronon.Profile == "gpu-vulkan-native"` in three places.
func (c ChrononConfig) StrictNative() bool {
	return c.StrictNativeBackend || c.Profile == ProfileGPUVulkanNative
}
