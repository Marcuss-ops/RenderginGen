package service

import "sync"

// Notifier is the SINGLE wake-up broadcast for long-poll claims (U2).
//
// Both HTTP endpoints — POST /jobs/claim (wait_ms) and POST /jobs/claim/wait
// (max_wait_ms) — are thin wrappers over Service.WaitAndClaim, which is the
// claim-side long-poll loop. WaitAndClaim coalesces wakes via Notifier and
// falls
// back to bounded re-poll so signal loss cannot stall a worker. The signal
// carries no data: the database (or memory store) remains the single source
// of truth, and every woken waiter re-runs the atomic ClaimState (SKIP
// LOCKED). Submit, ClaimState, Complete, Rendered, Fail, RequeueExpired and
// external NotifyState all call Notify(); WaitAndClaim (claim side) and
// WaitState (producer terminal-state side) are the only consumers, and each
// keeps its own bounded re-poll, so the two share this single broadcast
// primitive instead of introducing a competing timer.
// The service re-poll loop (pollDelay 1s→10s exponential) is the third
// mechanism folded into this one Notifier so there are no competing timers.
//
// This is the same semantic split as Postgres LISTEN/NOTIFY — the signal
// only wakes listeners, it never assigns work.
type Notifier struct {
	mu sync.Mutex
	ch chan struct{}
}

// NewNotifier creates a ready notifier.
func NewNotifier() *Notifier {
	return &Notifier{ch: make(chan struct{})}
}

// Notify wakes every current waiter and arms a fresh generation.
func (n *Notifier) Notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	select {
	case <-n.ch: // already closed; nothing to wake
		return
	default:
	}
	close(n.ch)
	n.ch = make(chan struct{})
}

// Done returns the channel closed by the next Notify.
func (n *Notifier) Done() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}
