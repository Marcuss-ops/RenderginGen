package processor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// Benchmark baseline, measured 2026-11-16 on the dev host (quiet host,
// software compositor) with a 49-frame 1080p24 timeline: NVENC 4.88 s plus
// canonical post-pass 0.93 s, against 39.86 s for the single-session x264 lane.
// The post-pass budget below is the measured 930 ms plus 20%, as agreed in
// RG-AUDIT-13; it is a host-calibrated number, never a generic CI expectation.
const (
	canonicalPostPassBaselineMS = 930
	canonicalPostPassBudgetPct  = 20
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
func TestCanonicalDecisionGuard(t *testing.T) {
	path := canonicalBenchFixture(t)
	ctx := context.Background()
	before, err := media.ProbeFile(ctx, path)
	if err != nil {
		t.Fatalf("probe fixture: %v", err)
	}
	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := canonicalizeOutputMP4(ctx, path, media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if encoded {
		t.Fatal("a receipt-certified canonical artifact was re-encoded")
	}
	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if infoBefore.Size() != infoAfter.Size() {
		t.Fatalf("artifact size changed on a skip: %d -> %d", infoBefore.Size(), infoAfter.Size())
	}
	after, err := media.ProbeFile(ctx, path)
	if err != nil {
		t.Fatalf("probe after decision: %v", err)
	}
	if after.FrameCount != before.FrameCount || after.DurationUS != before.DurationUS {
		t.Fatalf("decision changed the timeline: frames %d -> %d, duration_us %d -> %d",
			before.FrameCount, after.FrameCount, before.DurationUS, after.DurationUS)
	}
	if after.FrameCount != 49 || after.FPSNum != 24 || after.FPSDen != 1 {
		t.Fatalf("fixture timeline drifted: %d frames at %d/%d fps", after.FrameCount, after.FPSNum, after.FPSDen)
	}
}

// TestCanonicalPostPassBudget is the wall-time guard for the CPU post-pass.
// It is opt-in because the baseline belongs to a named host: run it with
// CANONICAL_GUARD_STRICT=1 on that host (2026-11-16, 49-frame 1080p24), where
// the agreed budget is 930 ms + 20% = 1116 ms. The default suite still runs
// the decision/frame/duration guard above, which is host-independent.
func TestCanonicalPostPassBudget(t *testing.T) {
	if os.Getenv("CANONICAL_GUARD_STRICT") != "1" {
		t.Skip("set CANONICAL_GUARD_STRICT=1 on the calibrated host to enforce the measured post-pass budget")
	}
	path := canonicalBenchFixture(t)
	if err := os.Remove(path + chronon.MediaReceiptSuffix); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := time.Now()
	encoded, err := canonicalizeOutputMP4(ctx, path, media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if !encoded {
		t.Fatal("an artifact without a canonical receipt was not re-encoded")
	}
	wallMS := float64(time.Since(start).Microseconds()) / 1000
	budgetMS := float64(canonicalPostPassBaselineMS) * (1 + float64(canonicalPostPassBudgetPct)/100)
	if wallMS > budgetMS {
		t.Fatalf("canonical post-pass took %.0f ms, above the %.0f ms budget (baseline %d ms + %d%%, 2026-11-16 dev host)",
			wallMS, budgetMS, canonicalPostPassBaselineMS, canonicalPostPassBudgetPct)
	}
	t.Logf("canonical post-pass wall %.0f ms, budget %.0f ms", wallMS, budgetMS)
}

func canonicalBenchFixture(b testing.TB) string {
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
