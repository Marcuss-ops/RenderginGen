package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/store"
)

// stagingPut streams a pending write and returns the response status.
func stagingPut(t *testing.T, ts *httptest.Server, session, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/objects/staging/"+session, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// stagingFinalize asserts a content address for a pending session.
func stagingFinalize(t *testing.T, ts *httptest.Server, session, sha string) int {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"sha256": sha})
	resp, err := http.Post(ts.URL+"/objects/staging/"+session+"/finalize", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// stagingAbort deletes a pending session.
func stagingAbort(t *testing.T, ts *httptest.Server, session string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/objects/staging/"+session, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// runStagingSuite exercises the staging routes against one backend. It runs
// against BOTH adapters so the disk and in-process stores cannot drift on
// statuses or on the verify-before-install rule.
func runStagingSuite(t *testing.T, ts *httptest.Server) {
	t.Helper()

	body := "fragmented mp4 bytes, final while appended"
	digest := digestOf(body)

	// 1. Streaming pending write: 201 with the digest computed from the
	//    stream, and the object is NOT visible under any content address yet.
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/objects/staging/job-42", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var staged struct {
		SHA256    string `json:"sha256"`
		SizeBytes int64  `json:"size_bytes"`
		Session   string `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&staged); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("staging put status = %d, want 201", resp.StatusCode)
	}
	if staged.SHA256 != digest {
		t.Fatalf("staging put digest = %s, want %s", staged.SHA256, digest)
	}
	if staged.SizeBytes != int64(len(body)) || staged.Session != "job-42" {
		t.Fatalf("staging put echo = %+v", staged)
	}
	head, err := http.Get(ts.URL + "/objects/" + digest)
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusNotFound {
		t.Fatalf("pending object visible before finalize: status = %d, want 404", head.StatusCode)
	}

	// 2. Finalize with a WRONG assertion: 422 and the pending bytes are
	//    discarded (a second finalize then reports 404).
	wrong := digestOf("some other content")
	if status := stagingFinalize(t, ts, "job-42", wrong); status != http.StatusUnprocessableEntity {
		t.Fatalf("wrong finalize status = %d, want 422", status)
	}
	if status := stagingFinalize(t, ts, "job-42", digest); status != http.StatusNotFound {
		t.Fatalf("finalize after rejected finalize status = %d, want 404", status)
	}

	// 3. Repeat the flow end to end: after finalize the bytes are readable
	//    AND correct under their content address.
	if status := stagingPut(t, ts, "job-43", body); status != http.StatusCreated {
		t.Fatalf("second staging put status = %d, want 201", status)
	}
	if status := stagingFinalize(t, ts, "job-43", digest); status != http.StatusCreated {
		t.Fatalf("finalize status = %d, want 201", status)
	}
	getResp, err := http.Get(ts.URL + "/objects/" + digest)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get after finalize status = %d, want 200", getResp.StatusCode)
	}
	got, _ := io.ReadAll(getResp.Body)
	if string(got) != body {
		t.Fatalf("get after finalize body = %q, want %q", got, body)
	}

	// 4. Abort is idempotent: 204 for a missing session, twice.
	if status := stagingAbort(t, ts, "never-existed"); status != http.StatusNoContent {
		t.Fatalf("abort missing session status = %d, want 204", status)
	}
	if status := stagingAbort(t, ts, "never-existed"); status != http.StatusNoContent {
		t.Fatalf("second abort status = %d, want 204", status)
	}

	// 5. A session that is not a safe opaque id is rejected with 400: the
	//    token becomes a file name and can never address anything else.
	if status := stagingPut(t, ts, "bad.session", body); status != http.StatusBadRequest {
		t.Fatalf("invalid session status = %d, want 400", status)
	}
}

func TestStagingFlowDiskBackend(t *testing.T) {
	runStagingSuite(t, newServer(t))
}

func TestStagingFlowMemoryBackend(t *testing.T) {
	ts := httptest.NewServer(New(store.NewMemory()).Handler())
	t.Cleanup(ts.Close)
	runStagingSuite(t, ts)
}

// unverifiedBackend wraps a Backend in a type that does NOT implement
// StagingBackend, to pin the honest 501 for backends without the extension.
type unverifiedBackend struct{ store.Backend }

func TestStagingNotImplementedStatus(t *testing.T) {
	ts := httptest.NewServer(New(unverifiedBackend{store.NewMemory()}).Handler())
	t.Cleanup(ts.Close)
	if status := stagingPut(t, ts, "job-1", "x"); status != http.StatusNotImplemented {
		t.Fatalf("staging on non-staging backend status = %d, want 501", status)
	}
}
