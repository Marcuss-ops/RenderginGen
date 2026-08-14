package model

import "time"

// WorkerStatus is the lifecycle status of a rendering worker.
type WorkerStatus string

const (
	WorkerStatusUnknown  WorkerStatus = "unknown"
	WorkerStatusReady    WorkerStatus = "ready"
	WorkerStatusBusy     WorkerStatus = "busy"
	WorkerStatusDraining WorkerStatus = "draining"
	WorkerStatusOffline  WorkerStatus = "offline"
)

// Worker is a registered rendering worker. It is the registry row for the
// GPU/Chronon worker; LastHeartbeatAt doubles as its liveness ledger so a
// worker that stops heartbeating can be drained.
type Worker struct {
	ID                   string       `json:"id"`
	Hostname             string       `json:"hostname,omitempty"`
	Status               WorkerStatus `json:"status"`
	RenderingGenVersion  string       `json:"renderinggen_version,omitempty"`
	ChrononVersion       string       `json:"chronon_version,omitempty"`
	OverlaySchemaVersion int          `json:"overlay_schema_version,omitempty"`
	GPUBackend           string       `json:"gpu_backend,omitempty"`
	GPUDevice            string       `json:"gpu_device,omitempty"`
	GPUDriver            string       `json:"gpu_driver,omitempty"`
	StartedAt            time.Time    `json:"started_at,omitempty"`
	LastHeartbeatAt      time.Time    `json:"last_heartbeat_at,omitempty"`
}

// WorkerHealth is the aggregate worker-health snapshot used for autoscaling
// and monitoring. Ready/Busy count only workers with a fresh heartbeat;
// Offline counts workers whose heartbeat has gone stale.
type WorkerHealth struct {
	Ready   int `json:"ready"`
	Busy    int `json:"busy"`
	Offline int `json:"offline"`
	Total   int `json:"total"`
}
