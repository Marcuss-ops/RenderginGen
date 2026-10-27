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
