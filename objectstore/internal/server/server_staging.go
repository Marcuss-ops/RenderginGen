// server_staging.go — streaming staging routes for the object store.
//
//	PUT    /objects/staging/{session}           -> 201 {session, sha256, size_bytes}
//	POST   /objects/staging/{session}/finalize  -> 201 {ok, key} | 400 | 404 | 422
//	DELETE /objects/staging/{session}           -> 204 (idempotent)
//
// The flow serves the "bytes final while appended" producers (fragmented MP4
// overlay artifacts): the body streams to a PENDING object and the store
// reports the SHA-256 it computed FROM THE STREAM. Finalize then asserts the
// content address and only installs the bytes when the assertion matches —
// the same verify-before-install rule as PUT /objects/{key}, split across the
// moment the bytes arrive and the moment their address is known. The visible
// tree keeps the exact content-addressing invariant; staging is a private
// workspace and pending objects are never readable through GET/HEAD.
package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Marcuss-ops/RenderingGen/objectstore/internal/store"
)

// stagingBackend resolves the optional StagingBackend extension. A backend
// that does not implement it gets 501 on these routes — honest "not
// implemented", never a staging flow that silently skips verification.
func (s *Server) stagingBackend(w http.ResponseWriter) (store.StagingBackend, bool) {
	backend, ok := s.store.(store.StagingBackend)
	if !ok {
		http.Error(w, "staging not implemented by this backend", http.StatusNotImplemented)
		return nil, false
	}
	return backend, true
}

// putStaging streams the request body into a pending object. The response
// carries the digest the store computed while hashing the stream, which is
// the value finalize must assert.
func (s *Server) putStaging(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.stagingBackend(w)
	if !ok {
		return
	}
	session := r.PathValue("session")
	// Same hardening posture as PUT /objects/{key}: cap the streamed body so
	// an unbounded upload cannot fill the disk before any check fires.
	const maxObjectBytes = 5 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxObjectBytes)
	digest, size, err := backend.StagingWrite(session, r.Body)
	if err != nil {
		http.Error(w, err.Error(), stagingErrorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session":    session,
		"sha256":     digest,
		"size_bytes": size,
	})
}

// finalizeStaging installs the pending object under the asserted content
// address. A wrong assertion is the caller's mistake (422) and discards the
// pending bytes — there is deliberately no "install under the computed
// digest anyway" convenience, because the caller's address is the identity
// every later reader will trust.
func (s *Server) finalizeStaging(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.stagingBackend(w)
	if !ok {
		return
	}
	session := r.PathValue("session")
	var body struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "finalize requires a JSON body {\"sha256\": \"...\"}", http.StatusBadRequest)
		return
	}
	if err := backend.StagingFinalize(session, body.SHA256); err != nil {
		http.Error(w, err.Error(), stagingErrorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":  true,
		"key": body.SHA256,
	})
}

// abortStaging discards a pending object. Idempotent by contract: aborting a
// missing session answers 204, so a producer can always reach a clean state.
func (s *Server) abortStaging(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.stagingBackend(w)
	if !ok {
		return
	}
	if err := backend.StagingAbort(r.PathValue("session")); err != nil {
		http.Error(w, err.Error(), stagingErrorStatus(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stagingErrorStatus maps staging failures onto the same "who must act"
// vocabulary as putErrorStatus: 400/422 are caller mistakes that must not be
// retried with the same bytes, 404 is a missing/finished session, 413 an
// oversized body, everything else is this server's failure.
func stagingErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrInvalidContentAddress),
		errors.Is(err, store.ErrStagingSession):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrStagingNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrContentAddressMismatch):
		return http.StatusUnprocessableEntity
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}
