package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
