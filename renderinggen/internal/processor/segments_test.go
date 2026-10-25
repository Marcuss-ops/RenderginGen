package processor

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// writeSegmentMP4 writes a real MP4 of `frames` frames at 10 fps into path
// (ffmpeg's software mux). It is the fake session's stand-in: the segmented
// mux writer produces one complete MP4 per item window, exactly like this.
func writeSegmentMP4(t *testing.T, path string, frames int) {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-y", "-v", "error",
		"-f", "lavfi", "-i", "color=c=blue:s=64x48:r=10",
		"-frames:v", strconv.Itoa(frames),
		"-c:v", "mpeg4", "-q:v", "5", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %s (%d frames): %v: %s", path, frames, err, out)
	}
}

// TestPlanSegmentOutputsRequiresAnExactTiling: the semantic item windows must
// tile [0, DurationFrames) exactly or the plan keeps the single-output path —
// segmentation must never silently change the content of an artifact. The
// output paths are deterministic so a retry always targets the same file.
func TestPlanSegmentOutputsRequiresAnExactTiling(t *testing.T) {
	dir := t.TempDir()
	windows := []overlay.ItemWindow{
		{ItemID: "a", StartFrame: 0, EndFrame: 8},
		{ItemID: "b", StartFrame: 8, EndFrame: 16},
		{ItemID: "c", StartFrame: 16, EndFrame: 24},
	}
	plan, ok := planSegmentOutputs(windows, 24, dir)
	if !ok {
		t.Fatal("exact tiling rejected")
	}
	if len(plan.Segments) != 3 || len(plan.ItemIDs) != 3 {
		t.Fatalf("segment plan has %d segments / %d item ids, want 3/3", len(plan.Segments), len(plan.ItemIDs))
	}
	for i, segment := range plan.Segments {
		if segment.OutputPath != segmentOutputPath(dir, i) {
			t.Fatalf("segment %d path = %q, want %q", i, segment.OutputPath, segmentOutputPath(dir, i))
		}
		if segment.StartFrame != windows[i].StartFrame || segment.EndFrame != windows[i].EndFrame {
			t.Fatalf("segment %d window = [%d,%d), want [%d,%d)", i, segment.StartFrame, segment.EndFrame, windows[i].StartFrame, windows[i].EndFrame)
		}
		if plan.ItemIDs[i] != windows[i].ItemID {
			t.Fatalf("segment %d item = %q, want %q", i, plan.ItemIDs[i], windows[i].ItemID)
		}
	}
	// Determinism: re-planning the same windows must target the same files.
	again, ok := planSegmentOutputs(windows, 24, dir)
	if !ok {
		t.Fatal("second planning rejected")
	}
	for i := range plan.Segments {
		if plan.Segments[i].OutputPath != again.Segments[i].OutputPath {
			t.Fatalf("segment %d path drifted: %q vs %q", i, plan.Segments[i].OutputPath, again.Segments[i].OutputPath)
		}
	}

	cases := []struct {
		name    string
		windows []overlay.ItemWindow
		total   int64
	}{
		{"single item", windows[:1], 24},
		{"no canvas", windows, 0},
		{"empty", nil, 24},
		{"lead-in", []overlay.ItemWindow{{ItemID: "a", StartFrame: 1, EndFrame: 9}, {ItemID: "b", StartFrame: 9, EndFrame: 24}}, 24},
		{"gap", []overlay.ItemWindow{{ItemID: "a", StartFrame: 0, EndFrame: 8}, {ItemID: "b", StartFrame: 9, EndFrame: 24}}, 24},
		{"overlap", []overlay.ItemWindow{{ItemID: "a", StartFrame: 0, EndFrame: 8}, {ItemID: "b", StartFrame: 7, EndFrame: 24}}, 24},
		{"zero-length window", []overlay.ItemWindow{{ItemID: "a", StartFrame: 0, EndFrame: 8}, {ItemID: "b", StartFrame: 8, EndFrame: 8}, {ItemID: "c", StartFrame: 8, EndFrame: 24}}, 24},
		{"short of the canvas", windows[:2], 24},
		{"past the canvas", []overlay.ItemWindow{{ItemID: "a", StartFrame: 0, EndFrame: 8}, {ItemID: "b", StartFrame: 8, EndFrame: 16}, {ItemID: "c", StartFrame: 16, EndFrame: 32}}, 24},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := planSegmentOutputs(tc.windows, tc.total, dir); ok {
				t.Fatalf("non-tileable plan accepted (%s)", tc.name)
			}
		})
	}
}

