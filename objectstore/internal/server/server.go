// Package server exposes the object store over HTTP.
//
// Contract (matches the RenderingGen worker's HTTP L3 backend):
//
//	PUT /objects/{key} -> 201
//	GET /objects/{key} -> 200 body | 404
//	GET /health        -> 200
package server

import (
	"io"
	"net/http"
	"strconv"

	"github.com/Marcuss-ops/RenderginGen/objectstore/internal/store"
)

// Server wraps the store with HTTP handlers.
type Server struct {
	store *store.Store
}

// New creates a server backed by the given store.
func New(s *store.Store) *Server {
	return &Server{store: s}
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /objects/{key}", s.put)
	mux.HandleFunc("GET /objects/{key}", s.get)
	mux.HandleFunc("GET /health", s.health)
	return mux
}

func (s *Server) put(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if err := s.store.PutReader(key, r.Body, r.ContentLength); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	// Stream from disk: buffering a multi-GB rendered artifact in RAM per
	// request would make every download a worker memory spike.
	f, size, err := s.store.Open(key)
	if err == store.ErrNotFound {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	_, _ = io.Copy(w, f)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
