// Package testenv centralizes the opt-in gates used by the live Chronon and
// media suites. Tests previously copied the same CHRONON_HOME default, binary
// resolution and skip policy into a dozen files; a change to one copy left the
// others silently different.
package testenv

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chrononbin"
)

const (
	// SkipGPUEnv is the documented opt-out for real-engine/GPU test work. Any
	// non-empty value disables it; the value is not interpreted, so a stale
	// "0" cannot silently re-enable GPU work.
	SkipGPUEnv = "RENDERINGGEN_SKIP_GPU_E2E"
	// ChrononHomeEnv names an alternate Chronon install prefix.
	ChrononHomeEnv = "CHRONON_HOME"
	// ChrononBinaryEnv is the client's explicit binary override.
	ChrononBinaryEnv = "CHRONON_BINARY"
	// ChrononBinEnv is the overlay certification suite's opt-in binary knob.
	ChrononBinEnv = "CHRONON_BIN"
)

// SkipGPU disables the calling test when the real-engine opt-out is set.
func SkipGPU(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv(SkipGPUEnv)) != "" {
		t.Skipf("%s is set: the real-engine suite is disabled for this run", SkipGPUEnv)
	}
}

// ChrononHome returns CHRONON_HOME or the runtime image's default prefix.
func ChrononHome(t *testing.T) string {
	t.Helper()
	return chrononbin.Home(os.Getenv(ChrononHomeEnv))
}

// RequireChronon resolves the engine binary, skipping when the suite is opted
// out or the binary is absent. Resolution honors CHRONON_BINARY first and then
// the CHRONON_HOME layouts, matching chronon.Client.Binary.
func RequireChronon(t *testing.T) string {
	t.Helper()
	SkipGPU(t)
	binary := chrononbin.Resolve(os.Getenv(ChrononHomeEnv), "", os.Getenv(ChrononBinaryEnv))
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("chronon3d_cli not available at %s (set %s or %s): %v", binary, ChrononHomeEnv, ChrononBinaryEnv, err)
	}
	return binary
}

// RequireChrononOptIn is the stricter overlay-certification gate: the caller
// must name the binary with CHRONON_BIN. A build discovered under CHRONON_HOME
// is not consent, because a bare `go test ./...` would then render video.
func RequireChrononOptIn(t *testing.T) string {
	t.Helper()
	SkipGPU(t)
	binary := strings.TrimSpace(os.Getenv(ChrononBinEnv))
	if binary == "" {
		t.Skipf("the real-engine suites are opt-in: set %s=/path/to/chronon3d_cli to run them", ChrononBinEnv)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("%s=%s is not available: %v", ChrononBinEnv, binary, err)
	}
	return binary
}

// RequireTools skips the calling test when any named external tool is missing.
func RequireTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available: %v", tool, err)
		}
	}
}
