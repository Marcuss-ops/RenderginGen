package processor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
)

// TestProbeFactsPreferTheCanonicalReceipt pins the rule that removes the
// duplicate verification from the hot path: Chronon already read the artifact's
// bytes while encoding, so the worker must take the media facts from the receipt
// and must NOT spawn its own ffprobe (~250 ms per job measured on the 100-overlay
// matrix).
//
// The proof is that a canonical receipt makes the path succeed with a file that
// does not exist: only the receipt branch can do that.
func TestProbeFactsPreferTheCanonicalReceipt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-rendered.mp4")
	receipt := canonicalReceipt()

	probed, source, err := probeFactsForFinalize(context.Background(), receipt, nil, missing)
	if err != nil {
		t.Fatalf("a canonical receipt must describe the media without touching the file: %v", err)
	}
	if source != probeSourceReceipt {
		t.Fatalf("probe source = %q, want %q", source, probeSourceReceipt)
	}
	if probed.Width != receipt.Media.Width || probed.Height != receipt.Media.Height {
		t.Fatalf("probed %dx%d, want the receipt's %dx%d", probed.Width, probed.Height, receipt.Media.Width, receipt.Media.Height)
	}
	if probed.FrameCount != int(receipt.Media.FrameCount) {
		t.Fatalf("frame count = %d, want the receipt's %d", probed.FrameCount, receipt.Media.FrameCount)
	}
}

// TestProbeFactsFallBackToALocalProbe pins the other half: the fallback is real
// and it is the ONLY path that inspects the artifact (the audit/debug route), so
// a renderer that stops writing receipts cannot silently produce uncertified
// artifacts.
func TestProbeFactsFallBackToALocalProbe(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-rendered.mp4")

	// No receipt at all, and a receipt that cannot describe the media: both take
	// the local path. The file is missing, so the probe fails — which is the
	// point: the fallback really did try to inspect it.
	verificationOnly := chronon.MediaReceipt{}
	verificationOnly.Verification.Status = "pass" // a verification block with no media facts is not canonical
	cases := []struct {
		name    string
		receipt chronon.MediaReceipt
		err     error
	}{
		{"absent receipt", chronon.MediaReceipt{}, nil},
		{"unreadable receipt", chronon.MediaReceipt{}, errReadFailed{}},
		{"receipt without canonical media", verificationOnly, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, source, err := probeFactsForFinalize(context.Background(), tc.receipt, tc.err, missing)
			if source != probeSourceLocalProbe {
				t.Fatalf("probe source = %q, want %q", source, probeSourceLocalProbe)
			}
			if err == nil {
				t.Fatal("the local fallback must actually probe the file; a missing file must fail")
			}
			if !strings.Contains(err.Error(), "ffprobe") {
				t.Fatalf("error = %v, want an ffprobe failure", err)
			}
		})
	}
}

type errReadFailed struct{}

func (errReadFailed) Error() string { return "receipt read failed" }

// canonicalReceipt is a receipt carrying the full structural media fact set the
// engine records for a render.
func canonicalReceipt() chronon.MediaReceipt {
	receipt := chronon.MediaReceipt{}
	receipt.Media.HasVideo = true
	receipt.Media.Container = "mp4"
	receipt.Media.VideoCodec = "h264"
	receipt.Media.PixelFormat = "yuv420p"
	receipt.Media.Width = 1920
	receipt.Media.Height = 1080
	receipt.Media.FPSNum = 24
	receipt.Media.FPSDen = 1
	receipt.Media.FrameCount = 120
	receipt.Media.DurationUS = 5_000_000
	return receipt
}
