package queue

import "fmt"

// ValidateChildren enforces the parent assembly contract for one chunk family:
// children arrive in deterministic chunk order (index 0..N-1), every child is
// completed with a durable artifact, their half-open frame ranges tile
// [start, end) contiguously — no holes, gaps or overlaps — and every child is
// CERTIFIED SAFE FOR PACKET-COPY CONCATENATION.
//
// The copy-safety half is not decorative: the parent is assembled by copying
// the children's packets without decoding (RenderingGen's ParentFinalizer →
// Chronon assemble_segments), so a child whose closed GOP is unproven, whose
// first frame is not a keyframe, or which was never certified copy-eligible can
// produce a stream that breaks at the concatenation boundary while every byte
// of every child is individually valid. Those three facts are certified on the
// artifact itself (media.ProbeResult → store_artifact), and a completed chunk
// job is ALWAYS structurally probed (processor.FinalizeJob probes any job that
// carries a FrameRange), so a missing fact is a defect, never "not measured".
//
// There is intentionally no "expected count" parameter: the worker cannot know
// a parent's planned chunk count independently of the children the queue
// returns (rows are insert-only and Children returns the whole family), so an
// expected-count check at the production call site could never fire — a lying
// contract. Dense indices plus exact frame coverage of [start, end) are the
// strongest verifiable completeness signal.
func ValidateChildren(children []*Job, start, end int64) error {
	if end <= start {
		return fmt.Errorf("invalid parent chunk contract")
	}
	cursor := start
	for i, child := range children {
		if child == nil || child.ChunkIndex != i {
			return fmt.Errorf("chunk index at position %d is invalid", i)
		}
		if child.State != StateCompleted || child.Artifact == nil || child.Artifact.StorageKey == "" {
			return fmt.Errorf("chunk %d is not completed with an artifact", i)
		}
		if child.FrameRange == nil || child.FrameRange.Start != cursor || child.FrameRange.End <= child.FrameRange.Start {
			return fmt.Errorf("chunk %d has gap, overlap, or invalid frame range", i)
		}
		if !child.Artifact.CopyEligible {
			return fmt.Errorf("chunk %d is not certified copy-eligible: the parent is assembled by packet copy and an uncertified chunk can never be proven safe", i)
		}
		if !child.Artifact.ClosedGOP {
			return fmt.Errorf("chunk %d has no certified closed-GOP structure: concatenating an open-GOP chunk can break the stream at the boundary", i)
		}
		if !child.Artifact.FirstFrameKeyframe {
			return fmt.Errorf("chunk %d does not start on a keyframe: a copied stream cannot decode from a non-keyframe boundary", i)
		}
		cursor = child.FrameRange.End
	}
	if cursor != end {
		return fmt.Errorf("chunk ranges end at %d, want %d", cursor, end)
	}
	return nil
}
