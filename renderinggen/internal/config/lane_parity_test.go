package config

import (
	"strings"
	"testing"
)

// TestLaneParityGuidanceNamesTheDecisionInputs pins the anti-blind-lane rule:
// the guidance must name both distributions the operator has to compare before
// touching gpu_lanes, plus the topology the lanes actually spread over.
func TestLaneParityGuidanceNamesTheDecisionInputs(t *testing.T) {
	got := LaneParityGuidance(3, "ipc", []string{"/run/a.sock", "/run/b.sock"})
	for _, want := range []string{
		"gpu_lanes=3",
		"sockets=2",
		"lanes_per_socket=2",
		"lane_wait",
		"render",
		"gpu_lanes only",
		"chronon.socket_paths",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("guidance is missing %q: %q", want, got)
		}
	}
}

// TestLaneParityGuidanceDefaultsEmptyTransport pins that an unset mode reads as
// the cli default instead of an empty string in the log line, and that a
// process-owning transport reports no sockets rather than a fake one.
func TestLaneParityGuidanceDefaultsEmptyTransport(t *testing.T) {
	got := LaneParityGuidance(2, "", nil)
	if !strings.Contains(got, "transport=cli") {
		t.Fatalf("empty mode must read as cli, got %q", got)
	}
	if !strings.Contains(got, "sockets=0") {
		t.Fatalf("the cli transport has no daemon socket to report, got %q", got)
	}
}

// TestRenderSocketsResolvesTheTopologyOnce pins the single authority for "which
// sockets this worker dials": the explicit list wins, the single socket_path is
// the one-daemon shape, and the cli transport has none.
func TestRenderSocketsResolvesTheTopologyOnce(t *testing.T) {
	multi := ChrononConfig{Mode: "ipc", SocketPaths: []string{"/run/a.sock", "/run/b.sock"}}
	if got := multi.RenderSockets(); len(got) != 2 || got[1] != "/run/b.sock" {
		t.Fatalf("socket_paths must be the topology, got %v", got)
	}
	// The returned slice is a copy: a caller cannot mutate the configuration.
	multi.RenderSockets()[0] = "/mutated"
	if multi.SocketPaths[0] != "/run/a.sock" {
		t.Fatalf("RenderSockets leaked a mutable view of the config: %v", multi.SocketPaths)
	}
	single := ChrononConfig{Mode: "ipc", SocketPath: "/run/only.sock"}
	if got := single.RenderSockets(); len(got) != 1 || got[0] != "/run/only.sock" {
		t.Fatalf("a single socket_path must resolve to one socket, got %v", got)
	}
	if got := (ChrononConfig{Mode: "cli", SocketPath: "/run/only.sock"}).RenderSockets(); got != nil {
		t.Fatalf("the cli transport owns its process and dials no socket, got %v", got)
	}
}
