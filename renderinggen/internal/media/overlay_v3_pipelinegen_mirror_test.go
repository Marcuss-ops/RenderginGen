package media

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// siblingAssemblyContract locates the active PipelineGen checkout. The existing
// anti-drift gate requires a go.work file, but this workspace keeps the three
// repositories as siblings; walk up from this test and accept either layout.
func siblingAssemblyContract(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate test source")
	}
	dir := filepath.Dir(source)
	for {
		candidate := filepath.Join(dir, "refactored", "internal", "kernel", "media", "assembly_contract.go")
		if data, err := os.ReadFile(candidate); err == nil {
			return string(data)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("PipelineGen sibling checkout not found")
		}
		dir = parent
	}
}

func TestOverlayV3MirrorsPipelineGenCanonicalConstants(t *testing.T) {
	source := siblingAssemblyContract(t)
	hash := regexp.MustCompile(`CanonicalVideoExtradataSHA256\s*=\s*"([0-9a-f]{64})"`).FindStringSubmatch(source)
	if hash == nil {
		t.Fatal("PipelineGen does not declare CanonicalVideoExtradataSHA256")
	}
	id := regexp.MustCompile(`OverlayVideoOnlyAssemblyProfileID\s*=\s*"([^"]+)"`).FindStringSubmatch(source)
	if id == nil {
		t.Fatal("PipelineGen does not declare OverlayVideoOnlyAssemblyProfileID")
	}
	if id[1] != ProfileVeloxOverlayVideoOnlyV3 {
		t.Fatalf("PipelineGen V3 id = %q, RenderingGen = %q", id[1], ProfileVeloxOverlayVideoOnlyV3)
	}
	profile, err := ResolveProfile(ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatal(err)
	}
	if profile.CodecConfigSHA256 != hash[1] {
		t.Fatalf("V3 avcC = %q, PipelineGen canonical = %q", profile.CodecConfigSHA256, hash[1])
	}
	if profile.VideoTimeBaseNum != 1 || profile.VideoTimeBaseDen != 90000 {
		t.Fatalf("V3 timebase = %d/%d, want 1/90000", profile.VideoTimeBaseNum, profile.VideoTimeBaseDen)
	}
	if !strings.Contains(source, `OverlayVideoOnlyProfileID = "VELOX_OVERLAY_VIDEO_ONLY_V3"`) {
		t.Fatal("PipelineGen's final-job default must be the canonical V3 profile")
	}
	if !strings.Contains(source, `OverlayVideoOnlyPreviousID = "VELOX_OVERLAY_VIDEO_ONLY_V2"`) {
		t.Fatal("PipelineGen must keep V2 as the previous lane for legacy/cache compatibility")
	}
}
