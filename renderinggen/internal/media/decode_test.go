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

func TestCountDecodedFrames(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Fatal(err)
	}
	count, err := CountDecodedFrames(context.Background(), goldenFixture(t, "background.mp4"))
	if err != nil || count <= 0 {
		t.Fatalf("full scan = %d, %v", count, err)
	}
	if _, err := CountDecodedFrames(context.Background(), filepath.Join(t.TempDir(), "missing.mp4")); err == nil {
		t.Fatal("missing stream accepted")
	}
}

func TestCountDecodedFramesRejectsDecoderDiagnostics(t *testing.T) {
	t.Cleanup(func() { Configure(Binaries{}) })
	tool := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf '8\\n'\nprintf 'corrupt frame\\n' >&2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	Configure(Binaries{FFprobe: tool})
	if _, err := CountDecodedFrames(context.Background(), "damaged.mp4"); err == nil || !strings.Contains(err.Error(), "corrupt frame") {
		t.Fatalf("decoder diagnostics accepted: %v", err)
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

// TestValidateDecodeSampledAcceptsARealArtifact pins the pass direction of the
// sampled gate on the checked-in fixture: real media passes all three
// deterministic passes (keyframe traversal, head, tail).
func TestValidateDecodeSampledAcceptsARealArtifact(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}
	path := goldenFixture(t, "background.mp4")
	if err := ValidateDecodeSampled(context.Background(), path); err != nil {
		t.Fatalf("a decodable artifact must pass the sampled gate: %v", err)
	}
}

// TestValidateDecodeSampledRejectsAnUndecodableFile pins the fail direction:
// anything that is not media fails the sampled gate, and a missing file fails
// rather than passing vacuously.
func TestValidateDecodeSampledRejectsAnUndecodableFile(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}
	dir := t.TempDir()
	notMedia := filepath.Join(dir, "not-media.mp4")
	if err := os.WriteFile(notMedia, []byte("this is not an MP4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDecodeSampled(context.Background(), notMedia); err == nil {
		t.Fatal("a file that is not media must fail the sampled decode gate")
	}
	if err := ValidateDecodeSampled(context.Background(), filepath.Join(dir, "absent.mp4")); err == nil {
		t.Fatal("a missing file must fail the sampled decode gate")
	}
}

// TestValidateDecodeSampledReportsTheConfiguredBinary pins the same tool seam
// as the complete decode: the gate resolves its binary through media.Configure
// and never silently falls back to PATH.
func TestValidateDecodeSampledReportsTheConfiguredBinary(t *testing.T) {
	t.Cleanup(func() { Configure(Binaries{}) })
	Configure(Binaries{FFmpeg: "/nonexistent/ffmpeg-for-test"})
	err := ValidateDecodeSampled(context.Background(), "whatever.mp4")
	if err == nil {
		t.Fatal("a configured-but-absent binary must fail the gate")
	}
	if !strings.Contains(err.Error(), "ffmpeg-for-test") {
		t.Fatalf("error %q does not name the configured binary", err)
	}
}
