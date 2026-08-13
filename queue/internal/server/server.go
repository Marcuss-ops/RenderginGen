// Package server exposes the job queue over HTTP.
//
// Contract (matches the RenderingGen worker client):
//
//	POST /jobs                  submit a job (PipelineGen)
//	POST /jobs/claim            claim the next job (worker pulls)
//	POST /jobs/{id}/complete    report completion
//	POST /jobs/{id}/fail        report failure (requeue or fail permanently)
//	GET  /jobs/depth            queue depth/stats (autoscaling)
//	GET  /health                health check
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/store"
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
	mux.HandleFunc("POST /jobs", s.submit)
	mux.HandleFunc("POST /jobs/claim", s.claim)
	mux.HandleFunc("POST /jobs/{id}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{id}/fail", s.fail)
	mux.HandleFunc("GET /jobs/depth", s.depth)
	mux.HandleFunc("GET /health", s.health)
	return mux
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	var job store.Job
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if job.ID == "" {
		job.ID = newID()
	}
	if err := s.store.Submit(job); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": job.ID})
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Worker string `json:"worker"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	job, lease, err := s.store.Claim(req.Worker)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	writeJSON(w, http.StatusOK, claimResponse{
		ID:          job.ID,
		OverlaySpec: job.OverlaySpec,
		Assets:      job.Assets,
		Lease:       lease,
	})
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Worker string `json:"worker"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := s.store.Complete(id, req.Worker); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Worker string `json:"worker"`
		Data   struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := s.store.Fail(id, req.Worker, req.Data.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) depth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Stats())
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// claimResponse is the payload returned to a worker on claim.
type claimResponse struct {
	ID          string           `json:"id"`
	OverlaySpec json.RawMessage  `json:"overlay_spec"`
	Assets      []store.AssetRef `json:"assets"`
	Lease       time.Duration    `json:"lease"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "job-" + hex.EncodeToString(b)
}
