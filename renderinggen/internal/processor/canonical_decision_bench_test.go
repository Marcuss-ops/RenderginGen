package processor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// BenchmarkCanonicalizeOutputDecision measures the two paths the canonical
// finalize can take for an artifact whose renderer receipt certifies the
// canonical avcC: the confirmation ffprobe the pass used to pay on every
// artifact, and the receipt check that replaced it. The delta is the
// per-artifact cost the single-session canonical lane removes from the post
// lane (TICKET-CANONICAL-X264-SINGLE-SESSION, §8).
func BenchmarkCanonicalizeOutputDecision(b *testing.B) {
	path := canonicalBenchFixture(b)
	ctx := context.Background()
	b.Run("source probe", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := media.ProbeFile(ctx, path); err != nil {
				b.Fatalf("probe: %v", err)
			}
		}
	})
	b.Run("receipt-certified", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			encoded, err := canonicalizeOutputMP4(ctx, path, media.ProfileVeloxOverlayVideoOnlyV3)
			if err != nil {
				b.Fatalf("canonicalize: %v", err)
			}
			if encoded {
				b.Fatal("a receipt-certified canonical artifact was re-encoded")
			}
		}
	})
}

// canonicalBenchFixture builds a 49-frame 1080p24 H.264 artifact and the
// renderer receipt that certifies the canonical avcC for it. The fixture's own
// avcC is irrelevant to the benchmark: it measures the decision cost for a
// receipt the renderer has already certified, not the encode.
func canonicalBenchFixture(b *testing.B) string {
	b.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		b.Skip("ffmpeg not available")
	}
	profile, err := media.ResolveProfile(media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		b.Fatalf("resolve V3: %v", err)
	}
	path := filepath.Join(b.TempDir(), "canonical-decision.mp4")
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24",
		"-frames:v", "49", "-c:v", "libx264", "-preset", "ultrafast",
		"-pix_fmt", "yuv420p", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		b.Skipf("ffmpeg cannot build the 1080p fixture: %v: %s", err, output)
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatalf("stat fixture: %v", err)
	}
	receipt := chronon.MediaReceipt{Schema: "chronon3d.render-receipt.v1"}
	receipt.Verification.Status = "pass"
	receipt.Output.SHA256 = strings.Repeat("d", 64)
	receipt.Output.Bytes = info.Size()
	receipt.Media.CodecConfigSHA256 = profile.CodecConfigSHA256
	data, err := json.Marshal(receipt)
	if err != nil {
		b.Fatalf("encode receipt: %v", err)
	}
	if err := os.WriteFile(path+chronon.MediaReceiptSuffix, data, 0o644); err != nil {
		b.Fatalf("write receipt: %v", err)
	}
	return path
}
