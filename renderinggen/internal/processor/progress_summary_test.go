package processor

import (
	"strings"
	"testing"
)

func TestProgressSummary(t *testing.T) {
	for _, tc := range []struct {
		name         string
		done         int64
		total        int64
		fps          float64
		wantPrefix   string
		wantETA      bool
		wantComplete bool
	}{
		{name: "no milestones", done: 0, total: 0, wantPrefix: "unknown"},
		{name: "half way", done: 540, total: 1200, fps: 18, wantPrefix: "45.0%", wantETA: true},
		{name: "no fps yet", done: 120, total: 1200, wantPrefix: "10.0%"},
		{name: "finished", done: 1200, total: 1200, fps: 18, wantPrefix: "100.0%"},
		{name: "clamped", done: 1300, total: 1200, fps: 18, wantPrefix: "100.0%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := progressSummary(tc.done, tc.total, tc.fps)
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Fatalf("summary = %q, want prefix %q", got, tc.wantPrefix)
			}
			if strings.Contains(got, "eta=") != tc.wantETA {
				t.Fatalf("summary = %q, want eta=%t", got, tc.wantETA)
			}
		})
	}
}
