package chronon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeStrictNativeTestPlan(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(`{"layers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIPCEncodeRequest(t *testing.T) {
	frame := encodeIPCRequest(ipcCommandRenderJob, []byte(`{"plan_path":"/jobs/1/plan.json"}`))

	if len(frame) != ipcHeaderBytes+len(`{"plan_path":"/jobs/1/plan.json"}`) {
		t.Fatalf("frame length = %d", len(frame))
	}
	if got := binary.BigEndian.Uint32(frame[0:4]); got != ipcMagic {
		t.Fatalf("magic = %#x", got)
	}
	if got := binary.BigEndian.Uint32(frame[4:8]); got != ipcCommandRenderJob {
		t.Fatalf("command = %d", got)
	}
	if got := binary.BigEndian.Uint32(frame[8:12]); int(got) != len(`{"plan_path":"/jobs/1/plan.json"}`) {
		t.Fatalf("payload len = %d", got)
	}
	if string(frame[ipcHeaderBytes:]) != `{"plan_path":"/jobs/1/plan.json"}` {
		t.Fatalf("payload = %q", frame[ipcHeaderBytes:])
	}
}

// startFakeDaemon serves a single RENDER_JOB request over a unix socket,
// reporting the decoded command/payload and replying with the given status.
func startFakeDaemon(t *testing.T, replyStatus uint32, replyMsg string) (socketPath string, gotCmd chan uint32, gotPayload chan string) {
	t.Helper()
	socketPath = filepath.Join(t.TempDir(), "chronon.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	gotCmd = make(chan uint32, 1)
	gotPayload = make(chan string, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		header := make([]byte, ipcHeaderBytes)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		plen := binary.BigEndian.Uint32(header[8:12])
		payload := make([]byte, plen)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		gotCmd <- binary.BigEndian.Uint32(header[4:8])
		gotPayload <- string(payload)

		reply := make([]byte, ipcHeaderBytes+len(replyMsg))
		binary.BigEndian.PutUint32(reply[0:4], ipcMagic)
		binary.BigEndian.PutUint32(reply[4:8], replyStatus)
		binary.BigEndian.PutUint32(reply[8:12], uint32(len(replyMsg)))
		copy(reply[ipcHeaderBytes:], replyMsg)
		_, _ = conn.Write(reply)
	}()

	return socketPath, gotCmd, gotPayload
}

func TestIPCClientAssemble(t *testing.T) {
	socketPath, gotCmd, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)
	client := NewIPCClient(socketPath)
	if err := client.Assemble(context.Background(), AssembleRequest{Inputs: []string{"a.mp4", "b.mp4"}, Output: "final.mp4"}); err != nil {
		t.Fatal(err)
	}
	if cmd := <-gotCmd; cmd != ipcCommandAssembleSegments {
		t.Fatalf("command = %d", cmd)
	}
	var payload AssembleRequest
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Inputs) != 2 || payload.Inputs[0] != "a.mp4" || payload.Output != "final.mp4" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestIPCClientAssembleError(t *testing.T) {
	socketPath, _, _ := startFakeDaemon(t, ipcStatusError, "incompatible")
	if err := NewIPCClient(socketPath).Assemble(context.Background(), AssembleRequest{Inputs: []string{"a.mp4"}, Output: "final.mp4"}); err == nil {
		t.Fatal("expected assemble error")
	}
}

func TestIPCClientRenderSuccess(t *testing.T) {
	socketPath, gotCmd, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok","output":"/jobs/1/output/result.mp4"}`)

	client := NewIPCClient(socketPath)
	err := client.Render(context.Background(), RenderRequest{
		JobID:      "job-123",
		PlanPath:   "/jobs/1/plan.json",
		AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4",
		FirstFrame: 240,
		LastFrame:  359,
		Report:     true,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if cmd := <-gotCmd; cmd != ipcCommandRenderJob {
		t.Fatalf("command = %d", cmd)
	}
	var payload struct {
		JobID               string `json:"job_id"`
		PlanPath            string `json:"plan_path"`
		PreparedPackagePath string `json:"prepared_package_path"`
		AssetsRoot          string `json:"assets_root"`
		Output              string `json:"output"`
		FirstFrame          int64  `json:"first_frame"`
		LastFrame           int64  `json:"last_frame"`
		Report              bool   `json:"report"`
	}
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if payload.JobID != "job-123" ||
		payload.PlanPath != "/jobs/1/plan.json" ||
		payload.PreparedPackagePath != "" ||
		payload.AssetsRoot != "/jobs/1/assets" ||
		payload.Output != "/jobs/1/output/result.mp4" ||
		!payload.Report || payload.FirstFrame != 240 || payload.LastFrame != 359 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestIPCClientRenderStopsWaitingWhenContextExpires(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "chronon.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		header := make([]byte, ipcHeaderBytes)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		payload := make([]byte, binary.BigEndian.Uint32(header[8:12]))
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		close(accepted)
		// Deliberately withhold the daemon reply; the caller context must close
		// this transport instead of leaving a goroutine blocked in ReadFull.
		_, _ = io.Copy(io.Discard, conn)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err = NewIPCClient(socketPath).Render(ctx, RenderRequest{
		PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1",
		OutputPath: "/jobs/1/output/result.mp4", RangeEnabled: true,
		FirstFrame: 0, LastFrame: 0,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Render error = %v, want context deadline exceeded", err)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("fake daemon did not receive the render request")
	}
}

func TestIPCRenderTimeoutScalesWithFrameCountAndCaps(t *testing.T) {
	tests := []struct {
		frames int64
		want   time.Duration
	}{
		{frames: 1, want: defaultIPCRenderTimeout + ipcRenderAdmissionAllowance},
		{frames: 180, want: 270*time.Second + ipcRenderAdmissionAllowance},
		{frames: 181, want: 271*time.Second + 500*time.Millisecond + ipcRenderAdmissionAllowance},
		{frames: 100_000, want: maxIPCRenderTimeout + ipcRenderAdmissionAllowance},
	}
	for _, tt := range tests {
		ctx, cancel := ipcRenderContextForFrames(context.Background(), tt.frames)
		deadline, ok := ctx.Deadline()
		if !ok {
			cancel()
			t.Fatalf("frames=%d: missing timeout deadline", tt.frames)
		}
		got := time.Until(deadline).Round(100 * time.Millisecond)
		cancel()
		if got != tt.want {
			t.Errorf("frames=%d timeout=%s, want %s", tt.frames, got, tt.want)
		}
	}
}

func TestIPCClientRenderForwardsPreparedPackage(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)
	planPath := writeStrictNativeTestPlan(t)
	const preparedPackagePath = "/jobs/1/prepared.json"

	client := NewIPCClient(socketPath)
	if err := client.Render(context.Background(), RenderRequest{
		PlanPath:            planPath,
		PreparedPackagePath: preparedPackagePath,
		AssetsRoot:          "/jobs/1",
		OutputPath:          "/jobs/1/output/result.mp4",
	}); err != nil {
		t.Fatalf("render: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if got := payload["prepared_package_path"]; got != preparedPackagePath {
		t.Fatalf("prepared_package_path = %v, want %q (payload=%v)", got, preparedPackagePath, payload)
	}
}

func TestIPCClientRenderUsesSemanticContract(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)
	planPath := writeStrictNativeTestPlan(t)

	client := NewIPCClient(socketPath)
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:   planPath,
		AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4",
		Requirements: ExecutionRequirements{
			GPURequired: true, CPUFallbackAllowed: false,
			CompositionRequired: true, PacketCopyAllowed: true,
		},
		Output: OutputSpec{Codec: "h264", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, PipePixFmt: "nv12"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if _, ok := payload["execution_requirements"]; !ok {
		t.Fatalf("semantic requirements missing: %v", payload)
	}
	if _, ok := payload["output_spec"]; !ok {
		t.Fatalf("output spec missing: %v", payload)
	}
	outputSpec, ok := payload["output_spec"].(map[string]any)
	if !ok || outputSpec["pipe_pixfmt"] != "nv12" {
		t.Fatalf("pipe pixel format missing from output_spec: %v", payload["output_spec"])
	}
	for _, leaked := range []string{"hardware_encoder", "encoder_backend", "gpu_hot_path_mode"} {
		if _, ok := payload[leaked]; ok {
			t.Fatalf("backend detail leaked as %q: %v", leaked, payload)
		}
	}
}

func TestIPCClientRenderCarriesNoDirtyRects(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)
	planPath := writeStrictNativeTestPlan(t)

	client := NewIPCClient(socketPath)
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:     planPath,
		AssetsRoot:   "/jobs/1/assets",
		OutputPath:   "/jobs/1/output/result.mp4",
		NoDirtyRects: true,
		Requirements: ExecutionRequirements{
			Backend: "software", CompositionRequired: true,
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if got, ok := payload["no_dirty_rects"]; !ok || got != true {
		t.Fatalf("no_dirty_rects = %v (present=%v), want true: %v", got, ok, payload)
	}
}

// TestIPCClientRenderCarriesStrictNativeSelectionInOutputSpec pins the fix for
// the strict-native IPC black hole. The daemon resolves
// hardware_encoder/encoder_backend/gpu_hot_path_mode from output_spec via
// spec_or_root, so an IPC request that sent them NOWHERE could not express the
// strict contract at all: the daemon selected the software FFmpeg pipe lane and
// libx264 then rejected the NVENC-only --encode-preset ("p2"), failing every
// image/text-only render (GoldenSemanticOverlayJobV1).
func TestIPCClientRenderCarriesStrictNativeSelectionInOutputSpec(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)
	planPath := writeStrictNativeTestPlan(t)

	client := NewIPCClient(socketPath)
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:     planPath,
		AssetsRoot:   "/jobs/1/assets",
		OutputPath:   "/jobs/1/output/result.mp4",
		EncodePreset: "p2",
		Requirements: ExecutionRequirements{
			Backend: "vulkan", GPURequired: true, CPUFallbackAllowed: false,
			CompositionRequired: true, VideoSourceRequired: false, PacketCopyAllowed: true,
		},
		Output: OutputSpec{Codec: "h264", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, PipePixFmt: "yuv420p"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	outputSpec, ok := payload["output_spec"].(map[string]any)
	if !ok {
		t.Fatalf("output_spec missing or not an object: %v", payload["output_spec"])
	}
	for key, want := range map[string]string{
		"hardware_encoder":  "nvenc",
		"encoder_backend":   "native",
		"gpu_hot_path_mode": "require_gpu_native",
	} {
		if got := outputSpec[key]; got != want {
			t.Fatalf("output_spec[%q] = %v, want %q (payload=%v)", key, got, want, payload)
		}
	}
	// The backend detail stays confined to output_spec: the top-level payload
	// contract above is unchanged.
	for _, leaked := range []string{"hardware_encoder", "encoder_backend", "gpu_hot_path_mode"} {
		if _, ok := payload[leaked]; ok {
			t.Fatalf("backend detail leaked as top-level %q: %v", leaked, payload)
		}
	}
}

// TestIPCClientRenderCarriesEncoderProfileInOutputSpec pins the IPC half of
// the canonical single-session lane: the profile rides in output_spec (the
// daemon's closed vocabulary reads it there with spec_or_root fallback) and
// never leaks to the payload root, so a transport cannot silently drop it.
func TestIPCClientRenderCarriesEncoderProfileInOutputSpec(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)

	client := NewIPCClient(socketPath)
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:   writeStrictNativeTestPlan(t),
		AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4",
		Output: OutputSpec{
			Codec: "h264", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			PipePixFmt: "yuv420p", EncoderProfile: "canonical_x264",
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	outputSpec, ok := payload["output_spec"].(map[string]any)
	if !ok {
		t.Fatalf("output_spec missing or not an object: %v", payload["output_spec"])
	}
	if got := outputSpec["encoder_profile"]; got != "canonical_x264" {
		t.Fatalf("output_spec[encoder_profile] = %v, want canonical_x264", got)
	}
	if _, leaked := payload["encoder_profile"]; leaked {
		t.Fatalf("encoder_profile leaked as a top-level key: %v", payload)
	}
}

// TestIPCClientRenderOmitsSelectionWithoutGPURecord proves the selection is not
// stamped unconditionally: a request with no GPU/composition requirement keeps
// the historical payload (no encoder keys at all), so Chronon's own default
// applies unchanged.
func TestIPCClientRenderOmitsSelectionWithoutGPURecord(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)

	client := NewIPCClient(socketPath)
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:   "/jobs/1/plan.json",
		AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4",
		Output:     OutputSpec{Codec: "h264", Width: 320, Height: 180, FPSNum: 30, FPSDen: 1},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	outputSpec, ok := payload["output_spec"].(map[string]any)
	if !ok {
		t.Fatalf("output_spec missing or not an object: %v", payload["output_spec"])
	}
	for _, key := range []string{"hardware_encoder", "encoder_backend", "gpu_hot_path_mode"} {
		if _, present := outputSpec[key]; present {
			t.Fatalf("output_spec[%q] must be absent for a non-GPU request: %v", key, outputSpec)
		}
	}
}

// TestIPCClientRenderFrameZeroRange pins the wave-1B daemon-parity corner: a
// single-frame chunk at frame 0 (RangeEnabled with first=0, last=0) must
// reach the wire as range_enabled=true with first_frame=0/last_frame=0
// PRESENT — never as an absent range that the daemon would re-expand into a
// whole-plan render.
func TestIPCClientRenderFrameZeroRange(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)

	client := NewIPCClient(socketPath)
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:     "/jobs/1/plan.json",
		AssetsRoot:   "/jobs/1/assets",
		OutputPath:   "/jobs/1/output/result.mp4",
		RangeEnabled: true,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	enabled, _ := payload["range_enabled"].(bool)
	if !enabled {
		t.Fatalf("range_enabled must be true: %v", payload)
	}
	for _, key := range []string{"first_frame", "last_frame"} {
		if _, present := payload[key]; !present {
			t.Fatalf("%s must be present for an explicit range: %v", key, payload)
		}
	}
	if v, _ := payload["first_frame"].(float64); v != 0 {
		t.Fatalf("first_frame = %v, want 0", payload["first_frame"])
	}
	if v, _ := payload["last_frame"].(float64); v != 0 {
		t.Fatalf("last_frame = %v, want 0", payload["last_frame"])
	}
}

func TestIPCClientRenderMarksOnlyExplicitChunkChildrenAsParallelDisjoint(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)
	client := NewIPCClient(socketPath)
	if err := client.Render(context.Background(), RenderRequest{
		PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1", OutputPath: "/jobs/1/chunk.mp4",
		RangeEnabled: true, FirstFrame: 24, LastFrame: 47, ParallelDisjoint: true,
	}); err != nil {
		t.Fatalf("render chunk: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if got, ok := payload["parallel_disjoint"].(bool); !ok || !got {
		t.Fatalf("parallel_disjoint = %#v, want true", payload["parallel_disjoint"])
	}

	socketPath, _, gotPayload = startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)
	client = NewIPCClient(socketPath)
	if err := client.Render(context.Background(), RenderRequest{
		PlanPath: "/jobs/1/plan.json", AssetsRoot: "/jobs/1", OutputPath: "/jobs/1/range.mp4",
		RangeEnabled: true, FirstFrame: 24, LastFrame: 47,
	}); err != nil {
		t.Fatalf("render ranged non-chunk: %v", err)
	}
	payload = nil
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if got, present := payload["parallel_disjoint"]; present {
		t.Fatalf("non-chunk range must omit parallel_disjoint, got %#v", got)
	}
}

// TestIPCClientRenderWholePlanStaysWhole pins the mirror corner: a
// whole-plan render (RangeEnabled=false) must not carry range semantics even
// when coordinates happen to be 0 — the daemon must render the whole plan.
func TestIPCClientRenderWholePlanStaysWhole(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)

	client := NewIPCClient(socketPath)
	if err := client.Render(context.Background(), RenderRequest{
		PlanPath:   "/jobs/1/plan.json",
		AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4",
	}); err != nil {
		t.Fatalf("render: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if enabled, _ := payload["range_enabled"].(bool); enabled {
		t.Fatalf("whole-plan render must carry range_enabled=false: %v", payload)
	}
}

// TestIPCClientRenderForwardsFullSemanticContract asserts the daemon payload
// carries every RenderRequest semantic the CLI subprocess path already
// forwards (audio source, encode preset, receipt policy, requirements,
// output spec) so CLI-vs-daemon parity cannot silently drift.
func TestIPCClientRenderForwardsFullSemanticContract(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)

	client := NewIPCClient(socketPath)
	planPath := writeStrictNativeTestPlan(t)
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:        planPath,
		AssetsRoot:      "/jobs/1/assets",
		OutputPath:      "/jobs/1/output/result.mp4",
		AudioSourcePath: "/jobs/1/assets/source.mp4",
		EncodePreset:    "p2",
		ReceiptVerify:   "normal",
		RangeEnabled:    true,
		FirstFrame:      240,
		LastFrame:       359,
		Requirements: ExecutionRequirements{
			GPURequired: true, CPUFallbackAllowed: false,
			CompositionRequired: true, VideoSourceRequired: true,
			PacketCopyAllowed: false,
		},
		Output: OutputSpec{Codec: "h264"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if v, _ := payload["audio_source_path"].(string); v != "/jobs/1/assets/source.mp4" {
		t.Fatalf("audio_source_path = %v", payload["audio_source_path"])
	}
	if v, _ := payload["encode_preset"].(string); v != "p2" {
		t.Fatalf("encode_preset = %v", payload["encode_preset"])
	}
	if v, _ := payload["receipt_verify"].(string); v != "normal" {
		t.Fatalf("receipt_verify = %v", payload["receipt_verify"])
	}
	reqs, _ := payload["execution_requirements"].(map[string]any)
	if reqs == nil {
		t.Fatalf("execution_requirements missing: %v", payload)
	}
	if v, _ := reqs["cpu_fallback_allowed"].(bool); v {
		t.Fatalf("cpu_fallback_allowed must be false on the wire: %v", reqs)
	}
	if v, _ := reqs["packet_copy_allowed"].(bool); v {
		t.Fatalf("packet_copy_allowed must be false on the wire: %v", reqs)
	}
}

func TestIPCClientStatus(t *testing.T) {
	socketPath, gotCmd, _ := startFakeDaemon(t, ipcStatusOk, "frames_rendered=2 total_ms=123.4")

	client := NewIPCClient(socketPath)
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if cmd := <-gotCmd; cmd != ipcCommandStatus {
		t.Fatalf("command = %d", cmd)
	}
	if !strings.Contains(status, "frames_rendered=2") {
		t.Fatalf("status = %q", status)
	}
}

func TestIPCClientShutdown(t *testing.T) {
	socketPath, gotCmd, _ := startFakeDaemon(t, ipcStatusShutdown, "bye")

	client := NewIPCClient(socketPath)
	if err := client.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if cmd := <-gotCmd; cmd != ipcCommandShutdown {
		t.Fatalf("command = %d", cmd)
	}
}

func TestIPCClientRenderError(t *testing.T) {
	socketPath, _, _ := startFakeDaemon(t, ipcStatusError, "render failed")

	client := NewIPCClient(socketPath)
	err := client.Render(context.Background(), RenderRequest{PlanPath: "/jobs/1/plan.json"})
	if err == nil {
		t.Fatal("expected error from daemon status")
	}
}

func TestIPCClientDialFailure(t *testing.T) {
	client := NewIPCClient(filepath.Join(t.TempDir(), "missing.sock"))
	err := client.Render(context.Background(), RenderRequest{PlanPath: "/jobs/1/plan.json"})
	if err == nil {
		t.Fatal("expected dial error for missing socket")
	}
}

// TestIPCClientRenderToleratesUnparsableReplyOnFastTier pins the compatibility
// tier: a foreign/older daemon answering a plain "ok" body must keep working.
func TestIPCClientRenderToleratesUnparsableReplyOnFastTier(t *testing.T) {
	socketPath, _, _ := startFakeDaemon(t, ipcStatusOk, "ok")

	client := NewIPCClient(socketPath)
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:      "/jobs/1/plan.json",
		ReceiptVerify: ReceiptVerifyFast,
	})
	if err != nil {
		t.Fatalf("fast tier must tolerate a non-JSON ok reply: %v", err)
	}
}

