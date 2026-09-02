package queue

import (
	"context"
	"encoding/json"
	"fmt"
)

// PlanChunks splits a frame interval into contiguous, half-open ranges.
func PlanChunks(parentJobID string, plan json.RawMessage, assets []AssetRef, start, end, chunkSize int64) ([]Job, error) {
	if parentJobID == "" || start < 0 || end <= start || chunkSize <= 0 {
		return nil, fmt.Errorf("invalid chunk planning arguments")
	}
	jobs := make([]Job, 0, (end-start+chunkSize-1)/chunkSize)
	for index, frame := 0, start; frame < end; index, frame = index+1, frame+chunkSize {
		chunkEnd := frame + chunkSize
		if chunkEnd > end {
			chunkEnd = end
		}
		jobs = append(jobs, Job{
			ID:     fmt.Sprintf("%s-chunk-%04d", parentJobID, index),
			Schema: JobSchemaV1, Version: JobSchemaVersionV1,
			IdempotencyKey: fmt.Sprintf("%s:%d", parentJobID, index),
			JobType:        JobTypeRenderSegment, ParentJobID: parentJobID,
			ChunkIndex: index, FrameRange: &FrameRange{Start: frame, End: chunkEnd},
			RenderPlan: plan, Assets: append([]AssetRef(nil), assets...),
		})
	}
	return jobs, nil
}

// SubmitChunks submits planned chunks through the existing queue client.
func SubmitChunks(ctx context.Context, client *Client, jobs []Job) error {
	for _, job := range jobs {
		if err := client.Submit(ctx, job); err != nil {
			return fmt.Errorf("submit chunk %s: %w", job.ID, err)
		}
	}
	return nil
}
