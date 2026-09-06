package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
)

func TestHealthEndpointReturnsInfo(t *testing.T) {
	info := Info{
		Worker:        "w1",
		RenderingGen:  "1.2.3",
		Chronon:       "0.9.4",
		OverlaySchema: 3,
		Backend:       "vulkan",
		Status:        "ready",
	}
	hs := NewServer(":0", info)
	ts := httptest.NewServer(hs.srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}

	var got Info
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got != info {
		t.Fatalf("got %+v, want %+v", got, info)
	}
}

func TestHealthEndpointQueueStatusOverride(t *testing.T) {
	hs := NewServer(":0", Info{Worker: "w1", Status: "ready"})
	hs.SetQueueStatus(func() string { return "degraded" })
	ts := httptest.NewServer(hs.srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got Info
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", got.Status)
	}
	if got.Worker != "w1" {
		t.Fatalf("worker = %q, want w1 (static fields must be preserved)", got.Worker)
	}
}

func TestHealthEndpointQueueStatusEmptyFallsBack(t *testing.T) {
	hs := NewServer(":0", Info{Worker: "w1", Status: "ready"})
	hs.SetQueueStatus(func() string { return "" })
	ts := httptest.NewServer(hs.srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got Info
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" {
		t.Fatalf("status = %q, want static fallback ready", got.Status)
	}
}

func TestProgressEndpointReportsRender(t *testing.T) {
	hs := NewServer(":0", Info{Worker: "w1"})
	hs.SetProgressFunc(func() *chronon.Progress {
		return &chronon.Progress{
			JobID:       "video-983",
			FramesDone:  485,
			FramesTotal: 1800,
			Percent:     26.9,
			FPS:         18.4,
		}
	})
	ts := httptest.NewServer(hs.srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/progress")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got struct {
		Status      string  `json:"status"`
		JobID       string  `json:"job_id"`
		Stage       string  `json:"stage"`
		FramesDone  int64   `json:"frames_done"`
		FramesTotal int64   `json:"frames_total"`
		Progress    float64 `json:"progress"`
		FPS         float64 `json:"fps"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "rendering" || got.JobID != "video-983" || got.Stage != "chronon_render" {
		t.Fatalf("unexpected payload: %+v", got)
	}
	if got.FramesDone != 485 || got.FramesTotal != 1800 || got.Progress != 26.9 || got.FPS != 18.4 {
		t.Fatalf("unexpected numbers: %+v", got)
	}
}

func TestProgressEndpointIdle(t *testing.T) {
	hs := NewServer(":0", Info{})
	ts := httptest.NewServer(hs.srv.Handler)
	defer ts.Close()

	// No tracker installed: idle.
	resp, err := http.Get(ts.URL + "/progress")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "idle" {
		t.Fatalf("want idle, got %v", got)
	}

	// Tracker installed but no render observed: still idle.
	hs.SetProgressFunc(chronon.NewProgressTracker().Current)
	resp2, err := http.Get(ts.URL + "/progress")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var got2 map[string]string
	if err := json.NewDecoder(resp2.Body).Decode(&got2); err != nil {
		t.Fatal(err)
	}
	if got2["status"] != "idle" {
		t.Fatalf("want idle with empty tracker, got %v", got2)
	}
}

func TestUnknownPathNotFound(t *testing.T) {
	hs := NewServer(":0", Info{})
	ts := httptest.NewServer(hs.srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/other")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}
