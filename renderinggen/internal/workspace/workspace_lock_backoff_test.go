package workspace

import (
	"testing"
	"time"
)

func TestWorkspaceLockBackoffIsBounded(t *testing.T) {
	if got := nextWorkspaceLockBackoff(0); got != workspaceLockInitialBackoff {
		t.Fatalf("initial backoff = %v, want %v", got, workspaceLockInitialBackoff)
	}
	got := workspaceLockInitialBackoff
	for i := 0; i < 20; i++ {
		got = nextWorkspaceLockBackoff(got)
		if got > workspaceLockMaxBackoff {
			t.Fatalf("backoff grew to %v, above the %v cap", got, workspaceLockMaxBackoff)
		}
	}
	if got != workspaceLockMaxBackoff {
		t.Fatalf("backoff did not reach the cap: %v", got)
	}
	if workspaceLockWaitLimit <= 0 || workspaceLockWaitLimit > time.Minute {
		t.Fatalf("fallback wait limit = %v, want a bounded positive wait", workspaceLockWaitLimit)
	}
}
