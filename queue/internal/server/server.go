// Package server exposes the job queue over HTTP.
//
// Contract (matches the RenderingGen worker client):
//
//	POST /jobs                  submit a job (PipelineGen)
//	POST /jobs/claim            claim the next job (worker pulls)
//	POST /jobs/{id}/complete    report completion
//	POST /jobs/{id}/fail        report failure (requeue or fail permanently)
//	POST /jobs/{id}/renew       extend the lease during a long render
//	POST /jobs/{id}/progress    report render progress (frames done/total)
//	GET  /jobs/depth            queue depth/stats (autoscaling)
//	GET  /health                health check
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/service"
)

const maxClaimWait = 25 * time.Second

type wakeSignal struct {
	mu sync.Mutex
	ch chan struct{}
}

func newWakeSignal() *wakeSignal {
	return &wakeSignal{ch: make(chan struct{})}
}

// signal broadcasts an edge to every current waiter. Replacing the closed
// channel immediately also means callers that arrive after the edge simply do
// an atomic claim first and only wait if the database is actually empty.
func (w *wakeSignal) signal() {
	w.mu.Lock()
	close(w.ch)
	w.ch = make(chan struct{})
	w.mu.Unlock()
}

func (w *wakeSignal) channel() <-chan struct{} {
	w.mu.Lock()
	ch := w.ch
	w.mu.Unlock()
	return ch
}

// Server wraps the job service with HTTP handlers.
type Server struct {
	svc            *service.Service
	metricsHandler http.Handler
	pendingWake    *wakeSignal
	renderedWake   *wakeSignal
}

// New creates a server backed by the given job service.
func New(s *service.Service) *Server {
	return &Server{
		svc:          s,
		pendingWake:  newWakeSignal(),
		renderedWake: newWakeSignal(),
	}
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
	mux.HandleFunc("POST /jobs/claim/wait", s.claimWait)
	mux.HandleFunc("POST /jobs/{id}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{id}/rendered", s.rendered)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/rendered", s.rendered)
	mux.HandleFunc("POST /jobs/{id}/fail", s.fail)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/fail", s.fail)
	mux.HandleFunc("POST /jobs/{id}/retry", s.retry)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/retry", s.retry)
	mux.HandleFunc("POST /jobs/{id}/renew", s.renew)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/renew", s.renew)
	mux.HandleFunc("POST /jobs/{id}/progress", s.progress)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/progress", s.progress)
	mux.HandleFunc("POST /jobs/{id}/finalize/claim", s.claimFinalization)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/finalize/claim", s.claimFinalization)
	mux.HandleFunc("GET /jobs/{id}/children", s.children)
	mux.HandleFunc("GET /jobs/{parent}/{lang}/children", s.children)
	mux.HandleFunc("GET /jobs/{id}", s.get)
	mux.HandleFunc("GET /jobs/{parent}/{lang}", s.get)
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

func parseJobID(r *http.Request) string {
	if parent := r.PathValue("parent"); parent != "" {
		if lang := r.PathValue("lang"); lang != "" {
			return parent + "/" + lang
		}
	}
	return r.PathValue("id")
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
	canonical, created, err := s.svc.SubmitIdempotent(job)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if created {
		s.signal(model.StatePending)
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]string{"id": canonical.ID})
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Worker string `json:"worker"`
		State  string `json:"state,omitempty"`
		WaitMS int64  `json:"wait_ms,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	job, lease, err := s.svc.ClaimState(req.Worker, model.State(req.State))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if job == nil && req.WaitMS > 0 {
		wait := time.Duration(req.WaitMS) * time.Millisecond
		if wait > maxClaimWait {
			wait = maxClaimWait
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		case <-s.wakeChannel(model.State(req.State)):
		}
		job, lease, err = s.svc.ClaimState(req.Worker, model.State(req.State))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	writeJSON(w, http.StatusOK, claimResponse{
		ID:             job.ID,
		Schema:         job.Schema,
		Version:        job.Version,
		IdempotencyKey: job.IdempotencyKey,
		JobType:        job.JobType,
		ParentJobID:    job.ParentJobID,
		ChunkIndex:     job.ChunkIndex,
		FrameRange:     job.FrameRange,
		RenderPlan:     job.RenderPlan,
		Assets:         job.Assets,
		Lease:          lease,
		State:          job.State,
		Artifact:       job.Artifact,
	})
}

// signal only wakes workers; the job table remains the source of truth and
// every awakened worker still competes through the repository's atomic
// SELECT ... FOR UPDATE SKIP LOCKED claim.
func (s *Server) signal(state model.State) {
	if state == model.StateRendered {
		s.renderedWake.signal()
		return
	}
	s.pendingWake.signal()
}

func (s *Server) wakeChannel(state model.State) <-chan struct{} {
	if state == model.StateRendered {
		return s.renderedWake.channel()
	}
	return s.pendingWake.channel()
}

func (s *Server) claimWait(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Worker    string `json:"worker"`
		State     string `json:"state,omitempty"`
		MaxWaitMs int64  `json:"max_wait_ms,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	maxWait := time.Duration(req.MaxWaitMs) * time.Millisecond
	ctx, cancel := context.WithTimeout(r.Context(), maxWait+5*time.Second)
	defer cancel()
	job, lease, err := s.svc.WaitAndClaim(ctx, req.Worker, model.State(req.State), maxWait)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, claimResponse{
		ID:             job.ID,
		Schema:         job.Schema,
		Version:        job.Version,
		IdempotencyKey: job.IdempotencyKey,
		JobType:        job.JobType,
		ParentJobID:    job.ParentJobID,
		ChunkIndex:     job.ChunkIndex,
		FrameRange:     job.FrameRange,
		RenderPlan:     job.RenderPlan,
		Assets:         job.Assets,
		Lease:          lease,
		State:          job.State,
		Artifact:       job.Artifact,
	})
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
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
	id := parseJobID(r)
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

