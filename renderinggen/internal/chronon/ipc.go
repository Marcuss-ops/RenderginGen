package chronon

// IPC transport core: wire protocol mirror, connection lifecycle, framing and
// error mapping. Commands live in ipc_commands.go (status/prefetch/shutdown/
// assemble), ipc_render.go (RENDER_JOB/RENDER_BATCH) and ipc_watchdog.go
// (render liveness polling).

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// IPC wire constants — must match Chronon3d's chronon_ipc.hpp.
//
// The command and status enums are mirrored COMPLETE from the header, even
// where this client never sends a value (ipcCommandPreparePlan,
// ipcCommandRenderOverlay) or never compares against one (ipcStatusNotFound,
// ipcStatusBadRequest). They are the wire contract, not locals: a partial
// enum would silently misnumber the values after the first omission, and a
// future caller (or a daemon that starts returning "not found" for a
// missing-asset prefetch) needs the constant to exist. Contract surface, not
// dead code — do not trim it to the currently-referenced subset.
//
// "Mirrored by hand" is the real hazard, so the mirror is no longer only a
// comment: ipc_contract_test.go reads chronon_ipc.hpp from the sibling
// Chronon3d checkout, when one is present, and asserts every constant below
// against it. A value that shifts in the header fails that test instead of
// silently renumbering a command the daemon will interpret as another.
const (
	ipcMagic                   uint32 = 0x43484e33 // "CHN3"
	ipcHeaderBytes                    = 12         // magic + command/status + payload-len
	ipcMaxPayload                     = 64 * 1024 * 1024
	ipcCommandPrefetchAsset           = 1
	ipcCommandPreparePlan             = 2
	ipcCommandRenderOverlay           = 3
	ipcCommandStatus                  = 4
	ipcCommandShutdown                = 5
	ipcCommandRenderJob               = 6
	ipcCommandAssembleSegments        = 7
	ipcCommandGetJobStatus            = 8
	ipcCommandRenderBatch             = 9
	ipcCommandCacheStats              = 10
	ipcMaxBatchJobs                   = 64
	ipcStatusOk                       = 0
	ipcStatusError                    = 1
	ipcStatusNotFound                 = 2
	ipcStatusBadRequest               = 3
	ipcStatusShutdown                 = 4
)

// ipcProtocolVersion mirrors the header's kProtocolVersion.
//
// It is NOT transmitted: the 12-byte header is magic | command-or-status |
// payload-len, so neither side can detect a version skew on the wire. That
// makes the version a declaration about THIS file — "these constants were
// mirrored from protocol revision 1" — which is exactly what the contract test
// verifies against the header. It is named here so a revision bump has one
// place to land and one test to fail, rather than being an invisible fact
// about a hand-copied enum.
const ipcProtocolVersion uint32 = 1

// ipcCommandNames is the command enum by value. It is the mirror the contract
// test compares against the header, and the source of the name in any error a
// caller has to read.
var ipcCommandNames = map[uint32]string{
	ipcCommandPrefetchAsset:    "PrefetchAsset",
	ipcCommandPreparePlan:      "PreparePlan",
	ipcCommandRenderOverlay:    "RenderOverlay",
	ipcCommandStatus:           "Status",
	ipcCommandShutdown:         "Shutdown",
	ipcCommandRenderJob:        "RenderJob",
	ipcCommandAssembleSegments: "AssembleSegments",
	ipcCommandGetJobStatus:     "GetJobStatus",
	ipcCommandRenderBatch:      "RenderBatch",
	ipcCommandCacheStats:       "CacheStats",
}

// ipcStatusNames is the reply-status enum by value.
//
// Unlike the command enum, this one must be COMPLETE: every reply carries a
// status, so a value this map does not know is a status the client cannot
// interpret. describeIPCStatus turns exactly that case into a named "unknown
// status" failure instead of a bare number that reads like an ordinary daemon
// error (see ipcReplyError).
var ipcStatusNames = map[uint32]string{
	ipcStatusOk:         "Ok",
	ipcStatusError:      "Error",
	ipcStatusNotFound:   "NotFound",
	ipcStatusBadRequest: "BadRequest",
	ipcStatusShutdown:   "Shutdown",
}

// describeIPCStatus renders a reply status for an error message and reports
// whether the value is one this client's mirror knows. known=false means
// protocol drift: the daemon spoke a status from a revision this client was not
// built against, so the numeric code alone must not be trusted to mean
// "failure".
func describeIPCStatus(status uint32) (string, bool) {
	if name, ok := ipcStatusNames[status]; ok {
		return name, true
	}
	return "", false
}

// ipcReplyError is the single formatter for a non-ok reply status, so every
// IPC call site reports a daemon refusal the same way and an unrecognized code
// is reported as drift rather than as just another error. (The name avoids
// ipcStatusError, which is the wire constant for the Error status itself.)
func ipcReplyError(op string, status uint32, message string) error {
	if name, known := describeIPCStatus(status); known {
		return fmt.Errorf("ipc %s: daemon status %s (%d): %s", op, name, status, message)
	}
	return fmt.Errorf("ipc %s: daemon status %d is not in this client's protocol v%d mirror; the daemon may be speaking a newer revision: %s",
		op, status, ipcProtocolVersion, message)
}

