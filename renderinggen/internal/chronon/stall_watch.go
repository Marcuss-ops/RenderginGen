// stall_watch.go owns the two pieces a render's anti-stall watchdog needs: the
// activity clock the output scanners write and the sampler that decides when
// silence has lasted long enough to abort.
//
// Why a component. Both were inline in Client.Render — an atomic timestamp, a
// ticker goroutine, a cancel() and a log line, sharing a scope with pipe setup
// and process reaping. Nothing could be tested without starting a process and
// then waiting real seconds for a real ticker, so in practice nothing was: the
// watchdog's three behaviours (fires on silence, does not fire while output
// flows, stops when the render ends) were reachable only through an integration
// test with a deliberately hanging child. Here the tick source and the clock are
// parameters, so every one of those behaviours is a deterministic unit test (see
// stall_watch_test.go) and Render keeps only the wiring.
package chronon

import (
	"sync/atomic"
	"time"
)

// stallWatchInterval is how often a render's activity clock is sampled. It is
// deliberately much shorter than DefaultStallTimeout: the sampler bounds how
// late a stall is noticed, and a stall already costs the caller the full
// timeout, so the sampling period must not add meaningfully to it.
const stallWatchInterval = 5 * time.Second

// stallActivity is the shared "the child produced output at this time" clock.
//
// The scanners write it and the watchdog reads it, concurrently. It holds a
// Unix nanosecond timestamp in one atomic int64 rather than a time.Time behind a
// mutex: the write happens once per output line (tens of thousands of times per
// render) and must never block a scanner that is draining a pipe.
type stallActivity struct {
	nanos atomic.Int64
}

// newStallActivity starts an activity clock at now.
func newStallActivity(now time.Time) *stallActivity {
	a := &stallActivity{}
	a.touch(now)
	return a
}

// touch records output at now, and never moves the clock backwards.
//
// Monotonic on purpose: stdout and stderr are scanned by separate goroutines, so
// a line timestamped earlier than one already recorded can arrive later. Honouring
// it would make the render look MORE silent than it is and could abort a healthy
// render — the one failure a watchdog must not have. A stale touch is therefore
// simply ignored.
func (a *stallActivity) touch(now time.Time) {
	nanos := now.UnixNano()
	for {
		previous := a.nanos.Load()
		if nanos <= previous {
			return
		}
		if a.nanos.CompareAndSwap(previous, nanos) {
			return
		}
	}
}

// idleFor reports how long the clock has been untouched at now.
func (a *stallActivity) idleFor(now time.Time) time.Duration {
	return now.Sub(time.Unix(0, a.nanos.Load()))
}

// stallWatch reports a render that has gone silent for longer than timeout.
//
// run takes its tick source as a parameter instead of owning a ticker, which is
// what makes the sampler testable without sleeping: production passes a
// time.Ticker's channel, a test passes the channel it drives itself.
type stallWatch struct {
	timeout  time.Duration
	activity *stallActivity
	// now reads the current time; nil means time.Now. It is a field so a test
	// can move time without waiting for it.
	now func() time.Time
	// onStall is invoked AT MOST ONCE, on the goroutine running run, with the
	// measured idle duration. It is what aborts the render (cancelling the
	// process context), so it must not be called twice: a second call would log
	// a second stall for one render and could cancel a context that has already
	// been reused by the caller's error path.
	onStall func(idle time.Duration)
}

// stalledAt reports whether the render has been silent for longer than the
// timeout at now, and for how long.
//
// The decision is a pure function of the clock so it is exhaustively testable
// without a goroutine, a ticker or a sleep; run below only adds the sampling
// loop around it. A non-positive timeout never reports a stall: with no bound
// there is nothing to enforce, and treating it as "already stalled" would abort
// every healthy render, while the package default (DefaultStallTimeout) is
// applied by the caller for the same reason.
func (w *stallWatch) stalledAt(now time.Time) (time.Duration, bool) {
	if w.timeout <= 0 {
		return 0, false
	}
	idle := w.activity.idleFor(now)
	if idle <= w.timeout {
		return 0, false
	}
	return idle, true
}

// run samples ticks until the activity clock has been idle for longer than the
// timeout, then reports the stall through onStall and returns true. It returns
// false when done is closed first (the render finished, or the caller
// cancelled), in which case onStall is never called.
func (w *stallWatch) run(done <-chan struct{}, ticks <-chan time.Time) bool {
	if w.timeout <= 0 {
		return false
	}
	now := w.now
	if now == nil {
		now = time.Now
	}
	for {
		select {
		case <-done:
			return false
		case <-ticks:
			idle, stalled := w.stalledAt(now())
			if !stalled {
				continue
			}
			if w.onStall != nil {
				w.onStall(idle)
			}
			return true
		}
	}
}
