// segments.go owns the P4 batch-superjob output segmentation
// (docs/operations/TICKET-BATCH-SUPERJOB.md): one render session produces ONE
// MP4 per semantic item ("un piano = una sessione render"), and every segment
// keeps its own identity, verification and retry.
//
// The MP4-per-item contract with the final worker is preserved: each segment
// is a complete, independently fetchable artifact. What changes is only the
// render-session lifecycle — one startup, one encoder session, one EOF for the
// whole batch — instead of one open/close per artifact.
//
// Window rule: the semantic item windows must TILE the canvas exactly
// ([0, DurationFrames) with no gaps and no overlaps). Then segment k is item
// k's own frame window, PTS-zeroed by the segmented mux writer — exactly the
// frames the non-fused single-item path renders for that item, which is what
// makes per-segment parity against reference runs meaningful. A plan whose
// items do not tile (gaps, overlaps, lead-in) is NOT segmentable and keeps the
// historical single-output behavior: segmentation must never silently change
// the content of an artifact.
package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
)

// SegmentPlan is the worker-side plan for a segmented batch render: one
// output window per semantic item, in plan order.
type SegmentPlan struct {
	Segments []chronon.OutputSegment
	ItemIDs  []string // parallel to Segments
}

// planSegmentOutputs projects the compile pass's per-item frame windows onto
// the segmented-mux contract. It returns ok=false whenever the plan is not
// exactly tileable (fewer than two items, gaps, overlaps, out-of-order
// windows, or windows that do not cover the full canvas): the caller then
// keeps the single-output path, so segmentation can never rewrite the
// semantics of an existing plan.
//
// The output paths are deterministic (segment-<index>.mp4 under outputDir):
// a retry of the same segment always targets the same file, which is what
// makes the single-segment re-render idempotent.
func planSegmentOutputs(windows []overlay.ItemWindow, totalFrames int64, outputDir string) (SegmentPlan, bool) {
	var plan SegmentPlan
	if len(windows) < 2 || totalFrames <= 0 {
		return plan, false
	}
	expected := int64(0)
	for _, window := range windows {
		if window.StartFrame != expected || window.EndFrame <= window.StartFrame {
			return SegmentPlan{}, false
		}
		expected = window.EndFrame
	}
	if expected != totalFrames {
		return SegmentPlan{}, false
	}
	plan.Segments = make([]chronon.OutputSegment, 0, len(windows))
	plan.ItemIDs = make([]string, 0, len(windows))
	for _, window := range windows {
		plan.Segments = append(plan.Segments, chronon.OutputSegment{
			OutputPath: segmentOutputPath(outputDir, len(plan.Segments)),
			StartFrame: window.StartFrame,
			EndFrame:   window.EndFrame,
		})
		plan.ItemIDs = append(plan.ItemIDs, window.ItemID)
	}
	return plan, true
}

func segmentOutputPath(outputDir string, index int) string {
	return filepath.Join(outputDir, fmt.Sprintf("segment-%03d.mp4", index))
}

// SegmentArtifact is one verified segment's independent identity: its own
// content address, its own probe facts and its own receipt verdict. The
// batch's OutputFacts lists these entries (queue.BatchSegmentFacts — "receipt
// per batch + voci per segmento"), so every MP4 stays independently
// fetchable, verifiable and retryable.
type SegmentArtifact struct {
	ItemID     string `json:"item_id"`
	OutputPath string `json:"output_path"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	FrameCount int    `json:"frame_count,omitempty"`
	DurationUS int64  `json:"duration_us,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Retried    bool   `json:"retried,omitempty"`
}

