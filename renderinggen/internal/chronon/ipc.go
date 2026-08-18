package chronon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// IPC wire constants — must match Chronon3d's chronon_ipc.hpp.
const (
	ipcMagic            uint32 = 0x43484e33 // "CHN3"
	ipcHeaderBytes             = 12         // magic + command/status + payload-len
	ipcMaxPayload              = 64 * 1024 * 1024
	ipcCommandStatus           = 4
	ipcCommandShutdown         = 5
	ipcCommandRenderJob        = 6
	ipcStatusOk                = 0
	ipcStatusError             = 1
	ipcStatusNotFound          = 2
	ipcStatusBadRequest        = 3
	ipcStatusShutdown          = 4
)

// IPCClient renders through the persistent Chronon3d render daemon over a
// UNIX-domain socket. It implements Renderer, so it is a drop-in replacement
// for the CLI subprocess Client.
type IPCClient struct {
	socketPath string
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
	status, message, err := c.request(ctx, ipcCommandStatus, nil)
	if err != nil {
		return "", err
	}
	if status != ipcStatusOk {
		return "", fmt.Errorf("ipc status: daemon status %d: %s", status, message)
	}
	return message, nil
}

// Shutdown asks the daemon to stop serving and exit.
func (c *IPCClient) Shutdown(ctx context.Context) error {
	status, message, err := c.request(ctx, ipcCommandShutdown, nil)
	if err != nil {
		return err
	}
	if status != ipcStatusShutdown && status != ipcStatusOk {
		return fmt.Errorf("ipc shutdown: daemon status %d: %s", status, message)
	}
	return nil
}

// renderJobPayload is the JSON payload for the RENDER_JOB IPC command.
type renderJobPayload struct {
	PlanPath   string `json:"plan_path"`
	AssetsRoot string `json:"assets_root"`
	Output     string `json:"output"`
	Backend    string `json:"backend"`
	Report     bool   `json:"report"`
}

// renderJobReply is the JSON reply returned by a successful RENDER_JOB.
type renderJobReply struct {
	Status string `json:"status"`
	Output string `json:"output"`
}

// Render sends a RENDER_JOB command to the daemon and waits for its reply.
func (c *IPCClient) Render(ctx context.Context, req RenderRequest) error {
	payload, err := json.Marshal(renderJobPayload{
		PlanPath:   req.PlanPath,
		AssetsRoot: req.AssetsRoot,
		Output:     req.OutputPath,
		Backend:    req.Backend,
		Report:     req.Report,
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
		// Non-JSON Ok replies are tolerated (backward compatibility).
		return nil
	}
	if reply.Status != "" && reply.Status != "ok" {
		return fmt.Errorf("ipc render: %s", reply.Status)
	}
	return nil
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
