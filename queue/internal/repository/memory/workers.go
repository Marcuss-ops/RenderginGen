package memory

import (
	"fmt"
	"sort"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
)

// Register upserts a worker's identity and records its initial heartbeat.
func (s *Repository) Register(worker model.Worker) error {
	if worker.ID == "" {
		return fmt.Errorf("worker id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	existing, ok := s.workers[worker.ID]
	if !ok {
		existing = worker
		existing.StartedAt = now
	} else {
		// Refresh identity fields but preserve the original start time.
		existing.Hostname = worker.Hostname
		existing.Status = worker.Status
		existing.RenderingGenVersion = worker.RenderingGenVersion
		existing.ChrononVersion = worker.ChrononVersion
		existing.OverlaySchemaVersion = worker.OverlaySchemaVersion
		existing.GPUBackend = worker.GPUBackend
		existing.GPUDevice = worker.GPUDevice
		existing.GPUDriver = worker.GPUDriver
	}
	existing.LastHeartbeatAt = now
	s.workers[worker.ID] = existing
	return nil
}

// Heartbeat records a heartbeat for a registered worker.
func (s *Repository) Heartbeat(workerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	worker, ok := s.workers[workerID]
	if !ok {
		return fmt.Errorf("worker %s is not registered", workerID)
	}
	worker.LastHeartbeatAt = time.Now()
	s.workers[workerID] = worker
	return nil
}

// List returns all registered workers sorted by ID.
func (s *Repository) List() ([]model.Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]model.Worker, 0, len(s.workers))
	for _, w := range s.workers {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Health returns the aggregate worker-health snapshot.
func (s *Repository) Health(now time.Time, staleAfter time.Duration) (model.WorkerHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var h model.WorkerHealth
	for _, w := range s.workers {
		h.Total++
		if w.LastHeartbeatAt.IsZero() || now.Sub(w.LastHeartbeatAt) > staleAfter {
			h.Offline++
			continue
		}
		switch w.Status {
		case model.WorkerStatusReady:
			h.Ready++
		case model.WorkerStatusBusy:
			h.Busy++
		}
	}
	return h, nil
}
