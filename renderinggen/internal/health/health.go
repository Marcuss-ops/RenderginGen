// Package health exposes the versioned worker health endpoint.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Info is the payload returned by /health.
type Info struct {
	Worker        string `json:"worker"`
	RenderingGen  string `json:"renderinggen"`
	Chronon       string `json:"chronon"`
	OverlaySchema int    `json:"overlay_schema"`
	Backend       string `json:"backend"`
	Status        string `json:"status"`
}

// Server serves the /health endpoint until ctx is cancelled.
type Server struct {
	info Info
	srv  *http.Server
}

// NewServer creates a health server for the given metadata.
func NewServer(addr string, info Info) *Server {
	mux := http.NewServeMux()
	s := &Server{info: info, srv: &http.Server{Addr: addr, Handler: mux}}
	mux.HandleFunc("/health", s.handle)
	return s
}

func (s *Server) handle(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.info)
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
