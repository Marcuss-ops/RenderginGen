package workspace

import "time"

const (
	// workspaceLockWaitLimit bounds the non-flock fallback. A wedged marker or a
	// lock held by a process this host cannot probe must not block forever.
	workspaceLockWaitLimit      = 30 * time.Second
	workspaceLockInitialBackoff = 10 * time.Millisecond
	workspaceLockMaxBackoff     = 250 * time.Millisecond
)

func nextWorkspaceLockBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return workspaceLockInitialBackoff
	}
	next := current * 2
	if next > workspaceLockMaxBackoff {
		return workspaceLockMaxBackoff
	}
	return next
}
