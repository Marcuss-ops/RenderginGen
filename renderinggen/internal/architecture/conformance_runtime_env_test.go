// conformance_runtime_env_test.go pins the HOST RUNTIME ENVIRONMENT contract
// of the native systemd units: every Go service the repo ships must declare
// its GC policy (GOGC + GOMEMLIMIT) in the unit itself, so the memory behavior
// of a provisioned host is reviewable in the same diff as the code and cannot
// drift silently between workers.
//
// Same spirit as the CI-shape checks: a failure here means "the deployment
// contract is not being upheld", never "the code is wrong".
package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkerDeploymentsDeclareCatalogPath pins the geographic-catalog contract
// introduced when cwd/parent-directory discovery was removed: the worker now
// fails at startup without CHRONONTEMPLATE_CATALOG, so every deployment that
// boots it must supply the path explicitly. A unit or compose overlay that
// forgets it is a broken deployment, not a code bug, which is exactly why this
// lives with the other runtime-environment checks.
func TestWorkerDeploymentsDeclareCatalogPath(t *testing.T) {
	for _, unit := range []string{
		"renderinggen-worker.service",
		"renderinggen-worker-b.service",
	} {
		t.Run(unit, func(t *testing.T) {
			path := filepath.Join(RepoRoot(), "infra", "systemd", unit)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if !strings.Contains(string(raw), "Environment=CHRONONTEMPLATE_CATALOG=") {
				t.Errorf("%s must declare Environment=CHRONONTEMPLATE_CATALOG=...; startup validation rejects a worker without it", unit)
			}
		})
	}

	composePath := filepath.Join(RepoRoot(), "infra", "docker", "docker-compose.ci.yaml")
	raw, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read %s: %v", composePath, err)
	}
	compose := string(raw)
	if !strings.Contains(compose, "CHRONONTEMPLATE_CATALOG: /var/lib/renderinggen/catalog") {
		t.Error("the CI compose worker must set CHRONONTEMPLATE_CATALOG explicitly; without it the golden canary fails at startup validation")
	}
	if !strings.Contains(compose, "../../testdata/golden:/var/lib/renderinggen/catalog:ro") {
		t.Error("the CI compose worker must mount the checked-in Natural Earth fixture at CHRONONTEMPLATE_CATALOG")
	}
}

// TestWorkerUnitsProvideWritableWorkingDirectory pins the cwd contract that a
// bare `WorkingDirectory` removal silently broke in production: the worker
// renders from the plan's directory and resolves assets from config, so cwd is
// not a data path — but `chronon3d_cli doctor` probes filesystem.output_writable
// relative to cwd and exits non-zero from an unwritable one. A unit with no
// WorkingDirectory starts in / and crash-loops on the capability handshake.
//
// The requirement is therefore: declare a WorkingDirectory, and point it at a
// directory systemd itself creates (RuntimeDirectory/StateDirectory), so the
// unit stays machine-independent instead of naming a source checkout.
func TestWorkerUnitsProvideWritableWorkingDirectory(t *testing.T) {
	for _, unit := range []string{
		"renderinggen-worker.service",
		"renderinggen-worker-b.service",
	} {
		t.Run(unit, func(t *testing.T) {
			path := filepath.Join(RepoRoot(), "infra", "systemd", unit)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			workingDir := directiveValue(string(raw), "WorkingDirectory=")
			if workingDir == "" {
				t.Fatalf("%s must declare WorkingDirectory=; without it the service starts in / and chronon doctor fails filesystem.output_writable", unit)
			}
			ownedBySystemd := false
			for _, candidate := range []struct{ directive, prefix string }{
				{"RuntimeDirectory=", "/run/"},
				{"StateDirectory=", "/var/lib/"},
			} {
				value := directiveValue(string(raw), candidate.directive)
				if value != "" && workingDir == candidate.prefix+value {
					ownedBySystemd = true
				}
			}
			if !ownedBySystemd {
				t.Errorf("%s WorkingDirectory=%s must be a RuntimeDirectory=/StateDirectory= directory systemd creates, not a host-specific path", unit, workingDir)
			}
		})
	}
}

// directiveValue returns the value of the first line starting with directive,
// ignoring comment lines so prose that mentions a directive never satisfies it.
func directiveValue(text, directive string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, directive) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, directive))
		}
	}
	return ""
}

// TestSystemdUnitsDeclareGoMemoryPolicy requires every native Go service unit
// to pin GOGC and GOMEMLIMIT. The values themselves are operator-tunable
// (that is why they are Environment= lines with rationale comments and not
// baked into binaries); the REQUIREMENT is that each unit declares both, next
// to a comment explaining the cap, so a new unit never ships with an
// unbounded heap by omission.
func TestSystemdUnitsDeclareGoMemoryPolicy(t *testing.T) {
	units := []string{
		"renderinggen-worker.service",
		"renderinggen-worker-b.service",
		"renderinggen-queue.service",
	}
	for _, unit := range units {
		t.Run(unit, func(t *testing.T) {
			path := filepath.Join(RepoRoot(), "infra", "systemd", unit)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			text := string(raw)
			if !strings.Contains(text, "GOGC=") {
				t.Errorf("%s must declare GOGC= (explicit GC percentage next to the memory cap)", unit)
			}
			if !strings.Contains(text, "GOMEMLIMIT=") {
				t.Errorf("%s must declare GOMEMLIMIT= (soft heap cap; an undeclared limit means an unbounded heap)", unit)
			}
			// The declaration must be an Environment= assignment, not prose.
			if !strings.Contains(text, "Environment=GOGC=") {
				t.Errorf("%s must set the policy through an Environment=GOGC=... line", unit)
			}
		})
	}
}
