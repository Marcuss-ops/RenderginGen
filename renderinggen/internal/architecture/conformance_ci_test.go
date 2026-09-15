// conformance_ci_test.go covers the CI SHAPE: that the workflows actually run the
// module tests with the race detector, that the runtime certification has an
// owner that points the engine at the binary it certifies, and that no job
// silently checks out a sibling repository (which would make the gate pass for
// the wrong reason).
//
// These are checks about the repository's process rather than about the product's
// boundary, which is why they live apart from the rule tests: a failure here
// means "the gate is not being run as intended", never "the code is wrong".
package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIRunsRaceEnabledModuleTests(t *testing.T) {
	path := filepath.Join(RepoRoot(), ".github", "workflows", "build.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	workflow := string(raw)
	jobs := map[string]string{ // job name -> expected working directory
		"test-renderinggen": "renderinggen",
		"test-queue":        "queue",
		"test-objectstore":  "objectstore",
	}
	for job, dir := range jobs {
		block := ciJobBlock(t, workflow, job)
		if strings.TrimSpace(block) == "" {
			t.Errorf("CI job %q is gone; the module it gates is untested", job)
			continue
		}
		if !strings.Contains(block, "working-directory: "+dir) {
			t.Errorf("CI job %q no longer runs in %s/", job, dir)
		}
		if job == "test-renderinggen" {
			// The scanner is excluded from the instrumented run and must be run
			// separately, un-instrumented.
			if !strings.Contains(block, "go test -race -count=1 $(go list ./... | grep -v '/internal/architecture$')") {
				t.Errorf("CI job %q must run the concurrency-bearing packages under `-race -count=1`, excluding only internal/architecture", job)
			}
			if !strings.Contains(block, "go test -count=1 ./internal/architecture/...") {
				t.Errorf("CI job %q excludes internal/architecture from -race, so it must still run the gate in its own step", job)
			}
			if !strings.Contains(block, "RENDERINGGEN_SKIP_GPU_E2E=1") {
				t.Errorf("CI job %q must disable the real-engine runtime certification suite explicitly, so the unit job cannot start real renders", job)
			}
			continue
		}
		if !strings.Contains(block, "go test -race -count=1 ./...") {
			t.Errorf("CI job %q must run `go test -race -count=1 ./...`", job)
		}
	}
}

// TestCIRuntimeCertificationHasAnOwner pins the third leg of the test-tier
// decision. The unit jobs disable the real-engine certification suite
// (RENDERINGGEN_SKIP_GPU_E2E=1), which is correct — but it also means that
// without this job the suite that actually renders frames would have no owner
// anywhere: it would run only where a developer happened to have an engine, and
// nothing would say so. The job must do three things, all asserted here:
//
//  1. invoke the certification tests;
//  2. NOT set the skip variable (a job that sets it and "runs" the suite is the
//     false green this test exists to prevent);
//  3. be manual (workflow_dispatch) on a GPU runner, because the hosted runner
//     has no device and an unguarded job would fail on every push.
func TestCIRuntimeCertificationHasAnOwner(t *testing.T) {
	path := filepath.Join(RepoRoot(), ".github", "workflows", "build.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	workflow := string(raw)

	job := "runtime-certification"
	if !strings.Contains(workflow, "\n  "+job+":") {
		t.Fatalf("CI no longer declares the %q job: the unit jobs disable the runtime certification, so nothing would run it", job)
	}
	block := ciJobBlock(t, workflow, job)
	if !strings.Contains(block, "./internal/overlay/") {
		t.Errorf("CI job %q must run the certification suite under ./internal/overlay/", job)
	}
	if strings.Contains(block, "RENDERINGGEN_SKIP_GPU_E2E") {
		t.Errorf("CI job %q sets RENDERINGGEN_SKIP_GPU_E2E, so it would report a green run in which every certification test skipped", job)
	}
	if !strings.Contains(block, "CHRONON_BIN") {
		t.Errorf("CI job %q must point CHRONON_BIN at the engine it certifies", job)
	}
	if !strings.Contains(block, "workflow_dispatch") {
		t.Errorf("CI job %q must be manual: the hosted runners have no GPU", job)
	}
	if !strings.Contains(block, "self-hosted") {
		t.Errorf("CI job %q must select a GPU runner", job)
	}
}

// ciJobBlock slices one CI job out of a workflow by indentation. A job header is
// a line indented exactly two spaces ending in ':'; its body is everything up to
// the next line at the same indent depth (or a top-level key). Slicing on the raw
// text instead matched the first "\n  " of the four-space body indent and
// returned an empty block.
func ciJobBlock(t *testing.T, workflow, job string) string {
	t.Helper()
	var sb strings.Builder
	in := false
	for _, line := range strings.Split(workflow, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == line && trimmed != "" {
			in = false // top-level key ends any job body
			continue
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			in = trimmed == job+":"
			continue
		}
		if in {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// TestCIChecksOutNoSiblingRepository pins the other half of the scope table in
// CONFORMANCE.md: the workflow checks out THIS repository only, so cross-repo
// rules are not exercised in CI. The golden canary legitimately clones
// Chronon3d for the runtime image, but into a temporary directory — Targets()
// only looks beside the checkout, so that clone must never land in the
// workspace. If a sibling checkout is ever added here, the scope table and this
// test must change together, which is the point: the limitation is recorded,
// not discovered.
func TestCIChecksOutNoSiblingRepository(t *testing.T) {
	path := filepath.Join(RepoRoot(), ".github", "workflows", "build.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "repository:") {
			t.Errorf("build.yaml:%d adds a `repository:` input (%s); a sibling checkout changes the gate's enforced scope and CONFORMANCE.md", i+1, strings.TrimSpace(line))
		}
		if !strings.Contains(line, "clone") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		dest := fields[len(fields)-1]
		if strings.HasPrefix(dest, "/tmp/") || strings.Contains(dest, "runner.temp") {
			continue
		}
		if strings.Contains(strings.ToLower(dest), "chronon") || strings.Contains(strings.ToLower(dest), "refactored") {
			t.Errorf("build.yaml:%d clones a sibling into %q, which Targets() would scan: cross-repo enforcement is no longer local-only", i+1, dest)
		}
	}
}
