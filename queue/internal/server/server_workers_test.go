package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/repository/memory"
	"github.com/Marcuss-ops/RenderginGen/queue/internal/service"
)

func newWorkerServer(t *testing.T) *httptest.Server {
	t.Helper()
	repo := memory.New(30*time.Second, 3)
	svc := service.New(repo)
	svc.SetWorkerRepository(repo, 90*time.Second)
	ts := httptest.NewServer(New(svc).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestWorkerEndpoints(t *testing.T) {
	ts := newWorkerServer(t)

	if resp := post(t, ts.URL+"/workers/register", `{"id":"w1","hostname":"h1","status":"ready"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("register: want 204, got %d", resp.StatusCode)
	}
	if resp := post(t, ts.URL+"/workers/heartbeat", `{"worker":"w1"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("heartbeat: want 204, got %d", resp.StatusCode)
	}
	if resp := post(t, ts.URL+"/workers/heartbeat", `{"worker":"missing"}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown heartbeat: want 404, got %d", resp.StatusCode)
	}

	resp, err := http.Get(ts.URL + "/workers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: want 200, got %d", resp.StatusCode)
	}
	var workers []struct {
		ID       string `json:"id"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&workers); err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 || workers[0].ID != "w1" || workers[0].Hostname != "h1" {
		t.Fatalf("unexpected workers: %+v", workers)
	}

	resp, err = http.Get(ts.URL + "/workers/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: want 200, got %d", resp.StatusCode)
	}
	var health struct {
		Ready int `json:"ready"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.Ready != 1 || health.Total != 1 {
		t.Fatalf("unexpected health: %+v", health)
	}
}
