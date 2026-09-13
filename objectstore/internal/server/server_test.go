package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/store"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(New(store.New(t.TempDir())).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestPutGetRoundTrip(t *testing.T) {
	ts := newServer(t)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/objects/abc", strings.NewReader("hello"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("put: want 201, got %d", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/objects/abc")
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
	resp, err := http.Get(ts.URL + "/objects/nope")
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

	// Present object: HEAD must answer 200 with Content-Length and no body.
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/objects/abc", strings.NewReader("hello"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("put: want 201, got %d", resp.StatusCode)
	}

	resp, err = http.Head(ts.URL + "/objects/abc")
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
	resp, err = http.Head(ts.URL + "/objects/nope")
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
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/objects/range", strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("put: want 201, got %d", resp.StatusCode)
	}

	// A mid-file range is how a resumable upload reads past its first chunk.
	// Before this fix the handler returned 200 with the whole object for a
	// Range request, so the reader consumed the prefix and uploaded the wrong
	// bytes instead of the requested offset.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/objects/range", nil)
	req.Header.Set("Range", "bytes=5-9")
	resp, err = http.DefaultClient.Do(req)
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
