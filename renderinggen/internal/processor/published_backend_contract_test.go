package processor

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// findWorkspaceRoot walks up from start until it finds the go.work that joins
// PipelineGen, RenderingGen and Chronon3d; ok=false when there is none.
func findWorkspaceRoot(start string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// TestPublishedBackendMatchesPipelineGenContract pins the one published
// identity value this worker emits to the enum that OWNS it. `chronon_vulkan`
// is a PipelineGen contract value (clip.render backend vocabulary) that the
// worker must project onto the artifact; the literal used to be re-derived
// inside publishedRenderBackend with no link back to its owner, so a rename in
// PipelineGen would have surfaced as an unknown backend on every downstream
// consumer instead of as a build/test failure here.
//
// When the sibling repository is not checked out (standalone RenderingGen
// checkout, this repository's own CI) the test SKIPS visibly: the contract is
// verified where the owner exists, and its absence is not reported as a pass.
func TestPublishedBackendMatchesPipelineGenContract(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate the test source; skipping cross-repo backend contract")
	}
	root, found := findWorkspaceRoot(filepath.Dir(source))
	if !found {
		t.Skip("go.work / sibling repositories not checked out; skipping cross-repo backend contract (standalone RenderingGen checkout)")
	}
	backendFile := filepath.Join(root, "refactored", "internal", "capabilities", "cliprender", "backend.go")
	raw, err := os.ReadFile(backendFile)
	if err != nil {
		t.Skipf("PipelineGen backend vocabulary not checked out (%v); skipping", err)
	}
	decl := regexp.MustCompile(`BackendChrononVulkan\s+RenderBackend\s*=\s*"([^"]+)"`)
	match := decl.FindSubmatch(raw)
	if match == nil {
		t.Fatalf("%s no longer declares BackendChrononVulkan; the published backend identity is now owned elsewhere — re-point this pin", backendFile)
	}
	if got := string(match[1]); got != PublishedBackendChrononVulkan {
		t.Fatalf("published backend identity drift: the worker emits %q, PipelineGen's contract declares %q", PublishedBackendChrononVulkan, got)
	}
}

// TestPublishedBackendRequiresTheStrictNativeGate pins the rule the identity
// carries: `chronon_vulkan` is only ever published for a render that passed
// the strict native gate, and the internal configuration name ("vulkan") is
// never published as-is.
func TestPublishedBackendRequiresTheStrictNativeGate(t *testing.T) {
	cases := []struct {
		backend         string
		nativeCertified bool
		wantPublished   string
	}{
		{backend: "vulkan", nativeCertified: true, wantPublished: PublishedBackendChrononVulkan},
		{backend: "vulkan", nativeCertified: false, wantPublished: "vulkan"},
		{backend: "software", nativeCertified: true, wantPublished: "software"},
		{backend: "software", nativeCertified: false, wantPublished: "software"},
	}
	for _, tc := range cases {
		if got := publishedRenderBackend(tc.backend, tc.nativeCertified); got != tc.wantPublished {
			t.Errorf("publishedRenderBackend(%q, %v) = %q, want %q", tc.backend, tc.nativeCertified, got, tc.wantPublished)
		}
	}
}
