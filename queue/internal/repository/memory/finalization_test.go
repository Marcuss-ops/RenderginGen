package memory

import (
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderginGen/queue/internal/model"
)

func TestClaimFinalizationHasSingleOwner(t *testing.T) {
	r := New(time.Minute, 3)
	if err := r.Submit(model.Job{ID: "parent", RenderPlan: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	const claimers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	owners := 0
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, claimed, err := r.ClaimFinalization("parent", "worker")
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if claimed {
				mu.Lock()
				owners++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if owners != 1 {
		t.Fatalf("owners = %d, want 1", owners)
	}
	job, err := r.Get("parent")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != model.StateFinalizing {
		t.Fatalf("state = %q", job.State)
	}
}
