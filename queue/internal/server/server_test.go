package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/store"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := store.New(30*time.Second, 3)
	ts := httptest.NewServer(New(st).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestSubmitClaimCompleteFlow(t *testing.T) {
	ts := newServer(t)

	resp := post(t, ts.URL+"/jobs", `{"id":"job-1","overlay_spec":{"o":1},"assets":[{"hash":"abc","url":"s3://a"}]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit: want 201, got %d", resp.StatusCode)
	}

	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim: want 200, got %d", resp.StatusCode)
	}
	var claimed struct {
		ID    string `json:"id"`
		Lease int64  `json:"lease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&claimed); err != nil {
		t.Fatal(err)
	}
	if claimed.ID != "job-1" {
		t.Fatalf("want job-1, got %s", claimed.ID)
	}
	if claimed.Lease != int64(30*time.Second) {
		t.Fatalf("want lease 30s in ns, got %d", claimed.Lease)
	}

	// Second claim on an empty queue -> 204.
	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w2"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("empty claim: want 204, got %d", resp.StatusCode)
	}

	// Complete.
	resp = post(t, ts.URL+"/jobs/job-1/complete", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("complete: want 204, got %d", resp.StatusCode)
	}
}

func TestDepthReflectsPending(t *testing.T) {
	ts := newServer(t)
	post(t, ts.URL+"/jobs", `{"id":"a"}`)
	post(t, ts.URL+"/jobs", `{"id":"b"}`)

	resp, err := http.Get(ts.URL + "/jobs/depth")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stats struct {
		Depth int `json:"depth"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.Depth != 2 {
		t.Fatalf("want depth 2, got %d", stats.Depth)
	}
}

func TestHealth(t *testing.T) {
	ts := newServer(t)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: want 200, got %d", resp.StatusCode)
	}
}

func TestRenewEndpoint(t *testing.T) {
	ts := newServer(t)
	post(t, ts.URL+"/jobs", `{"id":"job-1"}`)

	resp := post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim: want 200, got %d", resp.StatusCode)
	}

	resp = post(t, ts.URL+"/jobs/job-1/renew", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("renew: want 204, got %d", resp.StatusCode)
	}

	// Wrong worker can't renew.
	resp = post(t, ts.URL+"/jobs/job-1/renew", `{"worker":"w2"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("wrong-worker renew: want 409, got %d", resp.StatusCode)
	}
}