// finalizeSegments runs the per-segment verification cycle of the P4 batch
// superjob: verify every segment independently (receipt identity gate +
// structural probe + decoded-frame count against its declared window),
// re-render ONLY a failed segment once (idempotent single-segment retry — the
// other segments and the session outputs are never touched), and store every
// verified segment under its own content address.
//
// The returned entries become the batch artifact's per-segment facts
// (OutputFacts.BatchSegments — "receipt per batch + voci per segmento"). The
// batch's own fused clip (the session's mirror output) keeps flowing through
// the ordinary single-artifact path untouched.
func (p *Processor) finalizeSegments(ctx context.Context, prepared *PreparedJob) ([]SegmentArtifact, error) {
	job := prepared.Job
	metrics := prepared.Metrics
	segments := prepared.Segments
	itemIDs := prepared.SegmentItemIDs

	entries := make([]SegmentArtifact, 0, len(segments))
	for i, segment := range segments {
		itemID := ""
		if i < len(itemIDs) {
			itemID = itemIDs[i]
		}
		entry, err := p.verifySegmentOutput(ctx, job.ID, itemID, segment, metrics)
		if err != nil {
			// Idempotent per-segment retry: one failed segment must not
			// invalidate the batch or the segments that already verified.
			workerlog.ByJobID(job.ID).Warnf("segment %d (%s) failed verification (%v); re-rendering only this segment", i, itemID, err)
			if retryErr := p.retrySegment(ctx, prepared, segment); retryErr != nil {
				return nil, fmt.Errorf("processor: segment %d (%s) failed verification (%v) and its single-segment retry failed: %w", i, itemID, err, retryErr)
			}
			entry, err = p.verifySegmentOutput(ctx, job.ID, itemID, segment, metrics)
			if err != nil {
				return nil, fmt.Errorf("processor: segment %d (%s) still fails verification after its single-segment retry: %w", i, itemID, err)
			}
			entry.Retried = true
		}
		// Independent identity: every segment is stored under its own content
		// address, exactly like the single-output path stores its artifact.
		if err := p.storeSegment(ctx, job.ID, entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// batchSegmentFacts projects the worker's verified segment entries onto the
// queue's per-segment certification surface (OutputFacts.BatchSegments).
func batchSegmentFacts(entries []SegmentArtifact) []queue.BatchSegmentFacts {
	if len(entries) == 0 {
		return nil
	}
	facts := make([]queue.BatchSegmentFacts, 0, len(entries))
	for _, entry := range entries {
		facts = append(facts, queue.BatchSegmentFacts{
			ItemID:     entry.ItemID,
			SHA256:     entry.SHA256,
			SizeBytes:  entry.SizeBytes,
			FrameCount: entry.FrameCount,
			DurationUS: entry.DurationUS,
			Width:      entry.Width,
			Height:     entry.Height,
			Retried:    entry.Retried,
		})
	}
	return facts
}

// verifySegmentOutput applies the per-segment verification cycle
// ("probe/digest per segmento"): the canonical receipt-identity gate (the same
// gate the single-output path runs) plus a structural probe. It returns the
// segment's independent identity on success.
func (p *Processor) verifySegmentOutput(ctx context.Context, jobID, itemID string, segment chronon.OutputSegment, metrics map[string]float64) (SegmentArtifact, error) {
	receipt, receiptErr := chronon.ReadMediaReceipt(segment.OutputPath)
	outcome, err := p.verifyArtifactReceipt(ctx, segment.OutputPath, receipt, receiptErr, metrics, jobID)
	if err != nil {
		return SegmentArtifact{}, err
	}
	entry := SegmentArtifact{
		ItemID:     itemID,
		OutputPath: segment.OutputPath,
		SHA256:     outcome.Identity.SHA256,
	}
	// The receipt gate resolves the content address under the job's policy;
	// a gate that accepted bytes without a usable claim must still leave a
	// proven address here, so hash the file directly.
	if entry.SHA256 == "" {
		digest, _, hashErr := hashio.File(segment.OutputPath)
		if hashErr != nil {
			return SegmentArtifact{}, fmt.Errorf("processor: hash segment %s: %w", segment.OutputPath, hashErr)
		}
		entry.SHA256 = digest
	}
	info, err := os.Stat(segment.OutputPath)
	if err != nil {
		return SegmentArtifact{}, fmt.Errorf("processor: stat segment %s: %w", segment.OutputPath, err)
	}
	entry.SizeBytes = info.Size()
	probed, source, probeErr := probeFactsForFinalize(ctx, receipt, receiptErr, segment.OutputPath)
	if probeErr != nil {
		return SegmentArtifact{}, fmt.Errorf("processor: segment probe: %w", probeErr)
	}
	if source != probeSourceReceipt {
		workerlog.ByJobID(jobID).Warnf("segment %s: media facts came from %s (no canonical receipt)", segment.OutputPath, source)
	}
	if expected := segment.EndFrame - segment.StartFrame; expected > 0 && probed.FrameCount > 0 && probed.FrameCount != int(expected) {
		return SegmentArtifact{}, fmt.Errorf("processor: segment %s decoded %d frames, want %d (its declared window)", segment.OutputPath, probed.FrameCount, expected)
	}
	entry.FrameCount = probed.FrameCount
	entry.DurationUS = probed.DurationUS
	entry.Width = probed.Width
	entry.Height = probed.Height
	return entry, nil
}

// storeSegment stores one verified segment under its own content address.
func (p *Processor) storeSegment(ctx context.Context, jobID string, entry SegmentArtifact) error {
	output, err := os.Open(entry.OutputPath)
	if err != nil {
		return fmt.Errorf("processor: open segment %s for upload: %w", entry.OutputPath, err)
	}
	defer output.Close()
	if err := p.store.PutReader(ctx, entry.SHA256, output, entry.SizeBytes); err != nil {
		return fmt.Errorf("processor: store segment %s: %w", entry.OutputPath, err)
	}
	workerlog.ByJobID(jobID).Infof("segment %s stored (item=%s sha256=%s bytes=%d frames=%d)", entry.OutputPath, entry.ItemID, entry.SHA256, entry.SizeBytes, entry.FrameCount)
	return nil
}

// retrySegment re-renders ONE segment without touching the other segments or
// the batch: the session's own request narrowed to the segment's frame window,
// into the same deterministic output path. The render is idempotent (same plan
// + same window ⇒ same pixels), so replacing the failed output with the
// retry's output is safe and no already-verified segment is re-rendered.
func (p *Processor) retrySegment(ctx context.Context, prepared *PreparedJob, segment chronon.OutputSegment) error {
	request := prepared.baseRenderRequest
	request.OutputSegments = nil
	request.OutputPath = segment.OutputPath
	request.RangeEnabled = true
	request.FirstFrame = segment.StartFrame
	request.LastFrame = segment.EndFrame - 1
	request.TotalFrames = segment.EndFrame - segment.StartFrame
	request.Progress = nil
	if err := p.renderer.Render(ctx, request); err != nil {
		return fmt.Errorf("processor: single-segment retry render [%d,%d): %w", segment.StartFrame, segment.EndFrame, err)
	}
	if p.strictNativeBackend && planHasVideoSource(prepared.Plan) &&
		!isFinalJobComposite(prepared.Job) {
		if err := requireNativeVulkan(
			segment.OutputPath, int(segment.EndFrame-segment.StartFrame)); err != nil {
			return fmt.Errorf("processor: single-segment retry failed native Vulkan gate: %w", err)
		}
	}
	return nil
}
