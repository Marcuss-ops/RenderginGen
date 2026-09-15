package config

import (
	"strings"
	"testing"
)

// The verification policy, the deep-visual gate, workspace retention and the
// Drive chunk size used to be read pointwise with os.Getenv from the packages
// that needed them. They are settings now, which is only an improvement if the
// names deployments already set keep working and the values are validated where
// a typo can be reported.

// TestVerificationSettingsDefaults pins the defaults to what the removed
// environment reads resolved to when unset: fast verification, no deep visual
// gate, no workspace retention, and the publisher's own chunk size (0 means
// "use the publisher default" rather than a chunk size of zero bytes).
func TestVerificationSettingsDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Pipeline.ReceiptVerify != "" {
		t.Errorf("pipeline.receipt_verify default = %q, want empty (fast)", cfg.Pipeline.ReceiptVerify)
	}
	if cfg.Pipeline.DeepVisualValidation {
		t.Error("pipeline.deep_visual_validation must default to off")
	}
	if cfg.Pipeline.KeepWorkspace {
		t.Error("pipeline.keep_workspace must default to off (the jobs root is often tmpfs)")
	}
	if cfg.Drive.ChunkBytes != 0 {
		t.Errorf("drive.chunk_bytes default = %d, want 0 (use the publisher default)", cfg.Drive.ChunkBytes)
	}
}

// TestLegacyEnvironmentAliasesStillApply pins the compatibility promise: each
// short name the code used to read directly still reaches the setting, and the
// canonical name derived from the yaml path wins when both are set.
func TestLegacyEnvironmentAliasesStillApply(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		check   func(*Config) bool
		message string
	}{
		{
			name:    "RENDERINGGEN_RECEIPT_VERIFY",
			env:     map[string]string{"RENDERINGGEN_RECEIPT_VERIFY": "normal"},
			check:   func(c *Config) bool { return c.Pipeline.ReceiptVerify == "normal" },
			message: "the receipt-verify policy must still be settable by its historical name",
		},
		{
			name:    "RENDERINGGEN_DEEP_VISUAL",
			env:     map[string]string{"RENDERINGGEN_DEEP_VISUAL": "1"},
			check:   func(c *Config) bool { return c.Pipeline.DeepVisualValidation },
			message: "=1 must still enable the deep visual gate",
		},
		{
			name:    "RENDERINGGEN_KEEP_WORKSPACE",
			env:     map[string]string{"RENDERINGGEN_KEEP_WORKSPACE": "1"},
			check:   func(c *Config) bool { return c.Pipeline.KeepWorkspace },
			message: "=1 must still retain workspaces",
		},
		{
			name:    "canonical key wins over the alias",
			env:     map[string]string{"RENDERINGGEN_RECEIPT_VERIFY": "fast", "RENDERINGGEN_PIPELINE_RECEIPT_VERIFY": "certify"},
			check:   func(c *Config) bool { return c.Pipeline.ReceiptVerify == "certify" },
			message: "the canonical key must win when both are set",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			cfg, err := Load(writeConfig(t, minimalConfig))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if !tc.check(cfg) {
				t.Fatalf("%s: %s", tc.name, tc.message)
			}
		})
	}
}

// yamlScalar renders a scalar for the generated documents. The empty string has
// to be quoted (a bare key with no value is null, not ""), and every value used
// here is a plain identifier otherwise.
func yamlScalar(value string) string {
	if value == "" {
		return `""`
	}
	return value
}

// TestReceiptVerifyMustBeAKnownPolicy pins fail-fast: a policy the engine client
// cannot resolve is rejected at load. Silently degrading an unrecognized value to
// fast would mean an operator who asked for proof ran without it.
func TestReceiptVerifyMustBeAKnownPolicy(t *testing.T) {
	for _, value := range []string{"", "fast", "normal", "certify"} {
		doc := minimalConfig + "pipeline:\n  receipt_verify: " + yamlScalar(value) + "\n"
		if _, err := Load(writeConfig(t, doc)); err != nil {
			t.Errorf("receipt_verify %q must be accepted: %v", value, err)
		}
	}
	for _, value := range []string{"Certify", "strict", "full", "decode"} {
		doc := minimalConfig + "pipeline:\n  receipt_verify: " + yamlScalar(value) + "\n"
		_, err := Load(writeConfig(t, doc))
		if err == nil {
			t.Errorf("receipt_verify %q must be rejected at load", value)
			continue
		}
		if !strings.Contains(err.Error(), "receipt_verify") {
			t.Errorf("error for %q = %q, want it to name the setting", value, err)
		}
	}
}

// TestVerificationSettingsFromTheEnvironmentOverlay pins that the derived
// canonical keys work for every new setting, which is what makes a new setting
// deployable without touching the yaml at all.
func TestVerificationSettingsFromTheEnvironmentOverlay(t *testing.T) {
	t.Setenv("RENDERINGGEN_PIPELINE_RECEIPT_VERIFY", "certify")
	t.Setenv("RENDERINGGEN_PIPELINE_DEEP_VISUAL_VALIDATION", "true")
	t.Setenv("RENDERINGGEN_PIPELINE_KEEP_WORKSPACE", "true")
	t.Setenv("RENDERINGGEN_DRIVE_CHUNK_BYTES", "8388608")

	cfg, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Pipeline.ReceiptVerify != "certify" {
		t.Errorf("receipt_verify = %q, want certify", cfg.Pipeline.ReceiptVerify)
	}
	if !cfg.Pipeline.DeepVisualValidation || !cfg.Pipeline.KeepWorkspace {
		t.Errorf("booleans = %v/%v, want both true", cfg.Pipeline.DeepVisualValidation, cfg.Pipeline.KeepWorkspace)
	}
	if cfg.Drive.ChunkBytes != 8388608 {
		t.Errorf("drive.chunk_bytes = %d, want 8388608", cfg.Drive.ChunkBytes)
	}
}

// TestVerificationSettingsRejectBadEnvironmentValues pins that the overlay
// validates instead of falling back: a bogus boolean or integer fails Load with
// the variable named, rather than leaving the worker on a default nobody chose.
func TestVerificationSettingsRejectBadEnvironmentValues(t *testing.T) {
	cases := map[string]string{
		"RENDERINGGEN_PIPELINE_DEEP_VISUAL_VALIDATION": "yes-please",
		"RENDERINGGEN_PIPELINE_KEEP_WORKSPACE":         "sometimes",
		"RENDERINGGEN_DRIVE_CHUNK_BYTES":               "8MiB",
	}
	for key, value := range cases {
		key, value := key, value
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			_, err := Load(writeConfig(t, minimalConfig))
			if err == nil {
				t.Fatalf("%s=%q must fail to load", key, value)
			}
			if !strings.Contains(err.Error(), key) {
				t.Fatalf("error = %q, want it to name %s", err, key)
			}
		})
	}
}
