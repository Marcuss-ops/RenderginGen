package chronon

import (
	"context"
	"fmt"
)

// SerializedRenderer keeps exactly one Chronon render in flight while still
// allowing the surrounding worker pipeline to prepare the next job and
// post-process the previous one concurrently.
type SerializedRenderer struct {
	inner Renderer
	lane  chan struct{}
}

func Serialize(inner Renderer) Renderer {
	return LimitConcurrency(inner, 1)
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
	return r.inner.Render(ctx, req)
}
