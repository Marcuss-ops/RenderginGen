package processor

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

func TestRenderProgressObserverConcurrentObserveAndHeartbeat(t *testing.T) {
	o := newRenderProgressObserver(&queue.Job{ID: "race"}, nil, "vulkan", "nvenc")
	o.logEvery = time.Nanosecond
	o.heartbeat = time.Millisecond
	o.logf = func(string, ...any) {}
	o.statef = func(string, string, ...any) {}
	stop := o.Heartbeat(context.Background())
	defer stop()

	var wg sync.WaitGroup
	for lane := 0; lane < 2; lane++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := int64(0); i < 200; i++ {
				o.Observe(chronon.RenderProgress{FramesDone: i, FramesTotal: 200, FPS: 30, At: time.Now()})
			}
		}()
	}
	wg.Wait()
	last, saw := o.Snapshot()
	if !saw || last.FramesDone != 199 {
		t.Fatalf("snapshot = %+v, saw=%v, want the last milestone", last, saw)
	}
}

func TestRenderProgressObserverReportsSilentRenderer(t *testing.T) {
	o := newRenderProgressObserver(&queue.Job{ID: "silent"}, nil, "vulkan", "nvenc")
	o.heartbeat = 5 * time.Millisecond
	o.logf = func(string, ...any) {}
	lines := make(chan string, 4)
	o.statef = func(_ string, format string, args ...any) {
		select {
		case lines <- format:
		default:
		}
	}
	stop := o.Heartbeat(context.Background())
	defer stop()
	select {
	case line := <-lines:
		if !strings.Contains(line, "progress=unknown") || !strings.Contains(line, "renderer_sent_no_frame_milestones") {
			t.Fatalf("heartbeat line %q does not report the silent renderer", line)
		}
	case <-time.After(time.Second):
		t.Fatal("silent renderer produced no heartbeat")
	}
}