func (s *Server) retry(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	if err := s.svc.Retry(id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.signal(model.StatePending)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rendered(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	var req struct {
		Worker string `json:"worker"`
		Data   struct {
			Reason   string         `json:"reason"`
			Artifact model.Artifact `json:"artifact"`
		} `json:"data"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := s.svc.Rendered(id, req.Worker, req.Data.Artifact, req.Data.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// progress records render progress reported by the lease-owning worker. It
// mirrors renew's conflict semantics: a stale worker's report is a 409, not a
// corruption vector.
func (s *Server) progress(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
	var req struct {
		Worker string         `json:"worker"`
		Data   model.Progress `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Worker == "" {
		http.Error(w, "worker is required", http.StatusBadRequest)
		return
	}
	if err := s.svc.SetProgress(id, req.Worker, req.Data); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) renew(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
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

func (s *Server) claimFinalization(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Worker string `json:"worker"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	job, claimed, err := s.svc.ClaimFinalization(parseJobID(r), req.Worker)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if !claimed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) children(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.svc.Children(parseJobID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	id := parseJobID(r)
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
	ID             string            `json:"id"`
	Schema         string            `json:"schema,omitempty"`
	Version        int               `json:"version,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	JobType        string            `json:"job_type,omitempty"`
	ParentJobID    string            `json:"parent_job_id,omitempty"`
	ChunkIndex     int               `json:"chunk_index,omitempty"`
	FrameRange     *model.FrameRange `json:"frame_range,omitempty"`

	RenderPlan json.RawMessage  `json:"render_plan"`
	Assets     []model.AssetRef `json:"assets"`
	Lease      time.Duration    `json:"lease"`

	// State and Artifact are populated on claim so a worker re-claiming a
	// rendered job can skip rendering and only retry publication.
	State    model.State     `json:"state,omitempty"`
	Artifact *model.Artifact `json:"artifact,omitempty"`
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
