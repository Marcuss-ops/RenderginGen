package chronon

import (
	"context"
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

// TestValidateRenderRequest pins the adapter-boundary range contract: an
// explicit range whose inclusive last frame precedes its first frame must be
// rejected, not silently dropped (which used to make Chronon render the WHOLE
// plan and report a chunk that never rendered as successful).
func TestValidateRenderRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     RenderRequest
		wantErr bool
	}{
		{"whole plan ignores coordinates", RenderRequest{}, false},
		{"single frame at zero", RenderRequest{RangeEnabled: true, FirstFrame: 0, LastFrame: 0}, false},
		{"normal range", RenderRequest{RangeEnabled: true, FirstFrame: 240, LastFrame: 359}, false},
		{"inverted range", RenderRequest{RangeEnabled: true, FirstFrame: 240, LastFrame: 100}, true},
		{"negative first frame", RenderRequest{RangeEnabled: true, FirstFrame: -1, LastFrame: 10}, true},
		{"inverted ignored without RangeEnabled", RenderRequest{FirstFrame: 240, LastFrame: 100}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRenderRequest(tc.req)
			if tc.wantErr && err == nil {
				t.Fatal("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want nil, got %v", err)
			}
		})
	}
}

// TestIPCClientRenderRejectsInvertedRange proves the IPC transport shares the
// boundary rule and that it fires BEFORE any dial: the socket path points at a
// non-existent file, so a dial-first implementation would report a dial error
// instead of the range error.
func TestIPCClientRenderRejectsInvertedRange(t *testing.T) {
	client := NewIPCClient(filepath.Join(t.TempDir(), "does-not-exist.sock"))
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:     "/jobs/1/plan.json",
		RangeEnabled: true,
		FirstFrame:   10,
		LastFrame:    5,
	})
	if err == nil {
		t.Fatal("expected an inverted-range error")
	}
	if !strings.Contains(err.Error(), "invalid frame range") {
		t.Fatalf("error = %v, want an invalid frame range error", err)
	}
}

// TestReadReceiptPresence pins the shared receipt-path authority: presence is
// reported from the canonical `<output>.receipt.json` suffix without requiring
// the identity block ReadMediaReceipt validates.
func TestReadReceiptPresence(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "result.mp4")

	if err := ReadReceiptPresence(output); err == nil {
		t.Fatal("want an error for a missing receipt")
	}
	if err := os.WriteFile(output+MediaReceiptSuffix, []byte(`{"copy_eligible":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReadReceiptPresence(output); err != nil {
		t.Fatalf("present receipt rejected: %v", err)
	}
	if err := os.WriteFile(output+MediaReceiptSuffix, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReadReceiptPresence(output); err == nil {
		t.Fatal("want an error for a non-JSON receipt")
	}
}

// TestIPCServiceOpsHaveDefaultDeadline pins the service-op bound: Status (and
// the other non-render commands) invoked with a deadline-less context must
// return when the daemon hangs instead of blocking forever. The render path
// deliberately keeps no default deadline, so only service ops are exercised.
func TestIPCServiceOpsHaveDefaultDeadline(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "hung.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Accept one connection and hold it open without replying: a hung daemon.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()

	client := NewIPCClient(socketPath)
	client.serviceTimeout = 150 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		_, err := client.Status(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from a hung daemon")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Status blocked on a hung daemon; the default service deadline did not apply")
	}
}

// TestRenderJoinsOutputStreamsBeforeReturning pins the cmd.Wait()/scanner join.
// Render must not return while an output-streaming goroutine can still invoke
// the Progress callback, or the caller (gpu_run's lastProgress, the ledger's
// render_frames_done/fps) reads its final observation concurrently with a
// writer and can silently miss the last milestone.
func TestRenderJoinsOutputStreamsBeforeReturning(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-cli")

	var body strings.Builder
	body.WriteString("#!/bin/sh\n")
	const frames = 200
	for i := 1; i <= frames; i++ {
		fmt.Fprintf(&body, "echo \"[video] %d/%d frames fps=%d.0\"\n", i, frames, i)
	}
	body.WriteString("exit 0\n")
	if err := os.WriteFile(script, []byte(body.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHRONON_BINARY", script)

	client := &Client{Home: dir}
	var (
		mu   sync.Mutex
		seen int64
	)
	err := client.Render(context.Background(), RenderRequest{
		PlanPath:    filepath.Join(dir, "plan.json"),
		AssetsRoot:  dir,
		OutputPath:  filepath.Join(dir, "out.mp4"),
		TotalFrames: frames,
		Progress: func(p RenderProgress) {
			mu.Lock()
			defer mu.Unlock()
			if p.FramesDone > seen {
				seen = p.FramesDone
			}
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if seen != frames {
		t.Fatalf("final observed frames_done = %d, want %d (final milestones lost: goroutine not joined)", seen, frames)
	}
}
