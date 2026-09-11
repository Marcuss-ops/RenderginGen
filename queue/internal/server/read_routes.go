// read_routes.go owns the read-only HTTP handlers: children, get, depth and
// health. None of them mutate queue state.
package server

import (
	"errors"
	"net/http"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

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
