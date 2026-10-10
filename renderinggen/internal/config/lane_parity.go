package config

// Package config owns the worker's lane/daemon parity rule: gpu_lanes must
// never be raised blindly.
//
// A worker with gpu_lanes=N but a Chronon topology that can only run M<N
// renders concurrently does not gain throughput from the extra lanes — it only
// grows gpu_lane_wait_ms (the prep→GPU rendezvous wait). The decision input is
// the measured distribution, already exposed per job (gpu_lane_wait_ms/us on
// the artifact) and on Prometheus
// (renderinggen_worker_lane_wait_seconds vs
// renderinggen_worker_phase_duration_seconds{phase="render"}):
//
//   - p50(lane_wait) << p50(render): lanes are not the bottleneck.
//   - p50(lane_wait) >= p50(render): lanes exceed daemon concurrency — add
//     daemons (chronon.socket_paths, one render socket each) or lower
//     gpu_lanes, instead of raising gpu_lanes.
//
// The two ways to reach the multi-session baseline are therefore: keep
// gpu_lanes at the number of daemons the host can afford, or list more daemons
// in chronon.socket_paths and let the lanes spread over them.

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
)

// RenderSockets returns the daemon sockets this worker renders through, in
// order: the explicit socket_paths list when set, otherwise the single
// socket_path. It is empty for the cli transport, which owns its own process
// and has no daemon to dial.
//
// One authority for "which sockets": the wiring (main.go) and the parity
// guidance both read this instead of each re-implementing the precedence.
func (c ChrononConfig) RenderSockets() []string {
	if c.Mode != "ipc" {
		return nil
	}
	if len(c.SocketPaths) > 0 {
		return append([]string(nil), c.SocketPaths...)
	}
	if strings.TrimSpace(c.SocketPath) == "" {
		return nil
	}
	return []string{c.SocketPath}
}

// LaneParityGuidance renders the operator rule for one worker's lane/daemon
// pairing. It is guidance, not validation: the daemon's true concurrency is a
// measured engine property (NVENC multi-session baseline: 2 on RTX
// A4000-class hosts), so the config cannot prove it at load.
func LaneParityGuidance(gpuLanes int, mode string, sockets []string) string {
	transport := mode
	if transport == "" {
		transport = "cli"
	}
	if len(sockets) == 0 {
		return fmt.Sprintf(
			"lane/daemon parity: gpu_lanes=%d transport=%s sockets=0 — "+
				"read p50(renderinggen_worker_lane_wait_seconds) vs p50(phase render); "+
				"raise gpu_lanes only while p50 lane wait stays well below p50 render, "+
				"otherwise lower gpu_lanes",
			gpuLanes, transport,
		)
	}
	return fmt.Sprintf(
		"lane/daemon parity: gpu_lanes=%d transport=%s sockets=%d (%s) lanes_per_socket=%d — "+
			"read p50(renderinggen_worker_lane_wait_seconds) vs p50(phase render); "+
			"raise gpu_lanes only while p50 lane wait stays well below p50 render, "+
			"otherwise add daemons to chronon.socket_paths or lower gpu_lanes",
		gpuLanes, transport, len(sockets), strings.Join(sockets, ", "),
		chronon.LanesPerSocket(gpuLanes, len(sockets)),
	)
}
