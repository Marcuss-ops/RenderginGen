package model

import "github.com/Marcuss-ops/RenderingGen/queue/client"

// WorkerStatus is the lifecycle status of a rendering worker. It is an ALIAS
// of the public wire contract type (queue/client), like every other type on
// this boundary: the worker registers through the same declaration the queue
// service validates and stores, so the vocabulary cannot drift.
type WorkerStatus = client.WorkerStatus

const (
	WorkerStatusUnknown  = client.WorkerStatusUnknown
	WorkerStatusReady    = client.WorkerStatusReady
	WorkerStatusBusy     = client.WorkerStatusBusy
	WorkerStatusDraining = client.WorkerStatusDraining
	WorkerStatusOffline  = client.WorkerStatusOffline
)

// Worker is a registered rendering worker: the registry row for the
// GPU/Chronon worker. LastHeartbeatAt doubles as its liveness ledger so a
// worker that stops heartbeating can be drained. It is an ALIAS of the public
// wire type.
type Worker = client.Worker

// WorkerHealth is the aggregate worker-health snapshot used for autoscaling
// and monitoring. Ready/Busy count only workers with a fresh heartbeat;
// Offline counts workers whose heartbeat has gone stale. It is an ALIAS of the
// public wire type.
type WorkerHealth = client.WorkerHealth
