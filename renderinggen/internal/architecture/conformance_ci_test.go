// conformance_ci_test.go covers the CI SHAPE: that the workflows actually run the
// module tests with the race detector, that the real-engine certification has an
// explicit local owner instead of a dormant CI job, and that no job silently
// checks out a sibling repository (which would make the gate pass for the wrong
// reason).
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
			// The certification suites are behind //go:build certification, so the
			// -race step above cannot see them. That is the intended split, but a
			// tag with no runner is a silent coverage cut: CI must run them in
			// their own step, and the local test-certification lane runs them with
			// a real engine.
			if !strings.Contains(block, "-tags certification") {
				t.Errorf("CI job %q must run the //go:build certification suites in their own step: the tagged files are invisible to the -race step, so without a runner the tag silently removes the certification coverage from CI", job)
			}
			continue
		}
		if !strings.Contains(block, "go test -race -count=1 ./...") {
			t.Errorf("CI job %q must run `go test -race -count=1 ./...`", job)
		}
	}
}

// TestRuntimeCertificationHasAnOwner pins the decision taken on 2026-11-24: the
// real-engine certification suite is not a CI job because this repository has no
// self-hosted GPU runner. Its owner is the documented local operator lane, which
// must stay runnable from a checkout and must not set the skip variable. A
// re-added `runtime-certification` job is a decision change: restore its
// five-property contract here instead of silently dropping the local lane.
func TestRuntimeCertificationHasAnOwner(t *testing.T) {
	workflowPath := filepath.Join(RepoRoot(), ".github", "workflows", "build.yaml")
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}
	workflow := string(raw)
	if strings.Contains(workflow, "\n  runtime-certification:") {
		t.Fatal("CI declares runtime-certification again: provision the gpu runner and restore the full job contract, or remove the job and keep the local lane as the only owner")
	}
	if strings.Contains(workflow, "inputs.certification") {
		t.Error("CI still references inputs.certification after the GPU job was retired")
	}

	makefilePath := filepath.Join(RepoRoot(), "Makefile")
	makefileRaw, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read %s: %v", makefilePath, err)
	}
	block := ciMakeTargetBlock(t, string(makefileRaw), "test-certification")
	if strings.TrimSpace(block) == "" {
		t.Fatal("the Makefile no longer declares the test-certification target: the real-engine suite would have no local entry point")
	}
	if !strings.Contains(block, "-tags certification") {
		t.Error("test-certification must pass -tags certification: the certification suites live behind that build tag")
	}
	if !strings.Contains(block, "./internal/overlay/") {
		t.Error("test-certification must run the certification suite under ./internal/overlay/")
	}
	if strings.Contains(block, "RENDERINGGEN_SKIP_GPU_E2E") {
		t.Error("test-certification sets RENDERINGGEN_SKIP_GPU_E2E, so it would report a green run in which every real-engine test skipped")
	}

	readmePath := filepath.Join(RepoRoot(), "README.md")
	readmeRaw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read %s: %v", readmePath, err)
	}
	command := "CHRONON_BIN=/path/to/chronon3d_cli make test-certification"
	if !strings.Contains(string(readmeRaw), command) {
		t.Errorf("README must document the local certification command %q", command)
	}
}

// ciMakeTargetBlock slices one Makefile target by its unindented header. The
// body includes the tab-indented recipe and stops at the next unindented line
// (the next target, variable or top-level comment).
func ciMakeTargetBlock(t *testing.T, makefile, target string) string {
	t.Helper()
	var sb strings.Builder
	in := false
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, target+":") {
			in = true
			sb.WriteString(line)
			sb.WriteString("\n")
			continue
		}
		if in {
			if line == "" || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") {
				sb.WriteString(line)
				sb.WriteString("\n")
				continue
			}
			break
		}
	}
	return sb.String()
}

// TestCIPublishRequiresAggregateVerify pins the release-integrity gate: no
// publish-* job may depend only on its own service test job, because a red
// renderinggen then does not stop queue/objectstore from shipping artifacts
// from a revision the repository as a whole considers broken. Every publish
// job needs the aggregate `verify` job, which itself needs all three unit
// suites green on the same commit.
func TestCIPublishRequiresAggregateVerify(t *testing.T) {
	path := filepath.Join(RepoRoot(), ".github", "workflows", "build.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	workflow := string(raw)
	verify := ciJobBlock(t, workflow, "verify")
	if strings.TrimSpace(verify) == "" {
		t.Fatal("CI no longer declares the aggregate `verify` job: publish jobs would fall back to per-service gates and ship from a globally red revision")
	}
	for _, job := range []string{"test-renderinggen", "test-queue", "test-objectstore"} {
		if !strings.Contains(verify, job) {
			t.Errorf("CI `verify` gate does not need %q: a red %s would no longer block publish", job, job)
		}
	}
	for _, job := range []string{"publish-worker", "publish-queue", "publish-objectstore"} {
		block := ciJobBlock(t, workflow, job)
		if strings.TrimSpace(block) == "" {
			t.Errorf("CI job %q is gone", job)
			continue
		}
		if !strings.Contains(block, "needs: verify") {
			t.Errorf("CI job %q must need the aggregate `verify` gate, not a single service test job", job)
		}
	}
	if strings.Contains(workflow, "IMAGE_BASE }}/renderinggen-worker:${{ env.VERSION }}\n") ||
		strings.Contains(workflow, "IMAGE_BASE }}/queue:${{ env.VERSION }}\n") ||
		strings.Contains(workflow, "IMAGE_BASE }}/objectstore:${{ env.VERSION }}\n") {
		t.Error("CI pushes a bare ${VERSION} tag: VERSION stays 0.1.0 across pushes, so the tag would name different bytes over time; publish SHA-qualified tags only")
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
