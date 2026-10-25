package chronon

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// segWindow builds one per-item output window for the contract tables.
func segWindow(path string, start, end int64) OutputSegment {
	return OutputSegment{OutputPath: path, StartFrame: start, EndFrame: end}
}

// TestValidateOutputSegmentsRequiresTheWholeTimelineToTile: the windows must
// tile [0, ·) contiguously with unique, non-empty output paths. A malformed
// window set is refused at the transport boundary so it can never reach the
// segmented mux writer mid-export.
func TestValidateOutputSegmentsRequiresTheWholeTimelineToTile(t *testing.T) {
	valid := RenderRequest{OutputSegments: []OutputSegment{
		segWindow("segment-000.mp4", 0, 8),
		segWindow("segment-001.mp4", 8, 24),
	}}
	if err := validateOutputSegments(valid); err != nil {
		t.Fatalf("valid tiling rejected: %v", err)
	}
	// Empty preserves the single-output path; one window is a legal
	// single-segment tiling.
	if err := validateOutputSegments(RenderRequest{}); err != nil {
		t.Fatalf("empty segment set rejected: %v", err)
	}
	if err := validateOutputSegments(RenderRequest{OutputSegments: []OutputSegment{segWindow("a.mp4", 0, 24)}}); err != nil {
		t.Fatalf("single-segment tiling rejected: %v", err)
	}

	cases := []struct {
		name string
		req  RenderRequest
		want string
	}{
		{
			name: "lead-in",
			req: RenderRequest{OutputSegments: []OutputSegment{
				segWindow("a.mp4", 1, 9),
				segWindow("b.mp4", 9, 24),
			}},
			want: "tile the timeline contiguously from frame 0",
		},
		{
			name: "gap",
			req: RenderRequest{OutputSegments: []OutputSegment{
				segWindow("a.mp4", 0, 8),
				segWindow("b.mp4", 9, 24),
			}},
			want: "tile the timeline",
		},
		{
			name: "overlap",
			req: RenderRequest{OutputSegments: []OutputSegment{
				segWindow("a.mp4", 0, 8),
				segWindow("b.mp4", 7, 24),
			}},
			want: "tile the timeline",
		},
		{
			name: "zero-length window",
			req: RenderRequest{OutputSegments: []OutputSegment{
				segWindow("a.mp4", 0, 8),
				segWindow("b.mp4", 8, 8),
			}},
			want: "empty or inverted window",
		},
		{
			name: "backwards window",
			req: RenderRequest{OutputSegments: []OutputSegment{
				segWindow("a.mp4", 0, 8),
				segWindow("b.mp4", 8, 4),
			}},
			want: "empty or inverted window",
		},
		{
			name: "empty output path",
			req:  RenderRequest{OutputSegments: []OutputSegment{segWindow("", 0, 8)}},
			want: "empty output_path",
		},
		{
			name: "duplicate output path",
			req: RenderRequest{OutputSegments: []OutputSegment{
				segWindow("a.mp4", 0, 8),
				segWindow("a.mp4", 8, 24),
			}},
			want: "repeats output_path",
		},
		{
			name: "muxed source audio",
			req: RenderRequest{
				OutputSegments:  []OutputSegment{segWindow("a.mp4", 0, 24)},
				AudioSourcePath: "master.wav",
			},
			want: "video-only",
		},
		{
			name: "explicit chunk range",
			req: RenderRequest{
				OutputSegments: []OutputSegment{segWindow("a.mp4", 0, 24)},
				RangeEnabled:   true,
				FirstFrame:     0,
				LastFrame:      7,
			},
			want: "explicit frame range",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOutputSegments(tc.req)
			if err == nil {
				t.Fatalf("malformed window set accepted (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestValidateRenderRequestRejectsSegmentsAndRanges: the shared boundary keeps
// the segmentation rules reachable from every transport, including the
// out-of-range chunk check that would otherwise over-render the whole plan.
func TestValidateRenderRequestRejectsSegmentsAndRanges(t *testing.T) {
	req := RenderRequest{
		OutputSegments: []OutputSegment{segWindow("a.mp4", 0, 8), segWindow("b.mp4", 8, 16)},
		RangeEnabled:   true,
		FirstFrame:     0,
		LastFrame:      7,
	}
	err := validateRenderRequest(req)
	if err == nil || !strings.Contains(err.Error(), "explicit frame range") {
		t.Fatalf("segments + range accepted: %v", err)
	}
	req = RenderRequest{RangeEnabled: true, FirstFrame: 9, LastFrame: 3}
	if err := validateRenderRequest(req); err == nil {
		t.Fatal("inverted chunk range accepted")
	}
}

// TestOutputSpecForTransportCarriesTheSegmentWindows: the request-level
// OutputSegments is the single semantic source and the wire copy happens in
// outputSpecForTransport, so no call site can forget it.
func TestOutputSpecForTransportCarriesTheSegmentWindows(t *testing.T) {
	segments := []OutputSegment{segWindow("a.mp4", 0, 8), segWindow("b.mp4", 8, 16)}
	spec := outputSpecForTransport(RenderRequest{OutputSegments: segments})
	if !reflect.DeepEqual(spec.OutputSegments, segments) {
		t.Fatalf("output_spec.output_segments = %+v, want %+v", spec.OutputSegments, segments)
	}
	if spec := outputSpecForTransport(RenderRequest{}); len(spec.OutputSegments) != 0 {
		t.Fatalf("single-output request leaked segment windows: %+v", spec.OutputSegments)
	}
}

// stubSegmentRenderer is a Renderer whose segmented-output capability the test
// controls, so wrapper forwarding can be checked in both directions.
type stubSegmentRenderer struct {
	segments bool
}

func (s stubSegmentRenderer) Render(context.Context, RenderRequest) error {
	return errors.New("not implemented")
}

func (s stubSegmentRenderer) SupportsOutputSegments() bool { return s.segments }

// TestSegmentedOutputCapabilityPerTransport: the worker must segment a batch
// only when the transport underneath can honour it. The CLI adapter cannot;
// the daemon IPC transport can; wrappers forward the capability of their lanes.
func TestSegmentedOutputCapabilityPerTransport(t *testing.T) {
	if (&Client{}).SupportsOutputSegments() {
		t.Fatal("the CLI subprocess adapter must not claim per-item output windows")
	}
	if !(&IPCClient{}).SupportsOutputSegments() {
		t.Fatal("the daemon IPC transport carries output_spec.output_segments and must claim the capability")
	}
	yes := stubSegmentRenderer{segments: true}
	no := stubSegmentRenderer{segments: false}
	if !(&SerializedRenderer{inner: yes}).SupportsOutputSegments() {
		t.Fatal("serialized wrapper must forward a capable lane")
	}
	if (&SerializedRenderer{inner: no}).SupportsOutputSegments() {
		t.Fatal("serialized wrapper must not promote an incapable lane")
	}
	if !(&spreadRenderer{lanes: []Renderer{yes, yes}}).SupportsOutputSegments() {
		t.Fatal("spread must be capable when every lane is")
	}
	if (&spreadRenderer{lanes: []Renderer{yes, no}}).SupportsOutputSegments() {
		t.Fatal("spread must be incapable when one lane is")
	}
	if (&spreadRenderer{lanes: nil}).SupportsOutputSegments() {
		t.Fatal("spread without lanes cannot honour segmented output")
	}
	if !(&DaemonPool{}).SupportsOutputSegments() {
		t.Fatal("daemon pool lanes are IPC daemons and must claim the capability")
	}
}

// TestClientRefusesSegmentedOutputOnTheCLITransport: refusing beats silently
// rendering ONE output and calling it a batch.
func TestClientRefusesSegmentedOutputOnTheCLITransport(t *testing.T) {
	req := RenderRequest{OutputSegments: []OutputSegment{segWindow("a.mp4", 0, 8), segWindow("b.mp4", 8, 16)}}
	err := (&Client{}).Render(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "only supported on the daemon IPC transport") {
		t.Fatalf("CLI adapter accepted segmented output: %v", err)
	}
}
