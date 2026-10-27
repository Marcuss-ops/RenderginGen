package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stagingFake is a minimal object store staging surface: it hashes what it
// receives exactly like the real store (so a client/store digest mismatch is
// expressible) and records the finalize assertion.
type stagingFake struct {
	body           []byte
	finalizeSHA    string
	finalizeCalled bool
	reportedDigest string // when set, overrides the digest reported at write
	uploadStatus   int
	finalizeStatus int
}

func (f *stagingFake) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /objects/staging/{session}", func(w http.ResponseWriter, r *http.Request) {
		if f.uploadStatus != 0 && f.uploadStatus != http.StatusCreated {
			http.Error(w, "upload refused", f.uploadStatus)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.body = body
		sum := sha256.Sum256(body)
		digest := f.reportedDigest
		if digest == "" {
			digest = hex.EncodeToString(sum[:])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session":    r.PathValue("session"),
			"sha256":     digest,
			"size_bytes": len(body),
		})
	})
	mux.HandleFunc("POST /objects/staging/{session}/finalize", func(w http.ResponseWriter, r *http.Request) {
		if f.finalizeStatus != 0 && f.finalizeStatus != http.StatusCreated {
			http.Error(w, "finalize refused", f.finalizeStatus)
			return
		}
		var body struct {
			SHA256 string `json:"sha256"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.finalizeCalled = true
		f.finalizeSHA = body.SHA256
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "key": body.SHA256})
	})
	return mux
}

func newStagedClient(t *testing.T, fake *stagingFake) (*HTTP, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(fake.handler())
	t.Cleanup(ts.Close)
	return NewHTTP(ts.URL), ts
}

func TestStoreStagedStreamsHashesAndFinalizes(t *testing.T) {
	fake := &stagingFake{}
	client, _ := newStagedClient(t, fake)

	body := "fragmented mp4 bytes, final while appended"
	gotSHA, gotSize, err := client.StoreStaged(context.Background(), "job-1", strings.NewReader(body))
	if err != nil {
		t.Fatalf("StoreStaged: %v", err)
	}
	want := sha256.Sum256([]byte(body))
	wantSHA := hex.EncodeToString(want[:])
	if gotSHA != wantSHA {
		t.Fatalf("digest = %s, want %s", gotSHA, wantSHA)
	}
	if gotSize != int64(len(body)) {
		t.Fatalf("size = %d, want %d", gotSize, len(body))
	}
	if string(fake.body) != body {
		t.Fatalf("streamed body = %q, want %q", fake.body, body)
	}
	if !fake.finalizeCalled || fake.finalizeSHA != wantSHA {
		t.Fatalf("finalize assertion = %q (called=%v), want %q",
			fake.finalizeSHA, fake.finalizeCalled, wantSHA)
	}
}

// TestStoreStagedRefusesDigestMismatch pins the corruption rule: when the
// store reports a different digest than the client computed, the client must
// NOT finalize — the bytes would be named by the wrong address.
func TestStoreStagedRefusesDigestMismatch(t *testing.T) {
	fake := &stagingFake{reportedDigest: strings.Repeat("0", 64)}
	client, _ := newStagedClient(t, fake)

	if _, _, err := client.StoreStaged(context.Background(), "job-2", strings.NewReader("x")); err == nil {
		t.Fatal("StoreStaged succeeded despite a client/store digest mismatch")
	}
	if fake.finalizeCalled {
		t.Fatal("finalize was called for a mismatched stream")
	}
}

func TestStoreStagedSurfacesUploadFailure(t *testing.T) {
	fake := &stagingFake{uploadStatus: http.StatusServiceUnavailable}
	client, _ := newStagedClient(t, fake)

	if _, _, err := client.StoreStaged(context.Background(), "job-3", strings.NewReader("x")); err == nil {
		t.Fatal("StoreStaged succeeded despite HTTP 503 on upload")
	}
	if fake.finalizeCalled {
		t.Fatal("finalize was called after a failed upload")
	}
}

func TestStoreStagedSurfacesFinalizeFailure(t *testing.T) {
	fake := &stagingFake{finalizeStatus: http.StatusUnprocessableEntity}
	client, _ := newStagedClient(t, fake)

	if _, _, err := client.StoreStaged(context.Background(), "job-4", strings.NewReader("x")); err == nil {
		t.Fatal("StoreStaged succeeded despite HTTP 422 on finalize")
	}
}

func TestStoreStagedRequiresSession(t *testing.T) {
	fake := &stagingFake{}
	client, _ := newStagedClient(t, fake)
	if _, _, err := client.StoreStaged(context.Background(), "  ", strings.NewReader("x")); err == nil {
		t.Fatal("StoreStaged accepted an empty session id")
	}
}
