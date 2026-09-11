// submit_test.go proves the replay contract against a fake queue: the first
// run submits, the replay resolves every job as existing and submits nothing,
// and only genuine queue errors stop the walk.
package batch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	queue "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// fakeQueue records submissions; -existing marks which job IDs answer 409.
type fakeQueue struct {
	mu        sync.Mutex
	jobs      map[string]bool
	getOn409  bool
	submits   int
	creates   int // submit POSTs that actually created a job (201)
	gets      int
	failAfter int // when > 0, fail the Nth submit with a 500
}

func (f *fakeQueue) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode submit body: %v", err)
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.submits++
		if f.failAfter > 0 && f.submits > f.failAfter {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if f.jobs[body.ID] {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("job already exists"))
			return
		}
		f.jobs[body.ID] = true
		f.creates++
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /jobs/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.gets++
		id := r.URL.Path[len("/jobs/"):]
		if !f.getOn409 || !f.jobs[id] {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(queue.Job{ID: id, State: queue.StatePending})
	})
	return mux
}

func jobsFor(t *testing.T, m Manifest) []queue.Job {
	t.Helper()
	jobs, err := Expand(m)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func TestSubmitAllFreshBatch(t *testing.T) {
	fq := &fakeQueue{jobs: map[string]bool{}}
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()
	client := queue.New(srv.URL)

	jobs := jobsFor(t, flatManifest())
	res, err := SubmitAll(context.Background(), ClientSubmitter{Client: client}, jobs)
	if err != nil {
		t.Fatalf("SubmitAll: %v", err)
	}
	if len(res.Submitted) != 2 || len(res.Existing) != 0 {
		t.Fatalf("fresh batch: submitted=%v existing=%v", res.Submitted, res.Existing)
	}
}

func TestSubmitAllReplayIsIdempotent(t *testing.T) {
	fq := &fakeQueue{jobs: map[string]bool{}, getOn409: true}
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()
	client := queue.New(srv.URL)

	jobs := jobsFor(t, flatManifest())
	if _, err := SubmitAll(context.Background(), ClientSubmitter{Client: client}, jobs); err != nil {
		t.Fatalf("first run: %v", err)
	}
	res, err := SubmitAll(context.Background(), ClientSubmitter{Client: client}, jobs)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(res.Submitted) != 0 || len(res.Existing) != 2 {
		t.Fatalf("replay must submit nothing: submitted=%v existing=%v", res.Submitted, res.Existing)
	}
	// Across both runs the queue must have created exactly one job per
	// logical video — the whole point of idempotent batch submission. The
	// replay POSTs again (409s) but must not duplicate anything.
	if fq.creates != 2 {
		t.Fatalf("queue created %d jobs across both runs, want 2", fq.creates)
	}
}

func TestSubmitAllStopsOnQueueError(t *testing.T) {
	fq := &fakeQueue{jobs: map[string]bool{}, failAfter: 1}
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()
	client := queue.New(srv.URL)

	jobs := jobsFor(t, flatManifest())
	res, err := SubmitAll(context.Background(), ClientSubmitter{Client: client}, jobs)
	if err == nil {
		t.Fatal("queue 500 must surface as an error")
	}
	if len(res.Submitted) != 1 {
		t.Fatalf("stop-on-first-error: submitted=%v", res.Submitted)
	}
}

func TestSubmitAllRejectsInconsistentExisting(t *testing.T) {
	// A 409 whose job cannot be read back is a queue inconsistency: the
	// submitter must surface it, never silently count it as success.
	fq := &fakeQueue{jobs: map[string]bool{}} // getOn409=false → Get returns 404
	srv := httptest.NewServer(fq.handler(t))
	defer srv.Close()
	// Pre-seed the jobs map so submits 409 but Get 404s is impossible in the
	// fake; instead simulate with an isolated Submit stub.
	fq.jobs["batch-2026-09-10:video-001"] = true

	s := stubSubmitter{getErr: errors.New("not found")}
	jobs := jobsFor(t, flatManifest())
	if _, err := SubmitAll(context.Background(), s, jobs); err == nil {
		t.Fatal("unreadable existing job must surface as an error")
	}
}

type stubSubmitter struct {
	submitErr error
	getErr    error
	job       queue.Job
}

func (s stubSubmitter) Submit(ctx context.Context, job queue.Job) error {
	if s.submitErr != nil {
		return s.submitErr
	}
	return queue.ErrJobExists
}

func (s stubSubmitter) Get(ctx context.Context, id string) (queue.Job, error) {
	if s.getErr != nil {
		return queue.Job{}, s.getErr
	}
	if s.job.ID != "" {
		return s.job, nil
	}
	return queue.Job{}, s.getErr
}
