// Package model defines the domain types shared by the queue, its HTTP server
// and every storage backend (in-memory and PostgreSQL).
package model

import (
	"encoding/json"
	"time"
)

// State is the lifecycle state of a job.
type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
)

// AssetRef points at an asset in the central artifact store.
type AssetRef struct {
	Hash string `json:"hash"`
	URL  string `json:"url"`
}

// Job is a unit of work in the queue.
type Job struct {
	ID          string          `json:"id"`
	OverlaySpec json.RawMessage `json:"overlay_spec"`
	Assets      []AssetRef      `json:"assets"`

	State      State     `json:"state"`
	Worker     string    `json:"worker,omitempty"`
	Attempts   int       `json:"attempts"`
	CreatedAt  time.Time `json:"created_at"`
	LeaseUntil time.Time `json:"lease_until,omitempty"`
	FailReason string    `json:"fail_reason,omitempty"`
}

// Stats is a snapshot of the queue, used for autoscaling and monitoring.
type Stats struct {
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Depth     int `json:"depth"`
}
