package chronon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

// IPC wire constants — must match Chronon3d's chronon_ipc.hpp.
//
// The command and status enums are mirrored COMPLETE from the header, even
// where this client does not yet compare against a value (ipcStatusNotFound,
// ipcStatusBadRequest). They are the wire contract, not locals: a partial
// enum would silently misnumber the values after the first omission, and a
// future caller (or a daemon that starts returning "not found" for a
// missing-asset prefetch) needs the constant to exist. Contract surface, not
// dead code — do not trim it to the currently-referenced subset.
const (
	ipcMagic                   uint32 = 0x43484e33 // "CHN3"
	ipcHeaderBytes                    = 12         // magic + command/status + payload-len
	ipcMaxPayload                     = 64 * 1024 * 1024
	ipcCommandPrefetchAsset           = 1
	ipcCommandStatus                  = 4
	ipcCommandShutdown                = 5
	ipcCommandRenderJob               = 6
	ipcCommandAssembleSegments        = 7
	ipcStatusOk                       = 0
	ipcStatusError                    = 1
	ipcStatusNotFound                 = 2
	ipcStatusBadRequest               = 3
	ipcStatusShutdown                 = 4
)

// defaultIPCServiceTimeout bounds the daemon operations that are NOT the
// per-job render (Status, PrefetchAsset, Shutdown). Those are commonly invoked
// with context.Background(), so without a bound a hung daemon blocks the caller
// forever. The per-job Render/Assemble calls carry the caller's own (usually
// lane-derived) context and deliberately keep no default, because a legitimate
// render may outlive any service timeout. The bound exists to fail, not to be
// tight.
const defaultIPCServiceTimeout = 90 * time.Second

// IPCClient renders through the persistent Chronon3d render daemon over a
// UNIX-domain socket. It implements Renderer, so it is a drop-in replacement
// for the CLI subprocess Client.
type IPCClient struct {
	socketPath string
	// serviceTimeout bounds Status/PrefetchAsset/Shutdown. Zero uses
	// defaultIPCServiceTimeout; tests shorten it.
	serviceTimeout time.Duration
}

// NewIPCClient creates a Renderer that talks to a Chronon3d daemon listening
// at socketPath.
func NewIPCClient(socketPath string) *IPCClient {
	return &IPCClient{socketPath: socketPath}
}

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
		return "", fmt.Errorf("ipc status: daemon status %d: %s", status, message)
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
		return fmt.Errorf("ipc prefetch asset: daemon status %d: %s", status, message)
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
		return fmt.Errorf("ipc shutdown: daemon status %d: %s", status, message)
	}
	return nil
}

// renderJobPayload is the JSON payload for the RENDER_JOB IPC command.  It is
// the daemon-side mirror of the full RenderRequest semantic contract:
//
//	plan_path / assets_root / output / report
//	range_enabled + first_frame + last_frame  (explicit chunk range; a
//	  single-frame chunk at frame 0 is first=0, last=0 WITH range_enabled
//	  true — never an "absent range" that silently re-expands to the whole
//	  plan)
//	audio_source_path (muxed source audio, daemon-side --gop-source)
//	encode_preset (native NVENC tier)
//	receipt_verify (per-job verification policy; the daemon reads the same
//	  policy the CLI subprocess receives through CHRONON_RECEIPT_VERIFY)
//	execution_requirements / output_spec (semantic, backend-neutral)
//
// first_frame/last_frame are marshaled unconditionally (no omitempty) so 0
// coordinates are expressible; the daemon honors them only when range_enabled
// is true.  Older daemons ignore the extra keys and keep rendering the whole
// plan.
type renderJobPayload struct {
	PlanPath              string                `json:"plan_path"`
	AssetsRoot            string                `json:"assets_root"`
	Output                string                `json:"output"`
	RangeEnabled          bool                  `json:"range_enabled"`
	FirstFrame            int64                 `json:"first_frame"`
	LastFrame             int64                 `json:"last_frame"`
	Report                bool                  `json:"report"`
	AudioSourcePath       string                `json:"audio_source_path,omitempty"`
	EncodePreset          string                `json:"encode_preset,omitempty"`
	ReceiptVerify         string                `json:"receipt_verify,omitempty"`
	ExecutionRequirements ExecutionRequirements `json:"execution_requirements"`
	OutputSpec            OutputSpec            `json:"output_spec"`
}

// AssembleRequest is the generic Chronon segment assembly request.
type AssembleRequest struct {
	Inputs []string `json:"input_paths"`
	Output string   `json:"output_path"`
}

type Assembler interface {
	Assemble(context.Context, AssembleRequest) error
}

type renderJobReply struct {
	Status string `json:"status"`
	Output string `json:"output"`
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
		return fmt.Errorf("ipc assemble: daemon status %d: %s", status, message)
	}
	return nil
}

