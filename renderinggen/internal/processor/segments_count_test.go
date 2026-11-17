package processor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/testenv"
)

// segmentFrameCountIsCertified decides whether the per-segment gate still has
// to pay a full -count_frames decode. These cases pin the rule: a receipt that
// already proves the count skips it, and anything less keeps the decode.
func TestSegmentFrameCountIsCertified(t *testing.T) {
	probed := media.ProbeResult{FrameCount: 24}

	passed := chronon.MediaReceipt{}
	passed.Verification.FrameCount = "pass"

	timed := passed
	timedMS := 780.5
	timed.Timing.CountFramesMS = &timedMS

	workerPass := passed
	workerPass.Schema = canonicalOverlayReceiptSchema

	workerPassNoCount := chronon.MediaReceipt{Schema: canonicalOverlayReceiptSchema}
	workerPassNoCount.Verification.FrameCount = "pass"

	notPassed := chronon.MediaReceipt{Schema: canonicalOverlayReceiptSchema}
	notPassed.Verification.FrameCount = "skip"

	cases := []struct {
		name    string
		receipt chronon.MediaReceipt
		probed  media.ProbeResult
		want    bool
	}{
		{
			name:    "canonical worker receipt certifies the count it just encoded",
			receipt: workerPass,
			probed:  probed,
			want:    true,
		},
		{
			name:    "chronon receipt with a measured count_frames phase still certifies it",
			receipt: timed,
			probed:  probed,
			want:    true,
		},
		{
			name:    "frame_count pass without a measured phase on a non-canonical receipt falls back",
			receipt: passed,
			probed:  probed,
			want:    false,
		},
		{
			name:    "an empty probed count is never certified",
			receipt: workerPass,
			probed:  media.ProbeResult{},
			want:    false,
		},
		{
			name:    "a non-passing frame_count verdict is never certified",
			receipt: notPassed,
			probed:  probed,
			want:    false,
		},
		{
			name:    "a missing receipt is never certified",
			receipt: chronon.MediaReceipt{},
			probed:  probed,
			want:    false,
		},
	}
	for _, tc := range cases {
		if got := segmentFrameCountIsCertified(tc.receipt, nil, tc.probed); got != tc.want {
			t.Errorf("%s: certified = %t, want %t", tc.name, got, tc.want)
		}
	}
	if segmentFrameCountIsCertified(workerPass, context.Canceled, probed) {
		t.Error("a receipt read error must keep the full-decode fallback")
	}
}

// TestVerifySegmentOutputSkipsTheFullDecodeForACanonicalReceipt is the measured
// saving: once the canonical pass has emitted and probed a segment, the gate
// must not decode the whole file again just to re-derive its frame count (a
// 3.8 s scan for 19 s of 1080p24 on the development host, vs 0.25 s for the
// sampled decode that already traverses every packet).
func TestVerifySegmentOutputSkipsTheFullDecodeForACanonicalReceipt(t *testing.T) {
	testenv.RequireTools(t, "ffmpeg", "ffprobe")
	ctx := context.Background()
	proc, _, _ := newProcessor(t)
	output := filepath.Join(t.TempDir(), "segment-000.mp4")
	writeNonCanonicalMP4(t, output, 24)
	if changed, err := canonicalizeOutputMP4(ctx, output, media.ProfileVeloxOverlayVideoOnlyV3); err != nil {
		t.Fatalf("canonicalize fixture: %v", err)
	} else if !changed {
		t.Fatal("fixture was not canonicalized; the test cannot exercise the canonical receipt")
	}

	restore := countDecodedFrames
	t.Cleanup(func() { countDecodedFrames = restore })
	countDecodedFrames = func(context.Context, string) (int, error) {
		t.Fatal("the canonical receipt certifies the frame count; a full -count_frames decode is waste")
		return 0, nil
	}

	metadata := planMetadata{
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		FrameCount: 24, ProfileID: media.ProfileVeloxOverlayVideoOnlyV3,
	}
	entry, err := proc.verifySegmentOutput(ctx, "job-canonical-count", "item-0",
		chronon.OutputSegment{OutputPath: output, StartFrame: 0, EndFrame: 24},
		map[string]float64{}, metadata)
	if err != nil {
		t.Fatalf("verifySegmentOutput: %v", err)
	}
	if entry.FrameCount != 24 {
		t.Fatalf("FrameCount = %d, want the certified 24", entry.FrameCount)
	}
}
