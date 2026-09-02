package server

import "github.com/Marcuss-ops/RenderginGen/queue/internal/model"

// NotifyState wakes long-poll claimers after an external queue notification
// (for example PostgreSQL LISTEN/NOTIFY). It never assigns a job: claimers
// still re-read the database and compete through SKIP LOCKED.
func (s *Server) NotifyState(state model.State) {
	if state != model.StatePending && state != model.StateRendered {
		return
	}
	s.signal(state)
}
