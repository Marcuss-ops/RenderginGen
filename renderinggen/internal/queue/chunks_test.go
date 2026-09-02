package queue

import (
	"encoding/json"
	"testing"
)

func TestPlanChunks(t *testing.T) {
	jobs, err := PlanChunks("parent", json.RawMessage(`{"schema":"chronon.render-plan"}`), nil, 0, 10, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("got %d chunks", len(jobs))
	}
	want := []FrameRange{{0, 4}, {4, 8}, {8, 10}}
	for i, job := range jobs {
		if job.ParentJobID != "parent" || job.ChunkIndex != i || *job.FrameRange != want[i] {
			t.Fatalf("chunk %d = %+v", i, job)
		}
	}
}

func TestPlanChunksRejectsInvalidInput(t *testing.T) {
	if _, err := PlanChunks("", nil, nil, 0, 1, 1); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := PlanChunks("parent", nil, nil, 2, 1, 1); err == nil {
		t.Fatal("expected validation error")
	}
}
