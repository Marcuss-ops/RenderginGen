package service

import (
	"fmt"
	"log"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// RegisterWorker upserts a worker's identity and records its initial
// heartbeat, then refreshes the worker-health metrics.
func (s *Service) RegisterWorker(worker model.Worker) error {
	if s.workerRepo == nil {
		return fmt.Errorf("worker repository is not configured")
	}
	if err := s.workerRepo.Register(worker); err != nil {
		return err
	}
	s.observeWorkerHealth()
	return nil
}

// WorkerHeartbeat records a heartbeat for a registered worker, then refreshes
// the worker-health metrics.
func (s *Service) WorkerHeartbeat(workerID string) error {
	if s.workerRepo == nil {
		return fmt.Errorf("worker repository is not configured")
	}
	if err := s.workerRepo.Heartbeat(workerID); err != nil {
		return err
	}
	s.observeWorkerHealth()
	return nil
}

// ListWorkers returns all registered workers.
func (s *Service) ListWorkers() ([]model.Worker, error) {
	if s.workerRepo == nil {
		return nil, fmt.Errorf("worker repository is not configured")
	}
	return s.workerRepo.List()
}

// WorkerHealth returns the aggregate worker-health snapshot.
func (s *Service) WorkerHealth() (model.WorkerHealth, error) {
	if s.workerRepo == nil {
		return model.WorkerHealth{}, fmt.Errorf("worker repository is not configured")
	}
	return s.workerRepo.Health(time.Now(), s.staleAfter)
}

// RefreshWorkerHealth recomputes the worker-health metrics from the current
// registry snapshot. It is called on register/heartbeat and periodically by
// the server so the ready/offline gauges decay as heartbeats age.
func (s *Service) RefreshWorkerHealth() {
	s.observeWorkerHealth()
}

func (s *Service) observeWorkerHealth() {
	if s.workerRepo == nil || s.metrics == nil {
		return
	}
	h, err := s.workerRepo.Health(time.Now(), s.staleAfter)
	if err != nil {
		log.Printf("worker health query failed: %v", err)
		// Do not freeze gauges at stale values: on query failure the last
		// ready/offline snapshot is no longer trustworthy for autoscaling.
		// Reset to 0 so dashboards do not show phantom ready workers while
		// the DB is unreachable.
		if s.metrics != nil {
			s.metrics.WorkersReady.Set(0)
			s.metrics.WorkersOffline.Set(0)
		}
		return
	}
	s.metrics.WorkersReady.Set(float64(h.Ready))
	s.metrics.WorkersOffline.Set(float64(h.Offline))
}
