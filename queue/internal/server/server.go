// Package server exposes the job queue over HTTP.
//
// Contract (matches the RenderingGen worker client):
//
//	POST /jobs                  submit a job (PipelineGen)
//	POST /jobs/claim            claim the next job (worker pulls)
//	POST /jobs/{id}/complete    report completion
//	POST /jobs/{id}/fail        report failure (requeue or fail permanently)
//	POST /jobs/{id}/renew       extend the lease during a long render
//	GET  /jobs/depth            queue depth/stats (autoscaling)
//	GET  /health                health check
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/service"
)

// Server wraps the job service with HTTP handlers.
type Server struct {
	svc            *service.Service
	metricsHandler http.Handler
}

// New creates a server backed by the given job service.
func New(s *service.Service) *Server {
	return &Server{svc: s}
}

// SetMetricsHandler attaches an optional Prometheus exposition handler served
// at GET /metrics. It is a no-op when the handler is nil.
func (s *Server) SetMetricsHandler(h http.Handler) {
	s.metricsHandler = h
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs", s.submit)
	mux.HandleFunc("POST /jobs/claim", s.claim)
	mux.HandleFunc("POST /jobs/{id}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{id}/fail", s.fail)
	mux.HandleFunc("POST /jobs/{id}/renew", s.renew)
	mux.HandleFunc("GET /jobs/{id}", s.get)
	mux.HandleFunc("GET /jobs/depth", s.depth)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /workers/register", s.registerWorker)
	mux.HandleFunc("POST /workers/heartbeat", s.heartbeat)
	mux.HandleFunc("GET /workers", s.listWorkers)
	mux.HandleFunc("GET /workers/health", s.workerHealth)
	if s.metricsHandler != nil {
		mux.Handle("GET /metrics", s.metricsHandler)
	}
	return mux
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	var job model.Job
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if job.ID == "" {
		job.ID = newID()
	}
	if err := s.svc.Submit(job); err != nil {
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

	job, lease, err := s.svc.Claim(req.Worker)
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
		Worker string         `json:"worker"`
		Data   model.Artifact `json:"data"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := s.svc.Complete(id, req.Worker, req.Data); err != nil {
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

	if err := s.svc.Fail(id, req.Worker, req.Data.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) renew(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Worker string `json:"worker"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := s.svc.Renew(id, req.Worker); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.svc.Get(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) depth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.Stats())
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// claimResponse is the payload returned to a worker on claim.
type claimResponse struct {
	ID          string           `json:"id"`
	OverlaySpec json.RawMessage  `json:"overlay_spec"`
	Assets      []model.AssetRef `json:"assets"`
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