// TestIPCClientRenderRejectsUnparsableReplyUnderProofPolicy pins the fail-closed
// side: normal/certify claim proof of the render, so a daemon whose reply cannot
// be parsed must not be credited with having rendered.
func TestIPCClientRenderRejectsUnparsableReplyUnderProofPolicy(t *testing.T) {
	for _, policy := range []string{ReceiptVerifyNormal, ReceiptVerifyCertify} {
		t.Run(policy, func(t *testing.T) {
			socketPath, _, _ := startFakeDaemon(t, ipcStatusOk, "ok")

			client := NewIPCClient(socketPath)
			err := client.Render(context.Background(), RenderRequest{
				PlanPath:      "/jobs/1/plan.json",
				ReceiptVerify: policy,
			})
			if err == nil {
				t.Fatalf("policy %q must reject an unparsable ok reply", policy)
			}
		})
	}
}

func TestRequiresStructuredReply(t *testing.T) {
	cases := map[string]bool{
		"":                   false,
		ReceiptVerifyFast:    false,
		"FAST":               false,
		ReceiptVerifyNormal:  true,
		ReceiptVerifyCertify: true,
		" certify ":          true,
	}
	for policy, want := range cases {
		if got := RequiresStructuredReply(policy); got != want {
			t.Errorf("RequiresStructuredReply(%q) = %v, want %v", policy, got, want)
		}
	}
}

