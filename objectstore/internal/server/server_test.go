package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/buildinfo"
	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/store"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(New(store.New(t.TempDir())).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// digestOf derives the content address the way every producer does. Keys in
// these tests are real digests: the store's contract is that a key IS the
// digest of its body, so a test key like "abc" would be testing a contract the
// server no longer has.
func digestOf(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func put(t *testing.T, ts *httptest.Server, key, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/objects/"+key, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func TestPutGetRoundTrip(t *testing.T) {
	ts := newServer(t)
	key := digestOf("hello")

	if status := put(t, ts, key, "hello"); status != http.StatusCreated {
		t.Fatalf("put: want 201, got %d", status)
	}

	resp, err := http.Get(ts.URL + "/objects/" + key)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Fatalf("got %q", body)
	}
}

func TestGetMissing(t *testing.T) {
	ts := newServer(t)
	resp, err := http.Get(ts.URL + "/objects/" + digestOf("never stored"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestHeadReportsExistenceWithoutBody(t *testing.T) {
	ts := newServer(t)
	key := digestOf("hello")

	if status := put(t, ts, key, "hello"); status != http.StatusCreated {
		t.Fatalf("put: want 201, got %d", status)
	}

	// Present object: HEAD must answer 200 with Content-Length and no body.
	resp, err := http.Head(ts.URL + "/objects/" + key)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("head existing: want 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Length"); got != "5" {
		t.Fatalf("head Content-Length = %q, want 5", got)
	}

	// Absent object: HEAD must answer 404.
	resp, err = http.Head(ts.URL + "/objects/" + digestOf("absent"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("head missing: want 404, got %d", resp.StatusCode)
	}
}

func TestGetServesRangeRequests(t *testing.T) {
	ts := newServer(t)

	const body = "0123456789abcdefghij" // 20 bytes
	key := digestOf(body)
	if status := put(t, ts, key, body); status != http.StatusCreated {
		t.Fatalf("put: want 201, got %d", status)
	}

	// A mid-file range is how a resumable upload reads past its first chunk.
	// Before this fix the handler returned 200 with the whole object for a
	// Range request, so the reader consumed the prefix and uploaded the wrong
	// bytes instead of the requested offset.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/objects/"+key, nil)
	req.Header.Set("Range", "bytes=5-9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range get: want 206, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 5-9/20" {
		t.Fatalf("Content-Range = %q, want %q", got, "bytes 5-9/20")
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != body[5:10] {
		t.Fatalf("range body = %q, want %q", got, body[5:10])
	}
}

// TestPutRejectsBytesThatDoNotMatchTheAddress is the HTTP half of the P0
// regression: `PUT /objects/<sha-of-A>` carrying B's bytes used to answer 201,
// after which `HEAD <sha-of-A>` returned 200 to every worker that trusted it.
func TestPutRejectsBytesThatDoNotMatchTheAddress(t *testing.T) {
	ts := newServer(t)
	key := digestOf("the bytes that belong at this address")

	if status := put(t, ts, key, "a different payload"); status != http.StatusUnprocessableEntity {
		t.Fatalf("mismatched put: want 422, got %d", status)
	}

	// The address must stay empty, and stay empty for BOTH probes: a 201
	// followed by a truthful 404 is the whole point of rejecting at write time.
	resp, err := http.Head(ts.URL + "/objects/" + key)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("head after rejected put: want 404, got %d", resp.StatusCode)
	}
	resp, err = http.Get(ts.URL + "/objects/" + key)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after rejected put: want 404, got %d", resp.StatusCode)
	}

	// The same bytes are accepted once addressed by their own digest.
	if status := put(t, ts, digestOf("a different payload"), "a different payload"); status != http.StatusCreated {
		t.Fatalf("correctly addressed put: want 201, got %d", status)
	}
}

// TestPutRejectsMalformedContentAddress: a key is a digest, not a name. A
// logical-path key would create an object no content-addressed reader can ever
// find, so it is refused up front.
func TestPutRejectsMalformedContentAddress(t *testing.T) {
	ts := newServer(t)
	for _, key := range []string{"abc", "plan.json", strings.ToUpper(digestOf("hello"))} {
		if status := put(t, ts, key, "hello"); status != http.StatusBadRequest {
			t.Fatalf("put %q: want 400, got %d", key, status)
		}
		resp, err := http.Head(ts.URL + "/objects/" + key)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("head %q: want 400, got %d", key, resp.StatusCode)
		}
	}
}

// TestPutIsRetrySafe: re-uploading identical bytes is a success, so an
// interrupted-then-retried producer does not have to special-case the second
// attempt.
func TestPutIsRetrySafe(t *testing.T) {
	ts := newServer(t)
	key := digestOf("retry me")
	for i := 0; i < 2; i++ {
		if status := put(t, ts, key, "retry me"); status != http.StatusCreated {
			t.Fatalf("put attempt %d: want 201, got %d", i, status)
		}
	}
}

// TestPutErrorStatus pins the status mapping directly, including the oversized
// case that cannot be produced without a 5 GiB body.
func TestPutErrorStatus(t *testing.T) {
	if got := putErrorStatus(&http.MaxBytesError{Limit: 1}); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: want 413, got %d", got)
	}
}

func TestVerifyEndpointAcceptsStoredObject(t *testing.T) {
	ts := newServer(t)
	key := digestOf("verified payload")
	if status := put(t, ts, key, "verified payload"); status != http.StatusCreated {
		t.Fatalf("put: want 201, got %d", status)
	}

	resp, err := http.Get(ts.URL + "/objects/" + key + "/verify")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify: want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true || body["sha256"] != key {
		t.Fatalf("verify body = %v", body)
	}
	if got, _ := body["size_bytes"].(float64); int64(got) != int64(len("verified payload")) {
		t.Fatalf("size_bytes = %v", body["size_bytes"])
	}
}

// TestVerifyEndpointDetectsTamperedBytes drives the read-side check over HTTP:
// the bytes are replaced on disk (a restore or hand copy, not an API call), so
// the write path's guarantee no longer applies and the endpoint must say so
// instead of repeating HEAD's cheerful 200.
func TestVerifyEndpointDetectsTamperedBytes(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(New(store.New(dir)).Handler())
	t.Cleanup(ts.Close)

	key := digestOf("the original bytes")
	if status := put(t, ts, key, "the original bytes"); status != http.StatusCreated {
		t.Fatalf("put: want 201, got %d", status)
	}
	// HEAD is still 200: it reports existence, which is exactly why an explicit
	// verification surface has to exist.
	if resp, err := http.Head(ts.URL + "/objects/" + key); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("head before tamper: want 200, got %d", resp.StatusCode)
		}
	}

	tampered := "the TAMPERED Bytes"
	if len(tampered) != len("the original bytes") {
		t.Fatal("fixture must be length-preserving")
	}
	if err := os.WriteFile(filepath.Join(dir, "objects", key[:2], key), []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/objects/" + key + "/verify")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("verify tampered: want 409, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false || body["sha256"] != digestOf(tampered) {
		t.Fatalf("verify body = %v", body)
	}
}

func TestVerifyEndpointMissingAndMalformed(t *testing.T) {
	ts := newServer(t)

	resp, err := http.Get(ts.URL + "/objects/" + digestOf("absent") + "/verify")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("verify missing: want 404, got %d", resp.StatusCode)
	}

	// A non-canonical key is a caller error here too, not a 404: the caller
	// asked about something that cannot be a content address.
	resp, err = http.Get(ts.URL + "/objects/plan.json/verify")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("verify malformed key: want 400, got %d", resp.StatusCode)
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
	body := decodeHealth(t, resp)
	if body["status"] != "ok" {
		t.Fatalf("health status = %v", body["status"])
	}
}

