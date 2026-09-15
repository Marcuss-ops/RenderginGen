package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateDecodeAcceptsARealArtifact pins the pass direction on the checked-in
// fixture, and that the gate uses the CONFIGURED binary (a wrapper path must be
// honoured here exactly as in the probe paths).
func TestValidateDecodeAcceptsARealArtifact(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}
	path := goldenFixture(t, "background.mp4")
	if err := ValidateDecode(context.Background(), path); err != nil {
		t.Fatalf("a decodable artifact must pass: %v", err)
	}
}

// TestValidateDecodeRejectsAnUndecodableFile pins the fail direction: a file that
// is not media at all must be reported with the tool's own diagnostics, and a
// missing file must fail rather than pass vacuously.
func TestValidateDecodeRejectsAnUndecodableFile(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}
	dir := t.TempDir()
	notMedia := filepath.Join(dir, "not-media.mp4")
	if err := os.WriteFile(notMedia, []byte("this is not an MP4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDecode(context.Background(), notMedia); err == nil {
		t.Fatal("a file that is not media must fail the decode gate")
	}
	if err := ValidateDecode(context.Background(), filepath.Join(dir, "absent.mp4")); err == nil {
		t.Fatal("a missing file must fail the decode gate")
	}
}

// TestValidateDecodeReportsTheConfiguredBinary pins that the gate resolves its
// tool through the same single point as the probe: pointing it at a binary that
// cannot exist turns every call into a failure instead of silently falling back
// to PATH.
func TestValidateDecodeReportsTheConfiguredBinary(t *testing.T) {
	t.Cleanup(func() { Configure(Binaries{}) })
	Configure(Binaries{FFmpeg: "/nonexistent/ffmpeg-for-test"})
	err := ValidateDecode(context.Background(), "whatever.mp4")
	if err == nil {
		t.Fatal("a configured-but-absent binary must fail the gate")
	}
	if !strings.Contains(err.Error(), "ffmpeg-for-test") {
		t.Fatalf("error %q does not name the configured binary", err)
	}
}