// TestIPCClientRenderBatchSendsCommandNine pins the wire: a batch is ONE frame
// with the header's RenderBatch command (9) and {"jobs":[RENDER_JOB, ...]}, so
// N overlays cost one dial and one connection instead of N.
func TestIPCClientRenderBatchSendsCommandNine(t *testing.T) {
	socketPath, gotCmd, gotPayload := startFakeDaemon(t, ipcStatusOk,
		`{"results":[{"index":0,"status":"ok","message":""},{"index":1,"status":"ok","message":""}]}`)

	client := NewIPCClient(socketPath)
	results, err := client.RenderBatch(context.Background(), []RenderRequest{
		{PlanPath: "/jobs/0/plan.json", OutputPath: "/jobs/0/out.mp4"},
		{PlanPath: "/jobs/1/plan.json", OutputPath: "/jobs/1/out.mp4", RangeEnabled: true, FirstFrame: 3, LastFrame: 9},
	})
	if err != nil {
		t.Fatalf("RenderBatch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if ipcCommandRenderBatch != 9 {
		t.Fatalf("ipcCommandRenderBatch = %d, want the header's RenderBatch = 9", ipcCommandRenderBatch)
	}
	if cmd := <-gotCmd; cmd != ipcCommandRenderBatch {
		t.Fatalf("command = %d, want RenderBatch (%d)", cmd, ipcCommandRenderBatch)
	}
	var payload struct {
		Jobs []struct {
			PlanPath     string `json:"plan_path"`
			Output       string `json:"output"`
			RangeEnabled bool   `json:"range_enabled"`
			FirstFrame   int64  `json:"first_frame"`
			LastFrame    int64  `json:"last_frame"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("decode batch payload: %v", err)
	}
	if len(payload.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(payload.Jobs))
	}
	if payload.Jobs[1].PlanPath != "/jobs/1/plan.json" || payload.Jobs[1].Output != "/jobs/1/out.mp4" {
		t.Fatalf("second job body drifted: %+v", payload.Jobs[1])
	}
	if !payload.Jobs[1].RangeEnabled || payload.Jobs[1].FirstFrame != 3 || payload.Jobs[1].LastFrame != 9 {
		t.Fatalf("second job range drifted: %+v", payload.Jobs[1])
	}
}

// TestIPCClientRenderBatchReportsPerJobFailures pins the failure contract: the
// daemon answers Error for a partly failed batch, and the caller still receives
// every per-job outcome plus an error that names the failed index.
func TestIPCClientRenderBatchReportsPerJobFailures(t *testing.T) {
	socketPath, _, _ := startFakeDaemon(t, ipcStatusError,
		`{"results":[{"index":0,"status":"ok","message":""},{"index":1,"status":"error","message":"render failed: boom"}]}`)

	client := NewIPCClient(socketPath)
	results, err := client.RenderBatch(context.Background(), []RenderRequest{
		{PlanPath: "/jobs/0/plan.json"},
		{PlanPath: "/jobs/1/plan.json"},
	})
	if err == nil {
		t.Fatal("a partly failed batch must return an error")
	}
	if !strings.Contains(err.Error(), "#1") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error does not attribute the failure: %v", err)
	}
	if len(results) != 2 || results[0].Status != "ok" || results[1].Status != "error" {
		t.Fatalf("per-job results lost: %+v", results)
	}
}

// TestIPCClientRenderBatchRejectsInvalidShapes fails closed before dialing: the
// daemon accepts 1..64 jobs (RENDER_BATCH contract), and a request that the
// single-job path would reject must not become valid by being batched.
func TestIPCClientRenderBatchRejectsInvalidShapes(t *testing.T) {
	client := NewIPCClient(filepath.Join(t.TempDir(), "never-dialed.sock"))
	if _, err := client.RenderBatch(context.Background(), nil); err == nil {
		t.Error("an empty batch must fail")
	}
	tooMany := make([]RenderRequest, ipcMaxBatchJobs+1)
	if _, err := client.RenderBatch(context.Background(), tooMany); err == nil {
		t.Error("a batch above the daemon limit must fail")
	}
	if _, err := client.RenderBatch(context.Background(), []RenderRequest{{}}); err == nil {
		t.Error("an invalid job inside a batch must fail")
	}
}

// startWatchDaemon serves RENDER_JOB (optionally holding the reply) and
// GET_JOB_STATUS on independent connections, mirroring the real daemon's
// one-connection-per-command model. It is the deterministic fixture for the
// IPC-02 watchdog: status is fixed, output bytes are controlled by the test.
func startWatchDaemon(t *testing.T, state string, holdRender bool) (string, func()) {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "chronon.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(func() {
		_ = ln.Close()
		close(stop)
		release()
	})

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				header := make([]byte, ipcHeaderBytes)
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				payload := make([]byte, binary.BigEndian.Uint32(header[8:12]))
				if _, err := io.ReadFull(conn, payload); err != nil {
					return
				}
				switch command := binary.BigEndian.Uint32(header[4:8]); command {
				case ipcCommandRenderJob, ipcCommandRenderBatch:
					if holdRender {
						select {
						case <-releaseCh:
						case <-stop:
							return
						}
					}
					writeWatchReply(conn, ipcStatusOk, `{"status":"ok"}`)
				case ipcCommandGetJobStatus:
					writeWatchReply(conn, ipcStatusOk,
						fmt.Sprintf(`{"job_id":%q,"state":%q}`, string(payload), state))
				default:
					writeWatchReply(conn, ipcStatusError, "unexpected command")
				}
			}(conn)
		}
	}()
	return socketPath, release
}

func writeWatchReply(conn net.Conn, status uint32, message string) {
	reply := make([]byte, ipcHeaderBytes+len(message))
	binary.BigEndian.PutUint32(reply[0:4], ipcMagic)
	binary.BigEndian.PutUint32(reply[4:8], status)
	binary.BigEndian.PutUint32(reply[8:12], uint32(len(message)))
	copy(reply[ipcHeaderBytes:], message)
	_, _ = conn.Write(reply)
}

func TestIPCClient_RenderAbortsOnStall(t *testing.T) {
	socketPath, _ := startWatchDaemon(t, "running", true)
	dir := t.TempDir()
	client := NewIPCClient(socketPath)
	client.SetStallTimeout(80 * time.Millisecond)

	started := time.Now()
	err := client.Render(context.Background(), RenderRequest{
		JobID:      "job-stall",
		PlanPath:   writeStrictNativeTestPlan(t),
		AssetsRoot: dir,
		OutputPath: filepath.Join(dir, "result.mp4"),
	})
	if !errors.Is(err, ErrIPCStallTimeout) {
		t.Fatalf("Render error = %v, want ErrIPCStallTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("stall abort took %v; the configured window is 80ms", elapsed)
	}
}

func TestIPCClientRenderKeepsRunningWhileOutputGrows(t *testing.T) {
	socketPath, release := startWatchDaemon(t, "running", true)
	dir := t.TempDir()
	output := filepath.Join(dir, "result.mp4")
	temp := output + ".chronon.tmp.1.mp4"
	// Seed the atomic-write sibling before the first tick, then keep it
	// growing: the watchdog must treat those bytes as frame progress.
	if err := os.WriteFile(temp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for i := 0; i < 12; i++ {
			if err := os.WriteFile(temp, make([]byte, (i+2)*4096), 0o600); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		release()
	}()

	client := NewIPCClient(socketPath)
	client.SetStallTimeout(300 * time.Millisecond)
	err := client.Render(context.Background(), RenderRequest{
		JobID:      "job-progress",
		PlanPath:   writeStrictNativeTestPlan(t),
		AssetsRoot: dir,
		OutputPath: output,
	})
	if err != nil {
		t.Fatalf("growing output was treated as a stall: %v", err)
	}
}

// Compile-time check: the daemon transport is the batch-capable transport.
var _ BatchRenderer = (*IPCClient)(nil)
