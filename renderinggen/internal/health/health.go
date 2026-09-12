// Package health exposes the versioned worker health endpoint.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
)

// Info is the payload returned by /health.
type Info struct {
	Worker        string `json:"worker"`
	RenderingGen  string `json:"renderinggen"`
	Chronon       string `json:"chronon"`
	OverlaySchema int    `json:"overlay_schema"`
	Backend       string `json:"backend"`
	Status        string `json:"status"`
	// Degradations carries process-cumulative fail-open degradations (for
	// example workspaces that could not be removed). Those phases never fail a
	// render by design, so without this map the worker would report "ready"
	// while leaking its tmpfs scratch space. Omitted when there is nothing to
	// report, so a healthy worker's payload is unchanged.
	Degradations map[string]int64 `json:"degradations,omitempty"`
}

// DegradationFunc reports the current fail-open degradation counters. Called
// per request so /health is always live. A nil map means "no degradation".
type DegradationFunc func() map[string]int64

// ProgressFunc returns the current render progress snapshot, or nil when no
// render is in flight. Called per request so /progress is always live.
type ProgressFunc func() *chronon.Progress

// Server serves the /health endpoint until ctx is cancelled.
type Server struct {
	info Info
	srv  *http.Server
	// progress, when set, exposes GET /progress (live render position).
	progress ProgressFunc
	// queueStatus, when set, overrides Info.Status on every /health response
	// (e.g. "ready" -> "degraded" while the queue is unreachable).
	queueStatus func() string
	// degradations, when set, is attached to every /health response.
	degradations DegradationFunc
}

// NewServer creates a health server for the given metadata.
func NewServer(addr string, info Info) *Server {
	mux := http.NewServeMux()
	s := &Server{info: info, srv: &http.Server{Addr: addr, Handler: mux}}
	mux.HandleFunc("/health", s.handle)
	mux.HandleFunc("/progress", s.handleProgress)
	return s
}

// SetProgressFunc installs the live render progress source for GET /progress.
func (s *Server) SetProgressFunc(fn ProgressFunc) { s.progress = fn }

// SetQueueStatus installs a live queue-connectivity reporter. When set, the
// string it returns replaces the static Status field on every /health
// response; return "" to fall back to the static value.
func (s *Server) SetQueueStatus(fn func() string) { s.queueStatus = fn }

// SetDegradationFunc installs the fail-open degradation reporter. When set,
// its non-empty result is attached to every /health response.
func (s *Server) SetDegradationFunc(fn DegradationFunc) { s.degradations = fn }

func (s *Server) handle(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	info := s.info
	if s.queueStatus != nil {
		if st := s.queueStatus(); st != "" {
			info.Status = st
		}
	}
	if s.degradations != nil {
		info.Degradations = s.degradations()
	}
	_ = json.NewEncoder(w).Encode(info)
}

// handleProgress serves the current render progress. When no render is in
// flight (or no tracker is installed) it answers 200 with status "idle".
func (s *Server) handleProgress(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.progress == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "idle"})
		return
	}
	p := s.progress()
	if p == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "idle"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":        "rendering",
		"job_id":        p.JobID,
		"stage":         "chronon_render",
		"frames_done":   p.FramesDone,
		"frames_total":  p.FramesTotal,
		"progress":      p.Percent,
		"fps":           p.FPS,
		"last_frame_at": p.LastFrameAt.UTC().Format(time.RFC3339Nano),
	})
}

// Run blocks until ctx is cancelled, then shuts down.
func (s *Server) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	}()
	err := s.srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
