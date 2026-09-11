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
//	GET  /jobs/{id}/wait        long-poll until the job is terminal (producer)
//	GET  /jobs/depth            queue depth/stats (autoscaling)
//	GET  /health                health check
//
// File layout: server.go owns routing + submit/claim, transition_routes.go the
// job-transition handlers, read_routes.go the read-only handlers and
// workers.go the worker registry endpoints.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/service"
)

const maxClaimWait = 25 * time.Second

const maxSubmitBytes = 10 << 20 // 10 MiB: large semantic plans + asset refs; unbounded is a hardening gap

// Server wraps the job service with HTTP handlers. Wake-up signaling for
// long-poll claims lives in one place — the service's Notifier — so the
// submit/claim/complete transitions and both claim endpoints share a single
// broadcast primitive (see WaitAndClaim).
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
	mux.HandleFunc("POST /jobs/claim/wait", s.claimWait)
	mux.HandleFunc("POST /jobs/{id}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/complete", s.complete)
	mux.HandleFunc("POST /jobs/{id}/rendered", s.rendered)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/rendered", s.rendered)
	mux.HandleFunc("POST /jobs/{id}/fail", s.fail)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/fail", s.fail)
	mux.HandleFunc("POST /jobs/{id}/retry", s.retry)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/retry", s.retry)
	mux.HandleFunc("POST /jobs/{id}/cancel", s.cancel)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/cancel", s.cancel)
	mux.HandleFunc("POST /jobs/{id}/renew", s.renew)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/renew", s.renew)
	mux.HandleFunc("POST /jobs/{id}/progress", s.progress)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/progress", s.progress)
	mux.HandleFunc("POST /jobs/{id}/finalize/claim", s.claimFinalization)
	mux.HandleFunc("POST /jobs/{parent}/{lang}/finalize/claim", s.claimFinalization)
	mux.HandleFunc("GET /jobs/{id}/children", s.children)
	mux.HandleFunc("GET /jobs/{parent}/{lang}/children", s.children)
	mux.HandleFunc("GET /jobs/{id}/wait", s.waitJob)
	mux.HandleFunc("GET /jobs/{parent}/{lang}/wait", s.waitJob)
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
	r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBytes)
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
		// 409 is reserved for the duplicate-ID condition. Producers treat 409
		// as idempotent success (queue/client maps it to ErrJobExists), so a
		// transient storage failure must never be reported as 409: that would
		// silently drop the job. Everything else is a server error.
		if errors.Is(err, repository.ErrJobExists) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// SubmitIdempotent already notified claim waiters on creation.
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]string{"id": canonical.ID})
}

// claim is the thin polling endpoint. With wait_ms==0 it is a single atomic
// ClaimState; with wait_ms>0 it delegates to the single Notifier-backed
// WaitAndClaim so both claim endpoints share one wake-up path. The wait never
// assigns work — every wake just re-runs the atomic claim (SKIP LOCKED).
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
		// Delegate the wait to the service: it re-runs the atomic claim on
		// every wake-up and bounded re-poll, on the single shared Notifier.
		waitCtx, cancel := context.WithTimeout(r.Context(), wait+5*time.Second)
		defer cancel()
		job, lease, err = s.svc.WaitAndClaim(waitCtx, req.Worker, model.State(req.State), wait)
		if err != nil {
			if r.Context().Err() != nil {
				return // client disconnected; nothing to write
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	writeJSON(w, http.StatusOK, buildClaimResponse(job, lease))
}

func buildClaimResponse(job *model.Job, lease time.Duration) claimResponse {
	return claimResponse{
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
	}
}

// claimWait is the dedicated long-poll endpoint. It is a thin wrapper over
// the same service WaitAndClaim as claim's wait_ms path — both share the
// single Notifier broadcast (submit/complete/fail/requeue all Notify). The
// only difference is wire naming (max_wait_ms vs wait_ms) and default wait.
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
	writeJSON(w, http.StatusOK, buildClaimResponse(job, lease))
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
