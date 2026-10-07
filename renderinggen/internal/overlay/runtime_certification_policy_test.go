// runtime_certification_policy_test.go pins the runtime-certification OPT-OUT
// contract in the DEFAULT test build: it must not move behind the
// certification tag, or CI would stop proving that mere presence of an engine
// binary is not accepted as consent to run real renders.

package overlay

import (
	"os"
	"testing"
)

// TestRuntimeCertificationOptOut pins the two switches that keep a normal test
// run from turning into minutes of real GPU renders plus pixel comparison:
//
//  1. the OPT-OUT env var, which `make test-unit` and CI set, and
//  2. the opt-IN requirement itself: with no CHRONON_BIN naming an engine, the
//     suite does not run.
//     That is the direction that used to be enforced by DISCOVERY of a build
//     beside this repository — which is exactly how a bare `go test ./...`
//     started rendering video on a development machine, and how it hung on a host
//     without a usable GPU. Pinning it here means removing that requirement is a
//     test failure, not a silent regression.
//
// Each switch is asserted, not described. A skipped subtest never reaches the
// statement after the guard, so the marker stays false — which is exactly what
// "it skipped" means. The last case pins the other direction: an EMPTY opt-out
// must not disable the suite (a stray empty variable cannot silently drop the
// certification), which is why the guard tests for non-empty instead of mere
// presence.
func TestRuntimeCertificationOptOut(t *testing.T) {
	t.Run("a non-empty value skips the suite", func(t *testing.T) {
		t.Setenv(skipRuntimeCertificationEnv, "1")
		proceeded := false
		t.Run("guarded test", func(t *testing.T) {
			chrononBinFor(t)
			proceeded = true
		})
		if proceeded {
			t.Fatalf("%s=1 did not skip a test that asks for the real engine", skipRuntimeCertificationEnv)
		}
	})

	t.Run("no engine named means no real render", func(t *testing.T) {
		t.Setenv(skipRuntimeCertificationEnv, "")
		t.Setenv(engineBinEnv, "")
		proceeded := false
		t.Run("guarded test", func(t *testing.T) {
			chrononBinFor(t)
			proceeded = true
		})
		if proceeded {
			t.Fatalf("the real-engine suite ran without %s being set; a discovered build beside this repository must not be enough", engineBinEnv)
		}
	})

	t.Run("an empty opt-out with an engine named leaves the suite enabled", func(t *testing.T) {
		exe, err := os.Executable()
		if err != nil {
			t.Skipf("cannot resolve the test binary path: %v", err)
		}
		t.Setenv(skipRuntimeCertificationEnv, "")
		t.Setenv(engineBinEnv, exe)
		returned := ""
		t.Run("guarded test", func(t *testing.T) {
			returned = chrononBinFor(t)
		})
		if returned != exe {
			t.Fatalf("an empty %s disabled the suite: chrononBinFor returned %q, want the %s path %q", skipRuntimeCertificationEnv, returned, engineBinEnv, exe)
		}
	})
}
