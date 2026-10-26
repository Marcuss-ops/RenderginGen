package chronon

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SerializedRenderer keeps exactly one Chronon render in flight while still
// allowing the surrounding worker pipeline to prepare the next job and
// post-process the previous one concurrently.
type SerializedRenderer struct {
	inner   Renderer
	lane    chan struct{}
	mu      sync.Mutex
	lastEnd time.Time
	active  int
}

func LimitConcurrency(inner Renderer, maxConcurrency int) Renderer {
	if inner == nil {
		return nil
	}
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	return &SerializedRenderer{inner: inner, lane: make(chan struct{}, maxConcurrency)}
}

func (r *SerializedRenderer) Render(ctx context.Context, req RenderRequest) error {
	select {
	case r.lane <- struct{}{}:
		defer func() { <-r.lane }()
	case <-ctx.Done():
		return fmt.Errorf("chronon render lane: %w", ctx.Err())
	}
	r.mu.Lock()
	var idle time.Duration
	if r.active == 0 && !r.lastEnd.IsZero() {
		idle = time.Since(r.lastEnd)
	}
	r.active++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.active--
		if r.active == 0 {
			r.lastEnd = time.Now()
		}
		r.mu.Unlock()
	}()
	if req.LaneTiming != nil {
		req.LaneTiming(idle)
	}
	return r.inner.Render(ctx, req)
}

// SupportsOutputSegments forwards the P4 capability to the wrapped lane.
func (r *SerializedRenderer) SupportsOutputSegments() bool {
	seg, ok := r.inner.(OutputSegmentRenderer)
	return ok && seg.SupportsOutputSegments()
}