// outputSpecForTransport stamps the resolved native-encoder selection onto the
// wire output_spec.
//
// The daemon resolves hardware_encoder/encoder_backend/gpu_hot_path_mode with
// spec_or_root(request, ...), i.e. it reads the output_spec sub-object. A
// request that omits them cannot express "strict native": the daemon selects
// the software FFmpeg pipe encoder, and the NVENC-only --encode-preset ("p2")
// then reaches libx264, which rejects it. The selection comes from the SAME
// authority as the CLI arguments (resolveNativeEncodeSelection), so the CLI and
// IPC transports cannot drift. It is stamped here rather than by the caller so
// no call site can forget it, and it never mutates the caller's struct.
func outputSpecForTransport(req RenderRequest) OutputSpec {
	spec := req.Output
	selection, ok := resolveNativeEncodeSelection(req)
	if !ok {
		return spec
	}
	if selection.HardwareEncoder != "" {
		spec.HardwareEncoder = selection.HardwareEncoder
	}
	spec.EncoderBackend = selection.EncoderBackend
	spec.GpuHotPathMode = selection.GpuHotPathMode
	return spec
}

// Render sends a RENDER_JOB command to the daemon and waits for its reply.
func (c *IPCClient) Render(ctx context.Context, req RenderRequest) error {
	if err := validateRenderRequest(req); err != nil {
		return err
	}
	payload, err := json.Marshal(renderJobPayload{
		PlanPath:   req.PlanPath,
		AssetsRoot: req.AssetsRoot, Output: req.OutputPath,
		// Range parity: coordinates are always marshaled (0 is meaningful)
		// and range_enabled marks them as an explicit chunk. A whole-plan
		// render is range_enabled=false even when the coordinates are 0.
		RangeEnabled:          req.RangeEnabled,
		FirstFrame:            req.FirstFrame,
		LastFrame:             req.LastFrame,
		Report:                req.Report,
		AudioSourcePath:       req.AudioSourcePath,
		EncodePreset:          req.EncodePreset,
		ReceiptVerify:         req.ReceiptVerify,
		ExecutionRequirements: req.Requirements,
		OutputSpec:            outputSpecForTransport(req),
	})
	if err != nil {
		return fmt.Errorf("ipc render: marshal payload: %w", err)
	}

	status, message, err := c.request(ctx, ipcCommandRenderJob, payload)
	if err != nil {
		return err
	}
	if status != ipcStatusOk {
		return fmt.Errorf("ipc render: daemon status %d: %s", status, message)
	}

	var reply renderJobReply
	if err := json.Unmarshal([]byte(message), &reply); err != nil {
		// Non-JSON Ok replies are tolerated (backward compatibility with
		// older/other daemons), but never silently: a daemon that starts
		// answering corrupt payloads must leave a trace instead of passing
		// unobserved.
		log.Printf("[chronon ipc WARN] render reply status=ok but body is not JSON (tolerated): %v", err)
		return nil
	}
	if reply.Status != "" && reply.Status != "ok" {
		return fmt.Errorf("ipc render: %s", reply.Status)
	}
	return nil
}

// serviceContext bounds a service operation. An existing context deadline is
// preserved (context.WithTimeout keeps the earlier of the two).
func (c *IPCClient) serviceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.serviceTimeout
	if timeout <= 0 {
		timeout = defaultIPCServiceTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// request dials the daemon, sends one framed command and reads the reply.
func (c *IPCClient) request(ctx context.Context, command uint32, payload []byte) (uint32, string, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return 0, "", fmt.Errorf("ipc dial %s: %w", c.socketPath, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	frame := encodeIPCRequest(command, payload)
	if _, err := conn.Write(frame); err != nil {
		return 0, "", fmt.Errorf("ipc write request: %w", err)
	}

	status, message, err := readIPCReply(conn)
	if err != nil {
		return 0, "", fmt.Errorf("ipc read reply: %w", err)
	}
	return status, message, nil
}

// encodeIPCRequest builds a request frame: magic | command | payload-len | payload.
func encodeIPCRequest(command uint32, payload []byte) []byte {
	frame := make([]byte, ipcHeaderBytes+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], ipcMagic)
	binary.BigEndian.PutUint32(frame[4:8], command)
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(payload)))
	copy(frame[ipcHeaderBytes:], payload)
	return frame
}

// readIPCReply reads a reply frame: magic | status | message-len | message.
func readIPCReply(conn net.Conn) (uint32, string, error) {
	header := make([]byte, ipcHeaderBytes)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, "", err
	}
	if binary.BigEndian.Uint32(header[0:4]) != ipcMagic {
		return 0, "", fmt.Errorf("bad magic")
	}
	status := binary.BigEndian.Uint32(header[4:8])
	msgLen := binary.BigEndian.Uint32(header[8:12])
	if msgLen > ipcMaxPayload {
		return 0, "", fmt.Errorf("reply too large: %d bytes", msgLen)
	}
	msg := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return 0, "", err
	}
	return status, string(msg), nil
}
