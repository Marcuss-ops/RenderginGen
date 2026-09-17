package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/buildinfo"
)

// TestHealthEndpointPublishesBuildIdentity is the worker half of the runtime
// identity contract: /health must answer "which binary, which commit, which
// config is this worker running?" without auditing the unit file or the `ps`
// line — the audit that made a stale worker indistinguishable from a fresh one.
func TestHealthEndpointPublishesBuildIdentity(t *testing.T) {
	identity := buildinfo.Identity{
		Version:      "0.1.0",
		GitCommit:    "a1b2c3d4e5f6",
		BuildTime:    "2026-09-17T09:00:00Z",
		BinarySHA256: "1111111111111111111111111111111111111111111111111111111111111111",
		ConfigPath:   "/etc/renderinggen/renderinggen.yaml",
		Mode:         "renderinggen-worker",
		WorkerID:     "renderinggen-host",
		PID:          4424,
		StartedAt:    "2026-09-17T08:42:18Z",
		IdentityHash: "cafebabecafebabe",
	}
	sh := NewServer(":0", Info{
		Worker:        "renderinggen-host",
		RenderingGen:  "0.1.0",
		Chronon:       "0.1.0",
		OverlaySchema: 1,
		Backend:       "vulkan",
		Status:        "ready",
		Build:         &identity,
	})
	ts := httptest.NewServer(sh.srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("get /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var got struct {
		Status string              `json:"status"`
		Build  *buildinfo.Identity `json:"build"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Build == nil {
		t.Fatal("/health must publish the worker build identity")
	}
	if got.Build.GitCommit != "a1b2c3d4e5f6" {
		t.Errorf("git_commit = %q", got.Build.GitCommit)
	}
	if got.Build.ConfigPath != "/etc/renderinggen/renderinggen.yaml" {
		t.Errorf("config_path = %q", got.Build.ConfigPath)
	}
	if got.Build.IdentityHash != "cafebabecafebabe" {
		t.Errorf("identity_hash = %q", got.Build.IdentityHash)
	}
	if got.Status != "ready" {
		t.Errorf("status = %q, want the historical field to be unchanged", got.Status)
	}
}

// TestHealthEndpointOmitsBuildWhenUnwired keeps the historical payload shape
// for fixtures that construct the server directly.
func TestHealthEndpointOmitsBuildWhenUnwired(t *testing.T) {
	sh := NewServer(":0", Info{Worker: "w", Status: "ready"})
	ts := httptest.NewServer(sh.srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("get /health: %v", err)
	}
	defer resp.Body.Close()

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := raw["build"]; present {
		t.Error("an unwired build identity must be omitted, not emitted empty")
	}
}
