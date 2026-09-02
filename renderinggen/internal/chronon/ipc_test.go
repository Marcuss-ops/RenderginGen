package chronon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

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
		PlanPath   string `json:"plan_path"`
		AssetsRoot string `json:"assets_root"`
		Output     string `json:"output"`
		FirstFrame int64  `json:"first_frame"`
		LastFrame  int64  `json:"last_frame"`
		Report     bool   `json:"report"`
	}
	if err := json.Unmarshal([]byte(<-gotPayload), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if payload.PlanPath != "/jobs/1/plan.json" ||
		payload.AssetsRoot != "/jobs/1/assets" ||
		payload.Output != "/jobs/1/output/result.mp4" ||
		!payload.Report || payload.FirstFrame != 240 || payload.LastFrame != 359 {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestIPCClientRenderUsesSemanticContract(t *testing.T) {
	socketPath, _, gotPayload := startFakeDaemon(t, ipcStatusOk, `{"status":"ok"}`)

	client := NewIPCClient(socketPath)
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:   "/jobs/1/plan.json",
		AssetsRoot: "/jobs/1/assets",
		OutputPath: "/jobs/1/output/result.mp4",
		Requirements: ExecutionRequirements{
			GPURequired: true, CPUFallbackAllowed: false,
			CompositionRequired: true, PacketCopyAllowed: true,
		},
		Output: OutputSpec{Codec: "h264", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1},
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
	for _, leaked := range []string{"hardware_encoder", "encoder_backend", "gpu_hot_path_mode"} {
		if _, ok := payload[leaked]; ok {
			t.Fatalf("backend detail leaked as %q: %v", leaked, payload)
		}
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
