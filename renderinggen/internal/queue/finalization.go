package queue

import "fmt"

// ValidateChildren enforces the parent assembly contract for one chunk family:
// children arrive in deterministic chunk order (index 0..N-1), every child is
// completed with a durable artifact, and their half-open frame ranges tile
// [start, end) contiguously — no holes, gaps or overlaps.
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
		cursor = child.FrameRange.End
	}
	if cursor != end {
		return fmt.Errorf("chunk ranges end at %d, want %d", cursor, end)
	}
	return nil
}
