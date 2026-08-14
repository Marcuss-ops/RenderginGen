package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeQueue returns an httptest server implementing the subset of the queue
// HTTP API the client speaks.
func fakeQueue(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /jobs", func(w http.ResponseWriter, r *http.Request) {
		var job Job
		_ = json.NewDecoder(r.Body).Decode(&job)
		switch job.ID {
		case "dup":
			http.Error(w, "already exists", http.StatusConflict)
		case "boom":
			http.Error(w, "bad input", http.StatusBadRequest)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": job.ID})
		}
	})

	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "missing" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		job := Job{
			ID:       id,
			State:    StateCompleted,
			Attempts: 2,
			Artifact: &Artifact{
				ID:           "art-1",
				URL:          "https://store/overlay.mp4",
				SHA256:       "abc",
				ProfileID:    "velox-h264-copy-v1",
				CopyEligible: true,
				ClosedGOP:    true,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(job)
	})

	mux.HandleFunc("GET /jobs/depth", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Stats{Pending: 3, Running: 1, Completed: 5, Depth: 4})
	})

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	return httptest.NewServer(mux)
}

func TestSubmitSuccess(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Submit(context.Background(), Job{ID: "job-1", OverlaySpec: []byte(`{"n":1}`)}); err != nil {
		t.Fatalf("submit: %v", err)
	}
}

func TestSubmitDuplicate(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	err := c.Submit(context.Background(), Job{ID: "dup"})
	if err == nil || !errors.Is(err, ErrJobExists) {
		t.Fatalf("want ErrJobExists, got %v", err)
	}
}

func TestSubmitError(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Submit(context.Background(), Job{ID: "boom"}); err == nil {
		t.Fatal("expected error on 400")
	}
}

func TestGetArtifact(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	job, err := c.Get(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if job.State != StateCompleted || job.Artifact == nil {
		t.Fatalf("unexpected job: %+v", job)
	}
	if !job.Artifact.CopyEligible || !job.Artifact.ClosedGOP || job.Artifact.ProfileID != "velox-h264-copy-v1" {
		t.Fatalf("artifact certification not round-tripped: %+v", job.Artifact)
	}
}

func TestGetNotFound(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Get(context.Background(), "missing")
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDepthAndHealth(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	stats, err := c.Depth(context.Background())
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if stats.Pending != 3 || stats.Completed != 5 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
}

func TestWaitCompletes(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	job, err := c.Wait(ctx, "job-1", time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job.State != StateCompleted || job.Artifact == nil {
		t.Fatalf("wait returned non-terminal job: %+v", job)
	}
}

func TestWaitContextCancel(t *testing.T) {
	srv := fakeQueue(t)
	defer srv.Close()

	c := New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Wait(ctx, "missing", time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("want context cancellation, got %v", err)
	}
}