// TestBatchSegmentFactsProjectsTheVerifiedEntries: the queue's per-segment
// certification surface is a projection of the verified entries — no
// re-derivation that could drift from what was verified.
func TestBatchSegmentFactsProjectsTheVerifiedEntries(t *testing.T) {
	if batchSegmentFacts(nil) != nil {
		t.Fatal("empty entries must project to no facts")
	}
	entries := []SegmentArtifact{
		{ItemID: "a", OutputPath: "segment-000.mp4", SHA256: "aa", SizeBytes: 7, FrameCount: 8, DurationUS: 800000, Width: 64, Height: 48},
		{ItemID: "b", OutputPath: "segment-001.mp4", SHA256: "bb", SizeBytes: 9, Retried: true},
	}
	facts := batchSegmentFacts(entries)
	if len(facts) != 2 {
		t.Fatalf("facts = %d entries, want 2", len(facts))
	}
	if facts[0].ItemID != "a" || facts[0].SHA256 != "aa" || facts[0].SizeBytes != 7 || facts[0].FrameCount != 8 ||
		facts[0].DurationUS != 800000 || facts[0].Width != 64 || facts[0].Height != 48 || facts[0].Retried {
		t.Fatalf("facts[0] = %+v", facts[0])
	}
	if facts[1].ItemID != "b" || facts[1].SHA256 != "bb" || !facts[1].Retried {
		t.Fatalf("facts[1] = %+v", facts[1])
	}
}

// TestRetrySegmentRendersOnlyItsWindow: the single-segment retry re-renders ONE
// failed segment from the session's exact request narrowed to its frame
// window, into the same deterministic output path — never the whole batch.
func TestRetrySegmentRendersOnlyItsWindow(t *testing.T) {
	proc, _, renderer := newProcessor(t)
	prepared := &PreparedJob{baseRenderRequest: chronon.RenderRequest{
		PlanPath:    "plan.json",
		AssetsRoot:  "assets",
		OutputPath:  "mirror.mp4",
		TotalFrames: 24,
		Report:      true,
		OutputSegments: []chronon.OutputSegment{
			{OutputPath: "segment-000.mp4", StartFrame: 0, EndFrame: 8},
		},
	}}
	segment := chronon.OutputSegment{OutputPath: filepath.Join(t.TempDir(), "segment-001.mp4"), StartFrame: 8, EndFrame: 16}
	if err := proc.retrySegment(context.Background(), prepared, segment); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if renderer.calls != 1 {
		t.Fatalf("renders = %d, want exactly 1", renderer.calls)
	}
	req := renderer.req
	if req.OutputPath != segment.OutputPath {
		t.Fatalf("retry output = %q, want %q", req.OutputPath, segment.OutputPath)
	}
	if !req.RangeEnabled || req.FirstFrame != 8 || req.LastFrame != 15 || req.TotalFrames != 8 {
		t.Fatalf("retry window = range=%v [%d,%d] of %d, want [8,16) of 8", req.RangeEnabled, req.FirstFrame, req.LastFrame, req.TotalFrames)
	}
	if len(req.OutputSegments) != 0 {
		t.Fatalf("retry must not carry the session's per-item windows: %+v", req.OutputSegments)
	}
	if req.PlanPath != "plan.json" {
		t.Fatalf("retry must reuse the session's plan, got %q", req.PlanPath)
	}

	// A failing retry surfaces the window it could not repair.
	proc, _, renderer = newProcessor(t)
	renderer.err = context.DeadlineExceeded
	err := proc.retrySegment(context.Background(), prepared, segment)
	if err == nil {
		t.Fatal("retry render error swallowed")
	}
}

