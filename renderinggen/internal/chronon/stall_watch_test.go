package chronon

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStallWatchStalledAt exhaustively pins the stall decision, including the
// boundary: silence equal to the timeout is NOT a stall, because the timeout is
// the budget the engine is allowed to stay quiet for.
func TestStallWatchStalledAt(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name      string
		timeout   time.Duration
		elapsed   time.Duration
		wantStall bool
	}{
		{"fresh output", 10 * time.Second, 0, false},
		{"inside the window", 10 * time.Second, 9 * time.Second, false},
		{"exactly the window", 10 * time.Second, 10 * time.Second, false},
		{"one tick past the window", 10 * time.Second, 10*time.Second + time.Millisecond, true},
		{"far past the window", time.Second, 2 * time.Minute, true},
		{"zero timeout disables", 0, time.Hour, false},
		{"negative timeout disables", -time.Second, time.Hour, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			activity := newStallActivity(base)
			w := stallWatch{timeout: tc.timeout, activity: activity}
			idle, stalled := w.stalledAt(base.Add(tc.elapsed))
			if stalled != tc.wantStall {
				t.Fatalf("stalledAt(timeout=%v, elapsed=%v) = (%v, %v), want stalled=%v",
					tc.timeout, tc.elapsed, idle, stalled, tc.wantStall)
			}
			if stalled && idle != tc.elapsed {
				t.Fatalf("stalledAt reported idle %v, want %v", idle, tc.elapsed)
			}
		})
	}
}

// TestStallActivityTouchResetsIdle pins the clock contract the scanners rely on:
// the newest touch wins, and a touch after a long silence restarts the budget.
func TestStallActivityTouchResetsIdle(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	activity := newStallActivity(base)
	if got := activity.idleFor(base.Add(3 * time.Second)); got != 3*time.Second {
		t.Fatalf("idleFor after construction = %v, want 3s", got)
	}
	activity.touch(base.Add(4 * time.Second))
	if got := activity.idleFor(base.Add(5 * time.Second)); got != time.Second {
		t.Fatalf("idleFor after touch = %v, want 1s", got)
	}
	// An OLDER touch must not move the clock backwards: concurrent scanners can
	// deliver a line timestamped before another scanner's line, and honouring it
	// would extend the deadline of a genuinely silent render.
	activity.touch(base.Add(time.Second))
	if got := activity.idleFor(base.Add(5 * time.Second)); got != time.Second {
		t.Fatalf("a stale touch moved the clock backwards: idleFor = %v, want 1s", got)
	}
}

// TestStallWatchFiresOnceOnSilence drives the real sampling loop with a short
// tick and asserts the two things Render depends on: the stall is reported
// exactly once, and run returns true after reporting it.
func TestStallWatchFiresOnceOnSilence(t *testing.T) {
	activity := newStallActivity(time.Now())
	ticks := make(chan time.Time, 1)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	go func() {
		for tick := range ticker.C {
			select {
			case ticks <- tick:
			default:
			}
		}
	}()
	done := make(chan struct{})
	defer close(done)

	var (
		mu     sync.Mutex
		calls  int
		idle   time.Duration
		loaded atomic.Bool
	)
	watch := stallWatch{
		timeout:  time.Millisecond,
		activity: activity,
		onStall: func(d time.Duration) {
			mu.Lock()
			calls++
			idle = d
			mu.Unlock()
			loaded.Store(true)
		},
	}

	fired := make(chan bool, 1)
	go func() { fired <- watch.run(done, ticks) }()

	select {
	case result := <-fired:
		if !result {
			t.Fatal("a silent render must report a stall")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stall watchdog never fired on a silent render")
	}
	mu.Lock()
	got := calls
	measured := idle
	mu.Unlock()
	if got != 1 {
		t.Fatalf("onStall called %d times, want exactly 1", got)
	}
	if measured <= 0 {
		t.Fatalf("onStall reported idle %v, want a positive measured idle", measured)
	}
	if !loaded.Load() {
		t.Fatal("onStall did not record the stall")
	}
}

// TestStallWatchDoesNotFireWhileOutputFlows keeps the clock fresh for the whole
// sampling window and asserts the watchdog stays quiet. The timeout is far
// larger than the activity period so this cannot flake on a loaded machine: a
// false stall would need a multi-second scheduling pause.
func TestStallWatchDoesNotFireWhileOutputFlows(t *testing.T) {
	activity := newStallActivity(time.Now())
	ticks := make(chan time.Time, 1)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	go func() {
		for tick := range ticker.C {
			select {
			case ticks <- tick:
			default:
			}
		}
	}()
	stopTouching := make(chan struct{})
	var touching sync.WaitGroup
	touching.Add(1)
	go func() {
		defer touching.Done()
		touch := time.NewTicker(time.Millisecond)
		defer touch.Stop()
		for {
			select {
			case <-stopTouching:
				return
			case now := <-touch.C:
				activity.touch(now)
			}
		}
	}()

	done := make(chan struct{})
	var stalled atomic.Bool
	watch := stallWatch{
		timeout:  5 * time.Second,
		activity: activity,
		onStall:  func(time.Duration) { stalled.Store(true) },
	}
	fired := make(chan bool, 1)
	go func() { fired <- watch.run(done, ticks) }()

	time.Sleep(20 * time.Millisecond)
	close(stopTouching)
	touching.Wait()
	close(done)

	select {
	case result := <-fired:
		if result {
			t.Fatal("a render producing output must not be reported as stalled")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watchdog did not return when the render ended")
	}
	if stalled.Load() {
		t.Fatal("onStall was called for a render that kept producing output")
	}
}

// TestStallWatchDisabledNeverFires pins that a zero timeout is a disabled
// watchdog, not an instant stall: run must return without waiting for a tick
// that would otherwise abort every render.
func TestStallWatchDisabledNeverFires(t *testing.T) {
	activity := newStallActivity(time.Now())
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	done := make(chan struct{})
	stalls := 0
	watch := stallWatch{
		timeout:  0,
		activity: activity,
		onStall:  func(time.Duration) { stalls++ },
	}
	if watch.run(done, ticks) {
		t.Fatal("a disabled watchdog must not report a stall")
	}
	if stalls != 0 {
		t.Fatalf("onStall called %d times with the watchdog disabled", stalls)
	}
}

// TestStallWatchIntervalIsBelowTheDefaultTimeout pins the relationship the
// sampling period exists for: noticing a stall must not itself be slow.
func TestStallWatchIntervalIsBelowTheDefaultTimeout(t *testing.T) {
	if stallWatchInterval <= 0 {
		t.Fatal("stallWatchInterval must be positive")
	}
	if stallWatchInterval >= DefaultStallTimeout {
		t.Fatalf("stallWatchInterval %v must be shorter than DefaultStallTimeout %v, or every sample is a stall",
			stallWatchInterval, DefaultStallTimeout)
	}
}
