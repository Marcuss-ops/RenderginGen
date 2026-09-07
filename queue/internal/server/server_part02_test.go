package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCancelEndpointMakesJobUnclaimableAcrossLeaseCycle(t *testing.T) {
	ts := newServer(t)

	resp := post(t, ts.URL+"/jobs", `{"id":"job-cancel","schema":"renderinggen.job","version":1,"render_plan":{"o":1},"assets":[]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit: want 201, got %d", resp.StatusCode)
	}

	// Worker claims (one Chronon invocation), then the producer cancels.
	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim: want 200, got %d", resp.StatusCode)
	}
	resp = post(t, ts.URL+"/jobs/job-cancel/cancel", `{}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel: want 204, got %d", resp.StatusCode)
	}
	// Idempotent re-cancel is still 204.
	resp = post(t, ts.URL+"/jobs/job-cancel/cancel", `{}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second cancel: want 204, got %d", resp.StatusCode)
	}

	var job struct {
		State    string `json:"state"`
		Attempts int    `json:"attempts"`
	}
	getResp, err := http.Get(ts.URL + "/jobs/job-cancel")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if err := json.NewDecoder(getResp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.State != "cancelled" || job.Attempts != 1 {
		t.Fatalf("after cancel: state=%q attempts=%d, want cancelled/1", job.State, job.Attempts)
	}

	// Across lease expiry and repeated claims the job must never come back.
	for i := 0; i < 2; i++ {
		resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w2"}`)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("claim after cancel: want 204 (no job), got %d", resp.StatusCode)
		}
		getResp, err := http.Get(ts.URL + "/jobs/job-cancel")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewDecoder(getResp.Body).Decode(&job); err != nil {
			t.Fatal(err)
		}
		getResp.Body.Close()
		if job.State != "cancelled" || job.Attempts != 1 {
			t.Fatalf("cycle %d: state=%q attempts=%d, want cancelled/1", i, job.State, job.Attempts)
		}
	}
}

func TestCancelEndpointRejectsTerminalAndMissingJobs(t *testing.T) {
	ts := newServer(t)
	resp := post(t, ts.URL+"/jobs/missing/cancel", `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cancel missing: want 404, got %d", resp.StatusCode)
	}

	resp = post(t, ts.URL+"/jobs", `{"id":"job-done","render_plan":{"o":1}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit: got %d", resp.StatusCode)
	}
	resp = post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim: got %d", resp.StatusCode)
	}
	resp = post(t, ts.URL+"/jobs/job-done/complete", `{"worker":"w1","data":{"storage_key":"k","artifact_hash":"h","size_bytes":1,"content_type":"video/mp4"}}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("complete: got %d", resp.StatusCode)
	}
	resp = post(t, ts.URL+"/jobs/job-done/cancel", `{}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("cancel completed: want 409, got %d", resp.StatusCode)
	}
}
