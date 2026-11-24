package processor

import "testing"

// The prefetch semaphore is processor-scoped: two workers in one process must
// not consume each other's warm-up budget. The shipped ceiling is four.
func TestPrefetchInflightCapIsFour(t *testing.T) {
	if prefetchMaxInflight != 4 {
		t.Fatalf("prefetchMaxInflight = %d, want 4", prefetchMaxInflight)
	}
	p := &Processor{}
	if got := cap(p.prefetchSlots()); got != 4 {
		t.Fatalf("zero-value processor prefetch cap = %d, want 4", got)
	}
}
