package queue

import "fmt"

// ValidateChildren enforces exact count, indices, completed artifacts, and
// contiguous half-open frame ranges for a parent assembly.
func ValidateChildren(children []*Job, expected, start, end int64) error {
	if expected <= 0 || end <= start {
		return fmt.Errorf("invalid parent chunk contract")
	}
	if int64(len(children)) != expected {
		return fmt.Errorf("chunk count %d, want %d", len(children), expected)
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
