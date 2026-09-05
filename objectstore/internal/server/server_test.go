package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/objectstore/internal/store"
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
