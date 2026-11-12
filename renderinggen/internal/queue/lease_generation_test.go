package queue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Completion must carry the lease generation that claimed the job. This is
// the fencing token: a worker whose lease expired cannot publish its result
// after a newer attempt took ownership.
func TestClientCompleteSendsLeaseGeneration(t *testing.T) {
	var gotGeneration int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs/job-1/complete", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			LeaseGeneration int64 `json:"lease_generation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode complete body: %v", err)
		}
		gotGeneration = body.LeaseGeneration
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL, "w1")
	if err := c.Complete(context.Background(), "job-1", 7, Artifact{}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if gotGeneration != 7 {
		t.Fatalf("lease generation sent = %d, want 7", gotGeneration)
	}
}

// A stale generation is a conflict at the queue boundary. The worker must see
// an error and must not treat the job as completed.
func TestClientCompleteRejectsStaleLeaseGeneration(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs/job-1/complete", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "stale lease generation", http.StatusConflict)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL, "w1")
	err := c.Complete(context.Background(), "job-1", 6, Artifact{})
	if err == nil {
		t.Fatal("stale generation must fail closed")
	}
}
