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
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.Put(key, body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	data, err := s.store.Get(key)
	if err == store.ErrNotFound {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(data)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
