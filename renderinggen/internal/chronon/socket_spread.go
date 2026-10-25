// socket_spread.go owns the multi-daemon transport shape: one worker's render
// lanes spread round-robin over N independent Chronon daemon sockets.
//
// WHY IT EXISTS
// -------------
// A worker with gpu_lanes=N talking to ONE daemon does not gain throughput from
// the extra lanes. The daemon serializes them, and the only observable effect
// is a growing prep→GPU rendezvous wait
// (renderinggen_worker_lane_wait_seconds, the same interval gpu_lane_wait_ms
// records per job). Reaching the NVENC multi-session baseline therefore takes
// more than one daemon — one render socket each — which is exactly what
// chronon.socket_paths declares.
//
// This is the spread half of the warm DaemonPool's Render (round-robin over
// per-socket lanes, each lane a LimitConcurrency queue), with one difference
// that matters: the worker neither starts nor owns these daemons. They are the
// operator's units, so this transport only dials them.
//
// BOUNDING
// --------
// Each socket lane caps its own concurrency, so a busy daemon queues its next
// job instead of stalling the whole worker, and the worker's outer cap stays
// the single owner of "how many renders run at once"
// (cmd/renderinggen wraps this renderer in LimitConcurrency(renderer,
// gpu_lanes)). Splitting the worker's lanes over the sockets (rather than
// giving every socket every lane) is what makes the daemon count and the lane
// count agree instead of fighting.
package chronon

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
)

// LanesPerSocket splits totalLanes worker lanes over socketCount sockets. A
// socket never gets zero lanes: the split is ceil(total/sockets), so listing
// more sockets than lanes degrades to one lane per socket. The outer worker cap
// still bounds how many of those lanes may render at once, so the surplus is
// queueing, never extra GPU sessions.
func LanesPerSocket(totalLanes, socketCount int) int {
	if socketCount < 1 {
		return 1
	}
	if totalLanes < 1 {
		totalLanes = 1
	}
	return (totalLanes + socketCount - 1) / socketCount
}

// SpreadAcrossSockets builds the renderer for a multi-daemon worker: renders
// are spread round-robin over one lane per socket, each lane capped at
// LanesPerSocket(totalLanes, len(sockets)) concurrent renders.
//
// An empty list, a blank socket path or a repeated socket is an error rather
// than a degradation: each socket is one daemon, so spreading lanes over a path
// that is not a daemon halves throughput silently.
func SpreadAcrossSockets(sockets []string, totalLanes int) (Renderer, error) {
	if len(sockets) == 0 {
		return nil, fmt.Errorf("chronon socket spread: at least one daemon socket is required")
	}
	perSocket := LanesPerSocket(totalLanes, len(sockets))
	lanes := make([]Renderer, 0, len(sockets))
	seen := make(map[string]struct{}, len(sockets))
	for _, socket := range sockets {
		trimmed := strings.TrimSpace(socket)
		if trimmed == "" {
			return nil, fmt.Errorf("chronon socket spread: empty daemon socket path")
		}
		if _, duplicate := seen[trimmed]; duplicate {
			return nil, fmt.Errorf("chronon socket spread: socket %q is listed twice; each socket is one daemon", trimmed)
		}
		seen[trimmed] = struct{}{}
		lanes = append(lanes, LimitConcurrency(NewIPCClient(trimmed), perSocket))
	}
	return spreadAcrossLanes(lanes), nil
}

// Renderer factory kept separate from the socket plumbing so the spreading
// policy is testable with fake lanes instead of real daemons.
func spreadAcrossLanes(lanes []Renderer) Renderer {
	s := &spreadRenderer{lanes: lanes}
	s.inflight = make([]atomic.Int64, len(lanes))
	return s
}

// spreadRenderer distributes renders over independent lanes least-loaded, not
// round-robin blindly. A lane that is mid-render (or already has queued work)
// receives no further assignment, so the per-socket queue never grows behind a
// customer while another socket idles.
//
// WHY NOT ROUND-ROBIN
// -------------------
// Blind rotation assigns the NEXT lane purely by call order. With heterogeneous
// service times (phrase items ~2 s, map-backed items tens of seconds on a cold
// plate) one bad sequence parks two long renders behind the same lane while
// the other runs dry: the 2026-10-22 golden v16/v18 windows showed a single
// late map item stretching the wall ~40–60 s past every other item's finish.
// Least-loaded assignment bounds the worst case: no lane can be handed a
// second job before an unassigned lane has taken one.
//
// The pick is a lock-free min scan with a CAS increment, so concurrent callers
// cannot double-assign a lane past what its counter says (the increment is
// visible before Render is called), and a lost race simply re-scans.
type spreadRenderer struct {
	lanes []Renderer
	// inflight is the number of renders assigned to each lane that have not
	// returned yet — mid-render plus waiting for that lane's own concurrency
	// cap. The index mirrors lanes.
	inflight []atomic.Int64
	next     atomic.Uint64
}

// Compile-time check that spreadRenderer satisfies Renderer.
var _ Renderer = (*spreadRenderer)(nil)

// SupportsOutputSegments: the P4 segmented batch is ONE request on ONE lane,
// so the spread can carry it exactly when every lane underneath can.
func (s *spreadRenderer) SupportsOutputSegments() bool {
	for _, lane := range s.lanes {
		seg, ok := lane.(OutputSegmentRenderer)
		if !ok || !seg.SupportsOutputSegments() {
			return false
		}
	}
	return len(s.lanes) > 0
}

func (s *spreadRenderer) Render(ctx context.Context, req RenderRequest) error {
	if len(s.lanes) == 0 {
		return fmt.Errorf("chronon socket spread has no lanes")
	}
	slot := s.next.Add(1) - 1
	// Least-loaded scan, starting the sweep at the round-robin cursor so an
	// idle pool still spreads evenly instead of piling on lane 0.
	const noLane = -1
	lane := noLane
	for {
		best := noLane
		bestLoad := int64(^uint64(0) >> 1) // max int64
		for offset := range s.lanes {
			index := int((slot + uint64(offset)) % uint64(len(s.lanes)))
			if load := s.inflight[index].Load(); load < bestLoad {
				best, bestLoad = index, load
			}
		}
		// Claim the slot on the chosen lane. If another caller claimed the
		// same free slot first, rescan: the claim must be exact, not best effort.
		if s.inflight[best].CompareAndSwap(bestLoad, bestLoad+1) {
			lane = best
			break
		}
	}
	defer s.inflight[lane].Add(-1)
	return s.lanes[lane].Render(ctx, req)
}
