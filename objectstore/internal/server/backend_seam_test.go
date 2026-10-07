package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/store"
)

// TestHTTPContractRunsOverANonFilesystemBackend is the acceptance test for the
// store seam: every status the HTTP surface promises is produced by a backend
// with no disk at all. A regression that re-couples a handler to the filesystem
// adapter — or that leaves the content-address check in the adapter instead of
// in the contract — fails here, which is what makes a shared/CAS backend a
// deployment change rather than a rewrite of the HTTP layer.
func TestHTTPContractRunsOverANonFilesystemBackend(t *testing.T) {
	ts := httptest.NewServer(New(store.NewMemory()).Handler())
	t.Cleanup(ts.Close)

	const body = "payload served by a shared backend"
	key := digestOf(body)

	if status := put(t, ts, key, body); status != http.StatusCreated {
		t.Fatalf("put: want 201, got %d", status)
	}

	resp, err := http.Get(ts.URL + "/objects/" + key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(got) != body {
		t.Fatalf("get: status=%d body=%q", resp.StatusCode, got)
	}

	// HEAD stays a bodyless existence probe on every backend.
	head, err := http.Head(ts.URL + "/objects/" + key)
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusOK {
		t.Fatalf("head: want 200, got %d", head.StatusCode)
	}

	// /verify re-hashes through the backend, not through the disk adapter.
	verify, err := http.Get(ts.URL + "/objects/" + key + "/verify")
	if err != nil {
		t.Fatal(err)
	}
	verify.Body.Close()
	if verify.StatusCode != http.StatusOK {
		t.Fatalf("verify: want 200, got %d", verify.StatusCode)
	}

	// The write-path guarantee is part of the contract, so it survives the swap.
	if status := put(t, ts, digestOf("here"), "somewhere else"); status != http.StatusUnprocessableEntity {
		t.Fatalf("mismatched put over a non-filesystem backend: want 422, got %d", status)
	}

	// ... and so does an absent object.
	missing, err := http.Get(ts.URL + "/objects/" + digestOf("never stored anywhere"))
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing: want 404, got %d", missing.StatusCode)
	}
}