// TestFinalizeSegmentsRetriesOnlyTheFailedSegment: the per-segment
// verification cycle — verify every segment independently, re-render ONLY a
// failed segment once, store every verified segment under its own content
// address. A fake session has already written the three per-item MP4s; the
// middle one is corrupt (4 frames where its window wants 8).
func TestFinalizeSegmentsRetriesOnlyTheFailedSegment(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("ffprobe not available: %v", err)
	}
	proc, store, renderer := newProcessor(t)
	dir := t.TempDir()
	paths := []string{segmentOutputPath(dir, 0), segmentOutputPath(dir, 1), segmentOutputPath(dir, 2)}
	writeSegmentMP4(t, paths[0], 8)
	writeSegmentMP4(t, paths[1], 4) // fails its declared [8,16) window
	writeSegmentMP4(t, paths[2], 8)
	renderer.write = func(path string) error {
		writeSegmentMP4(t, path, 8)
		return nil
	}
	prepared := &PreparedJob{
		Job:     &queue.Job{ID: "job-segments"},
		Metrics: map[string]float64{},
		Segments: []chronon.OutputSegment{
			{OutputPath: paths[0], StartFrame: 0, EndFrame: 8},
			{OutputPath: paths[1], StartFrame: 8, EndFrame: 16},
			{OutputPath: paths[2], StartFrame: 16, EndFrame: 24},
		},
		SegmentItemIDs:    []string{"item-a", "item-b", "item-c"},
		baseRenderRequest: chronon.RenderRequest{PlanPath: "plan.json", OutputPath: filepath.Join(dir, "mirror.mp4"), TotalFrames: 24},
	}
	entries, err := proc.finalizeSegments(context.Background(), prepared)
	if err != nil {
		t.Fatalf("finalize segments: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if renderer.calls != 1 {
		t.Fatalf("only the failed segment must be re-rendered, got %d renders", renderer.calls)
	}
	retry := renderer.req
	if retry.OutputPath != paths[1] || !retry.RangeEnabled || retry.FirstFrame != 8 || retry.LastFrame != 15 || retry.TotalFrames != 8 {
		t.Fatalf("retry request = %q range=%v [%d,%d] of %d, want %q [8,16) of 8",
			retry.OutputPath, retry.RangeEnabled, retry.FirstFrame, retry.LastFrame, retry.TotalFrames, paths[1])
	}
	if len(retry.OutputSegments) != 0 {
		t.Fatalf("retry must not carry the session's per-item windows: %+v", retry.OutputSegments)
	}
	for i, entry := range entries {
		if entry.ItemID != prepared.SegmentItemIDs[i] {
			t.Fatalf("entry %d item = %q, want %q", i, entry.ItemID, prepared.SegmentItemIDs[i])
		}
		wantRetried := i == 1
		if entry.Retried != wantRetried {
			t.Fatalf("entry %d retried = %v, want %v", i, entry.Retried, wantRetried)
		}
		if entry.FrameCount != 8 {
			t.Fatalf("entry %d frames = %d, want 8", i, entry.FrameCount)
		}
		digest, size, err := hashio.File(paths[i])
		if err != nil {
			t.Fatalf("hash segment %d: %v", i, err)
		}
		if entry.SHA256 != digest || entry.SizeBytes != size {
			t.Fatalf("entry %d identity = %s/%d, want %s/%d", i, entry.SHA256, entry.SizeBytes, digest, size)
		}
		// Independent identity: the segment is stored under its own content
		// address, exactly like the single-output path stores its artifact.
		data, err := store.Get(context.Background(), entry.SHA256)
		if err != nil {
			t.Fatalf("stored segment %d: %v", i, err)
		}
		if int64(len(data)) != entry.SizeBytes {
			t.Fatalf("stored segment %d is %d bytes, want %d", i, len(data), entry.SizeBytes)
		}
	}
}
