// Package server exposes the object store over HTTP.
//
// Contract (matches the RenderingGen worker's HTTP L3 backend):
//
//	PUT  /objects/{key}        -> 201 | 400 malformed content address | 413 too large | 422 bytes do not hash to key
//	GET  /objects/{key}        -> 200 body | 400 malformed content address | 404
//	HEAD /objects/{key}        -> 200 | 400 malformed content address | 404
//	GET  /objects/{key}/verify -> 200 (verified) | 400 | 404 | 409 (stored bytes contradict the address)
//	GET  /health               -> 200 {"status", "store", "build"}
//
// {key} is a content address, not a name: it must be the lowercase SHA-256 hex
// digest of the body. The store (internal/store) recomputes the digest while
// streaming the upload and refuses to install anything that disagrees, so 201
// is a statement about the BYTES and never about the key. A caller that
// publishes the wrong bytes under a valid address gets 422 and leaves no
// object behind, instead of poisoning the address for every later reader.
//
// GET/HEAD stay cheap existence probes (they never re-read the bytes), so
// /verify is the explicit way to ask the stronger question — "are the bytes
// stored here actually the ones this address names?" — which is what an
// operator needs after a volume restore or a disk migration.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/buildinfo"
	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/store"
)

// Server wraps the store with HTTP handlers.
type Server struct {
	store *store.Store
	// build, when set, supplies the runtime build identity published as the
	// "build" object on /health. A function rather than a value: the executable
	// digest is computed once and memoized, and a live accessor keeps the
	// handler free of construction-order coupling with the composition root.
	build func() buildinfo.Identity
}

// New creates a server backed by the given store.
func New(s *store.Store) *Server {
	return &Server{store: s}
}

// SetBuildInfo installs the runtime build-identity accessor. The composition
// root wires buildinfo.Current; tests may pass a fixed tuple. Until it is
// wired, /health omits the field rather than publishing an empty document.
func (s *Server) SetBuildInfo(fn func() buildinfo.Identity) {
	s.build = fn
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /objects/{key}", s.put)
	mux.HandleFunc("GET /objects/{key}", s.get)
	// HEAD is the cheap existence probe used by uploaders (PipelineGen's
	// clip-executor prefetch) to skip re-uploading an object that is already
	// staged under its content address. 200 is trustworthy precisely because
	// PUT verifies the digest before installing: an object reachable under a
	// key was proven to hash to that key, so the probe does not have to re-read
	// the (possibly multi-GB) bytes to answer.
	mux.HandleFunc("HEAD /objects/{key}", s.head)
	// /verify re-hashes the stored object. It is a separate route (GET/HEAD
	// answer from the directory entry alone) so the expensive check is always
	// an explicit request, never a surprise on the hot read path.
	mux.HandleFunc("GET /objects/{key}/verify", s.verify)
	mux.HandleFunc("GET /health", s.health)
	return mux
}

// verify answers whether the bytes stored under key hash to key. 409 means the
// store is holding bytes that contradict their own address: the caller must
// re-publish the object (or restore it), and any reader that already trusted
// it may have used the wrong bytes.
func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	check, err := s.store.Verify(key)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case errors.Is(err, store.ErrInvalidContentAddress):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrIntegrityViolation):
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":         false,
			"key":        key,
			"sha256":     check.SHA256,
			"size_bytes": check.SizeBytes,
			"reason":     check.Reason,
		})
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":         true,
		"key":        key,
		"sha256":     check.SHA256,
		"size_bytes": check.SizeBytes,
	})
}

func (s *Server) head(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	f, size, err := s.store.Open(key)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, store.ErrInvalidContentAddress) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	f.Close()
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) put(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	// Cap PUT body: object store is trusted-network but unbounded streaming
	// after full write before Content-Length mismatch is detected is a
	// hardening gap. 5 GiB covers any rendered segment; anything larger is
	// a bug/attack. LimitReader ensures we fail fast instead of filling disk.
	const maxObjectBytes = 5 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxObjectBytes)
	if err := s.store.PutReader(key, r.Body, r.ContentLength); err != nil {
		http.Error(w, err.Error(), putErrorStatus(err))
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// putErrorStatus maps a store write failure onto the status that describes WHO
// must act. A malformed key or content that disagrees with its address is the
// caller's mistake and must not be retried (400/422); an oversized body is
// rejected before it can fill the disk (413); everything else is this server's
// failure (500). Returning 5xx for a content mismatch — the historical
// behaviour — invited a producer to retry the same wrong bytes forever.
func putErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrInvalidContentAddress):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrContentAddressMismatch):
		return http.StatusUnprocessableEntity
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	// Stream from disk: buffering a multi-GB rendered artifact in RAM per
	// request would make every download a worker memory spike.
	f, _, err := s.store.Open(key)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, store.ErrInvalidContentAddress) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, key, info.ModTime(), f)
}

// health answers two questions in one request, because a probe that asks
// "are you up?" should not have to make a second call to ask "and which code
// and which contract am I talking to?":
//
//	build  the revision, executable digest and start time of THIS process
//	store  the addressing/verification guarantee this store implements
//
// The store block is a capability declaration, not a claim about the data:
// verify_on_write is true because the write path recomputes the digest, and
// verify_on_read is true because /verify and Scan exist. Neither says the
// bytes on disk are currently correct — that is what the verification surface
// is FOR, and asserting it here would be exactly the "looks verified, is not"
// failure the store was built to remove.
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{
		"status": "ok",
		"store":  s.store.Capabilities(),
	}
	if s.build != nil {
		identity := s.build()
		payload["build"] = identity
	}
	_ = json.NewEncoder(w).Encode(payload)
}
