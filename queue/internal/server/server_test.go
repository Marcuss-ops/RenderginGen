package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository/memory"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/service"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	repo := memory.New(30*time.Second, 3)
	svc := service.New(repo)
	ts := httptest.NewServer(New(svc).Handler())
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

	resp := post(t, ts.URL+"/jobs", `{"id":"job-1","schema":"renderinggen.job","version":1,"render_plan":{"o":1},"assets":[{"hash":"abc","logical_path":"videos/base.mp4"}]}`)
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
	resp = post(t, ts.URL+"/jobs/job-1/complete", `{"worker":"w1","data":{"storage_key":"sha-job-1","artifact_hash":"sha-job-1","size_bytes":1,"content_type":"video/mp4"}}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("complete: want 204, got %d", resp.StatusCode)
	}
}

func TestSubmitDuplicateIDIsConflict(t *testing.T) {
	ts := newServer(t)
	body := `{"id":"job-dup","render_plan":{"n":1}}`
	first := post(t, ts.URL+"/jobs", body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first submit: got %d", first.StatusCode)
	}
	first.Body.Close()

	resp := post(t, ts.URL+"/jobs", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate submit: want 409, got %d", resp.StatusCode)
	}
}

// failingRepo forces a non-duplicate storage failure on submit so the server
// can be proven NOT to collapse internal errors into 409 (which the client
// would treat as idempotent success and silently drop the job).
type failingRepo struct {
	*memory.Repository
}

func (failingRepo) SubmitIdempotent(model.Job) (*model.Job, bool, error) {
	return nil, false, errors.New("storage unavailable")
}

func TestSubmitStorageFailureIsNotConflict(t *testing.T) {
	repo := failingRepo{memory.New(30*time.Second, 3)}
	svc := service.New(repo)
	ts := httptest.NewServer(New(svc).Handler())
	t.Cleanup(ts.Close)

	resp := post(t, ts.URL+"/jobs", `{"id":"job-1","render_plan":{"n":1}}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("storage failure on submit: want 500, got %d", resp.StatusCode)
	}
}

func TestSubmitIdempotencyReturnsCanonicalJob(t *testing.T) {
	ts := newServer(t)
	body := `{"id":"job-first","idempotency_key":"pipeline-123:scene-04:overlay-v1","render_plan":{"schema":"chronon.render-plan","version":1}}`
	first := post(t, ts.URL+"/jobs", body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first submit: got %d", first.StatusCode)
	}
	var firstResponse struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(first.Body).Decode(&firstResponse); err != nil {
		t.Fatal(err)
	}
	first.Body.Close()

	second := post(t, ts.URL+"/jobs", `{"id":"job-retry-generated","idempotency_key":"pipeline-123:scene-04:overlay-v1","render_plan":{"different":true}}`)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("retry submit: got %d", second.StatusCode)
	}
	defer second.Body.Close()
	var secondResponse struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(second.Body).Decode(&secondResponse); err != nil {
		t.Fatal(err)
	}
	if secondResponse.ID != firstResponse.ID {
		t.Fatalf("canonical id changed: first=%q retry=%q", firstResponse.ID, secondResponse.ID)
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

func TestCompletePersistsArtifact(t *testing.T) {
	ts := newServer(t)

	post(t, ts.URL+"/jobs", `{"id":"job-1","schema":"renderinggen.job","version":1,"render_plan":{"n":1}}`)
	post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`)

	completeBody := `{"worker":"w1","data":{` +
		`"kind":"segment","storage_key":"abc123","artifact_url":"https://store/objects/abc123",` +
		`"artifact_hash":"abc123","content_type":"video/mp4","size_bytes":42,` +
		`"backend":"software","chronon_version":"0.1.0"}}`
	resp := post(t, ts.URL+"/jobs/job-1/complete", completeBody)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("complete: want 204, got %d", resp.StatusCode)
	}

	getResp, err := http.Get(ts.URL + "/jobs/job-1")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	var job struct {
		State    string `json:"state"`
		Artifact *struct {
			ArtifactHash   string `json:"artifact_hash"`
			ArtifactURL    string `json:"artifact_url"`
			ContentType    string `json:"content_type"`
			SizeBytes      int64  `json:"size_bytes"`
			Backend        string `json:"backend"`
			ChrononVersion string `json:"chronon_version"`
		} `json:"artifact"`
	}
	if err := json.NewDecoder(getResp.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.State != "completed" || job.Artifact == nil {
		t.Fatalf("unexpected job: %+v", job)
	}
	a := job.Artifact
	if a.ArtifactHash != "abc123" || a.ArtifactURL != "https://store/objects/abc123" ||
		a.ContentType != "video/mp4" || a.SizeBytes != 42 ||
		a.Backend != "software" || a.ChrononVersion != "0.1.0" {
		t.Fatalf("artifact not persisted: %+v", a)
	}
}