// defaultIPCServiceTimeout bounds the daemon operations that are NOT the
// per-job render (Status, PrefetchAsset, Shutdown). Those are commonly invoked
// with context.Background(), so without a bound a hung daemon blocks the caller
// forever. The per-job Render/Assemble calls carry the caller's own (usually
// lane-derived) context and deliberately keep no default, because a legitimate
// render may outlive any service timeout. The bound exists to fail, not to be
// tight.
const defaultIPCServiceTimeout = 90 * time.Second

// ErrIPCStallTimeout identifies an IPC render aborted because the daemon made
// no observable progress. It is a transport-level failure, not a semantic
// render error: the processor may recover it through SoftwareRetryAllowed
// instead of failing the job permanently.
var ErrIPCStallTimeout = errors.New("ipc render stalled")

// IPCClient renders through the persistent Chronon3d render daemon over a
// UNIX-domain socket. It implements Renderer, so it is a drop-in replacement
// for the CLI subprocess Client.
//
// One connection per command — deliberate, not an oversight.
//
// The daemon DOES accept many frames per connection: chronon_ipc.hpp
// serve_once() loops until EOF, and daemon_service_ipc.cpp serves via
// serve_concurrent() (one thread per connection). So a persistent pooled
// connection is expressible, and the audit flagged the per-command dial as
// the largest remaining per-job syscall on this path. It is kept anyway, and
// the reasoning is deliberate:
//
//  1. Payoff is negligible where it was claimed. A unix-domain
//     socket()/connect() is a few microseconds against a render that takes
//     seconds; there is no measured job-level effect to recover.
//  2. A pool changes failure semantics on the critical path. Today a daemon
//     restart between jobs is transparent — the next command simply dials the
//     new listener. A pooled connection made stale by a restart turns the
//     FIRST render after the restart into a job failure.
//  3. The usual mitigation is unavailable. Retrying a stale write is safe for
//     Status, but RENDER_JOB is not idempotent: a write that reached the
//     daemon before the transport broke may already be rendering, so an
//     automatic retry could double-render. Fail-closed is the correct policy
//     for this command, and fail-closed is what a fresh dial gives for free.
//  4. This client is shared by concurrent GPU lanes (see LimitConcurrency),
//     so a pool needs exclusive borrow plus cap management — state and failure
//     modes that buy nothing today.
//
// Revisit only with a measurement showing per-job dials are visible in the
// job profile, and only with a stale-connection probe that cannot retry a
// non-idempotent command.
type IPCClient struct {
	socketPath string
	// serviceTimeout bounds Status/PrefetchAsset/Shutdown. Zero uses
	// defaultIPCServiceTimeout; tests shorten it.
	serviceTimeout time.Duration
	// stallTimeout is measured from observable output-file progress. IPC has no
	// frame stream yet, so a new/unchanged partial output is the transport's
	// liveness signal.
	stallTimeout time.Duration
}

// NewIPCClient creates a Renderer that talks to a Chronon3d daemon listening
// at socketPath.
func NewIPCClient(socketPath string) *IPCClient {
	return &IPCClient{socketPath: socketPath, stallTimeout: DefaultStallTimeout}
}

// SetStallTimeout configures the maximum time an IPC render may make no
// observable output-file progress before its request connection is closed.
func (c *IPCClient) SetStallTimeout(timeout time.Duration) {
	if timeout > 0 {
		c.stallTimeout = timeout
	}
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

// ipcRequestContextError maps a transport failure to the caller's context
// error. The socket deadline is installed from ctx's deadline, so an i/o
// timeout is that deadline expiring even if the timer goroutine has not yet
// run and ctx.Err() is still nil at the instant the syscall returns.
func ipcRequestContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("ipc request canceled: %w", ctxErr)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		if _, ok := ctx.Deadline(); ok {
			return fmt.Errorf("ipc request canceled: %w", context.DeadlineExceeded)
		}
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
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	if deadline, ok := ctx.Deadline(); ok {
		// The deadline IS the bound of this transport (see
		// defaultIPCServiceTimeout): a connection whose deadline could not be
		// installed can block the caller past every timeout this client
		// promises, so the failure must surface here rather than later as a
		// hang.
		if err := conn.SetDeadline(deadline); err != nil {
			return 0, "", fmt.Errorf("ipc set deadline on %s: %w", c.socketPath, err)
		}
	}

	frame := encodeIPCRequest(command, payload)
	if _, err := conn.Write(frame); err != nil {
		if canceled := ipcRequestContextError(ctx, err); canceled != nil {
			return 0, "", canceled
		}
		return 0, "", fmt.Errorf("ipc write request: %w", err)
	}

	status, message, err := readIPCReply(conn)
	if err != nil {
		if canceled := ipcRequestContextError(ctx, err); canceled != nil {
			return 0, "", canceled
		}
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
