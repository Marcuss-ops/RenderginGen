package workermetrics

import (
	"strings"
	"testing"
	"time"
)

// TestExpositionCarriesLaneWait pins the P2 feed: the prep→GPU rendezvous wait
// must be scrapeable on its own series so p50(lane wait) vs p50(render) can be
// read off Prometheus before anyone touches gpu_lanes.
func TestExpositionCarriesLaneWait(t *testing.T) {
	m := New()
	m.ObserveLaneWait(27 * time.Second)
	m.ObserveLaneWait(-time.Second) // clock skew clamps to 0, never negative

	body := scrape(t, m)
	for _, want := range []string{
		`renderinggen_worker_lane_wait_seconds_count 2`,
		`renderinggen_worker_lane_wait_seconds_bucket{le="30"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition is missing %q\n---\n%s", want, body)
		}
	}
}

// TestLaneWaitHookMatchesDirectObservation pins the hook as an adapter, not a
// second measurement.
func TestLaneWaitHookMatchesDirectObservation(t *testing.T) {
	viaHook := New()
	viaHook.LaneWaitHook()(750 * time.Millisecond)
	direct := New()
	direct.ObserveLaneWait(750 * time.Millisecond)
	if got, want := laneWaitFamily(scrape(t, viaHook)), laneWaitFamily(scrape(t, direct)); got != want {
		t.Errorf("hook exposition differs from direct observation:\nhook:\n%s\ndirect:\n%s", got, want)
	}
}

func laneWaitFamily(body string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "renderinggen_worker_lane_wait_seconds") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// TestNilLaneWaitIsSafe pins the unwired contract.
func TestNilLaneWaitIsSafe(t *testing.T) {
	var m *Metrics
	m.ObserveLaneWait(time.Second)
	m.LaneWaitHook()(time.Second)
}
