package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

func (s *Server) submitBatch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBytes)
	var req struct {
		Jobs []model.Job `json:"jobs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Jobs) == 0 {
		http.Error(w, "jobs is required", http.StatusBadRequest)
		return
	}
	for _, job := range req.Jobs {
		if err := client.ValidateJobID(job.ID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := client.ValidateJobMetadata(job); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := s.svc.SubmitBatch(r.Context(), req.Jobs); err != nil {
		if errors.Is(err, repository.ErrJobExists) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
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
	if err := client.ValidateJobID(job.ID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := client.ValidateJobMetadata(job); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	canonical, created, err := s.svc.SubmitIdempotent(r.Context(), job)
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
func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "job-" + hex.EncodeToString(b)
}
