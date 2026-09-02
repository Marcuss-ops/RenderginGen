package service

import "sync"

// Notifier is an in-process wake-up broadcast for state changes that could
// make a claim succeed. It carries no data: the database (or memory store)
// remains the single source of truth, and every woken claim re-runs the
// atomic claim path. This is the same semantic split as Postgres
// LISTEN/NOTIFY — the signal only wakes listeners, it never assigns work.
type Notifier struct {
	mu   sync.Mutex
	ch   chan struct{}
	once sync.Once
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
