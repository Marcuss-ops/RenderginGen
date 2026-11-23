package chronon

// Non-render IPC commands: daemon status, asset warm-up, shutdown and segment
// assembly. The render path lives in ipc_render.go, transport in ipc.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Status asks the daemon for its engine statistics (frames rendered, total
// render ms, prepared composition). It is how the daemon benchmark proves the
// engine stays warm between jobs.
func (c *IPCClient) Status(ctx context.Context) (string, error) {
	ctx, cancel := c.serviceContext(ctx)
	defer cancel()
	status, message, err := c.request(ctx, ipcCommandStatus, nil)
	if err != nil {
		return "", err
	}
	if status != ipcStatusOk {
		return "", ipcReplyError("status", status, message)
	}
	return message, nil
}

// PrefetchAsset asks the persistent daemon to import one already-materialized
// asset into Chronon's process-local image/video cache. It is an optimization
// only: a warm-up failure must never discard a valid render.
func (c *IPCClient) PrefetchAsset(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("ipc prefetch asset: path is empty")
	}
	ctx, cancel := c.serviceContext(ctx)
	defer cancel()
	status, message, err := c.request(ctx, ipcCommandPrefetchAsset, []byte(path))
	if err != nil {
		return err
	}
	if status != ipcStatusOk {
		return ipcReplyError("prefetch asset", status, message)
	}
	return nil
}

// Shutdown asks the daemon to stop serving and exit.
func (c *IPCClient) Shutdown(ctx context.Context) error {
	ctx, cancel := c.serviceContext(ctx)
	defer cancel()
	status, message, err := c.request(ctx, ipcCommandShutdown, nil)
	if err != nil {
		return err
	}
	if status != ipcStatusShutdown && status != ipcStatusOk {
		return ipcReplyError("shutdown", status, message)
	}
	return nil
}

// AssembleRequest is the generic Chronon segment assembly request.
type AssembleRequest struct {
	Inputs []string `json:"input_paths"`
	Output string   `json:"output_path"`
	// FragmentedMP4 emits the assembled artifact as a fragmented MP4 (the
	// overlay-artifact contract: bytes final when appended, streamable while
	// the mux is open). The chunk segments themselves may be classic MP4.
	FragmentedMP4 bool `json:"fragmented_mp4,omitempty"`
	// AudioSourcePath optionally muxes the source media's audio stream into
	// the assembled artifact — AUDIO-AWARE CHUNK ASSEMBLY: the family anchor is
	// the single audio owner over the concatenated video-only children, so the
	// source stream is copied exactly once and audio packet boundaries never
	// meet a window boundary. Semantics mirror RenderRequest.AudioSourcePath
	// exactly: the whole stream is copied verbatim (no transcode) and rebased
	// to PTS 0, so the assembled parent is what a single-job render of the
	// same plan would have produced.
	AudioSourcePath string `json:"audio_source_path,omitempty"`
	// AudioTargetSampleRate optionally resamples the OUTPUT audio stream to
	// the declared rate (0 keeps the source rate verbatim). Mirrors
	// RenderRequest.AudioTargetSampleRate: a positive value equal to the
	// source rate stays on the byte-identical copy path.
	AudioTargetSampleRate int `json:"audio_target_sample_rate,omitempty"`
}

type Assembler interface {
	Assemble(context.Context, AssembleRequest) error
}

// Assemble sends an ASSEMBLE_SEGMENTS command to the daemon.
func (c *IPCClient) Assemble(ctx context.Context, req AssembleRequest) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("ipc assemble: marshal payload: %w", err)
	}
	status, message, err := c.request(ctx, ipcCommandAssembleSegments, payload)
	if err != nil {
		return err
	}
	if status != ipcStatusOk {
		return ipcReplyError("assemble", status, message)
	}
	return nil
}
