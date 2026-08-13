package queue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientClaimRenewComplete(t *testing.T) {
	var renewed int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs/claim", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"job-1","overlay_spec":{"o":1},"lease":60000000000}`))
	})
	mux.HandleFunc("POST /jobs/job-1/renew", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&renewed, 1)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /jobs/job-1/complete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL, "w1")

	job, err := c.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.ID != "job-1" {
		t.Fatalf("got %+v", job)
	}
	if job.Lease != 60*time.Second {
		t.Fatalf("want lease 60s, got %s", job.Lease)
	}

	if err := c.Renew(context.Background(), job.ID); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if atomic.LoadInt32(&renewed) != 1 {
		t.Fatalf("want 1 renew call, got %d", renewed)
	}

	if err := c.Complete(context.Background(), job.ID, Result{}); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func TestClientClaimEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs/claim", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL, "w1")
	job, err := c.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if job != nil {
		t.Fatalf("want nil job on empty queue, got %+v", job)
	}
}

func TestClientFail(t *testing.T) {
	var gotReason string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs/job-1/fail", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Data struct {
				Reason string `json:"reason"`
			} `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotReason = body.Data.Reason
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL, "w1")
	if err := c.Fail(context.Background(), "job-1", "boom"); err != nil {
		t.Fatal(err)
	}
	if gotReason != "boom" {
		t.Fatalf("reason = %q", gotReason)
	}
}