func decodeHealth(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health: %v", err)
	}
	return body
}

// TestHealthPublishesBuildIdentity is the store's half of the runtime-identity
// contract: ONE request must answer both "is the store up?" and "which object
// store build is behind :9000?". Before this the store was the only service in
// the render path that could not identify itself, so an operator could read the
// revision of PipelineGen and RenderingGen and still have to guess here.
func TestHealthPublishesBuildIdentity(t *testing.T) {
	srv := New(store.New(t.TempDir()))
	srv.SetBuildInfo(func() buildinfo.Identity {
		return buildinfo.Identity{
			Version:      "0.1.0",
			GitCommit:    "a1b2c3d4e5f6",
			BuildTime:    "2026-09-17T09:00:00Z",
			BinarySHA256: strings.Repeat("1", 64),
			Mode:         "objectstore",
			WorkerID:     "store-host-1",
			PID:          4242,
			StartedAt:    "2026-09-17T09:00:01Z",
			IdentityHash: "cafebabecafebabe",
		}
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	build, ok := decodeHealth(t, resp)["build"].(map[string]any)
	if !ok {
		t.Fatal("/health must publish the store build identity")
	}
	if build["git_commit"] != "a1b2c3d4e5f6" {
		t.Errorf("git_commit = %v", build["git_commit"])
	}
	if build["mode"] != "objectstore" {
		t.Errorf("mode = %v", build["mode"])
	}
	if build["identity_hash"] != "cafebabecafebabe" {
		t.Errorf("identity_hash = %v", build["identity_hash"])
	}
}

// TestHealthOmitsBuildWhenUnwired keeps the historical payload shape for
// fixtures that construct the server directly.
func TestHealthOmitsBuildWhenUnwired(t *testing.T) {
	ts := newServer(t)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, present := decodeHealth(t, resp)["build"]; present {
		t.Error("an unwired build identity must be omitted, not emitted empty")
	}
}

// TestHealthDeclaresStoreCapabilities is what lets a probe distinguish this
// store from a pre-verification build WITHOUT hashing anything: the declared
// contract version and the two verification flags are the machine-readable
// statement a canary (or a runtime doctor) asserts before trusting the store.
func TestHealthDeclaresStoreCapabilities(t *testing.T) {
	ts := newServer(t)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, ok := decodeHealth(t, resp)["store"].(map[string]any)
	if !ok {
		t.Fatal("/health must declare the store capabilities")
	}
	if raw["content_address"] != store.ContentAddressProtocol {
		t.Errorf("content_address = %v, want %q", raw["content_address"], store.ContentAddressProtocol)
	}
	if raw["path_layout"] != store.PathLayout {
		t.Errorf("path_layout = %v, want %q", raw["path_layout"], store.PathLayout)
	}
	if got, _ := raw["address_length"].(float64); int(got) != store.SHA256HexLen {
		t.Errorf("address_length = %v, want %d", raw["address_length"], store.SHA256HexLen)
	}
	if raw["verify_on_write"] != true {
		t.Error("verify_on_write must be declared true by a store whose write path verifies")
	}
	if raw["verify_on_read"] != true {
		t.Error("verify_on_read must be declared true because /verify and Scan exist")
	}
}
