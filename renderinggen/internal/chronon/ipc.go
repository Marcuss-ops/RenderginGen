package chronon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// IPC cannot observe frame milestones today, so it cannot use the CLI
// no-progress watchdog. Bound each blocking daemon call by a generous budget
// proportional to the requested frame count. This is a hard render deadline,
// not a claim that the transport can detect a stalled frame.
const (
	defaultIPCRenderTimeout = 90 * time.Second
	maxIPCRenderTimeout     = 15 * time.Minute
	ipcRenderBudgetPerFrame = 500 * time.Millisecond
)

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

// renderJobPayload is the JSON payload for the RENDER_JOB IPC command.  It is
// the daemon-side mirror of the full RenderRequest semantic contract:
//
//	plan_path / prepared_package_path / assets_root / output / report
//	range_enabled + first_frame + last_frame  (explicit chunk range; a
//	  single-frame chunk at frame 0 is first=0, last=0 WITH range_enabled
//	  true — never an "absent range" that silently re-expands to the whole
//	  plan)
//	parallel_disjoint (trusted chunk-child admission hint; never inferred from
//	  a range alone)
//	audio_source_path (muxed source audio, daemon-side --gop-source)
//	encode_preset (native NVENC tier)
//	receipt_verify (per-job verification policy; the daemon reads the same
//	  policy the CLI subprocess receives through CHRONON_RECEIPT_VERIFY)
//	no_dirty_rects (per-job opt-out of the dirty-rectangle optimization; the
//	  daemon applies it as the CLI flag of the same name)
//	execution_requirements / output_spec (semantic, backend-neutral)
//
// first_frame/last_frame are marshaled unconditionally (no omitempty) so 0
// coordinates are expressible; the daemon honors them only when range_enabled
// is true.  Older daemons ignore the extra keys and keep rendering the whole
// plan.
type renderJobPayload struct {
	// JobID is transport metadata, never part of the render semantics: the
	// daemon uses it to make GET_JOB_STATUS answer for this render.
	JobID                 string                `json:"job_id,omitempty"`
	PlanPath              string                `json:"plan_path"`
	PreparedPackagePath   string                `json:"prepared_package_path,omitempty"`
	AssetsRoot            string                `json:"assets_root"`
	Output                string                `json:"output"`
	RangeEnabled          bool                  `json:"range_enabled"`
	FirstFrame            int64                 `json:"first_frame"`
	LastFrame             int64                 `json:"last_frame"`
	ParallelDisjoint      bool                  `json:"parallel_disjoint,omitempty"`
	Report                bool                  `json:"report"`
	AudioSourcePath       string                `json:"audio_source_path,omitempty"`
	AudioTargetSampleRate int                   `json:"audio_target_sample_rate,omitempty"`
	EncodePreset          string                `json:"encode_preset,omitempty"`
	ReceiptVerify         string                `json:"receipt_verify,omitempty"`
	NoDirtyRects          bool                  `json:"no_dirty_rects,omitempty"`
	ExecutionRequirements ExecutionRequirements `json:"execution_requirements"`
	OutputSpec            OutputSpec            `json:"output_spec"`
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
		return ipcReplyError("assemble", status, message)
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
	// P4 batch superjob: the per-item windows ride in output_spec like every
	// other output-contract field (the daemon's closed vocabulary reads them
	// there first, falling back to the payload root). The request-level
	// OutputSegments is the single semantic source; the wire copy happens
	// here so no call site can forget it.
	if len(req.OutputSegments) > 0 {
		spec.OutputSegments = req.OutputSegments
	}
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

// SupportsOutputSegments: the daemon IPC wire carries per-item output windows
// in output_spec.output_segments (P4 batch superjob).
func (c *IPCClient) SupportsOutputSegments() bool { return true }

// renderJobPayloadFor is the single RENDER_JOB body builder, shared by the
// single-job and batch commands so the two transports cannot drift.
func renderJobPayloadFor(req RenderRequest) renderJobPayload {
	return renderJobPayload{
		JobID:               req.JobID,
		PlanPath:            req.PlanPath,
		PreparedPackagePath: req.PreparedPackagePath,
		AssetsRoot:          req.AssetsRoot,
		Output:              req.OutputPath,
		// Range parity: coordinates are always marshaled (0 is meaningful)
		// and range_enabled marks them as an explicit chunk. A whole-plan
		// render is range_enabled=false even when the coordinates are 0.
		RangeEnabled:          req.RangeEnabled,
		FirstFrame:            req.FirstFrame,
		LastFrame:             req.LastFrame,
		ParallelDisjoint:      req.ParallelDisjoint,
		Report:                req.Report,
		AudioSourcePath:       req.AudioSourcePath,
		AudioTargetSampleRate: req.AudioTargetSampleRate,
		EncodePreset:          req.EncodePreset,
		ReceiptVerify:         req.ReceiptVerify,
		NoDirtyRects:          req.NoDirtyRects,
		ExecutionRequirements: req.Requirements,
		OutputSpec:            outputSpecForTransport(req),
	}
}

// Render sends a RENDER_JOB command to the daemon and waits for its reply.
func (c *IPCClient) Render(ctx context.Context, req RenderRequest) error {
	if err := validateRenderRequest(req); err != nil {
		return err
	}
	payload, err := json.Marshal(renderJobPayloadFor(req))
	if err != nil {
		return fmt.Errorf("ipc render: marshal payload: %w", err)
	}

	renderCtx, cancel := ipcRenderContext(ctx, req)
	defer cancel()
	outputs := []string{req.OutputPath}
	for _, seg := range req.OutputSegments {
		outputs = append(outputs, seg.OutputPath)
	}
	status, message, err := c.requestRenderWithProgress(renderCtx, ipcCommandRenderJob, payload, []string{req.JobID}, outputs)
	if err != nil {
		return err
	}
	if status != ipcStatusOk {
		return ipcReplyError("render", status, message)
	}

	var reply renderJobReply
	if err := json.Unmarshal([]byte(message), &reply); err != nil {
		// An unparsable reply under an "ok" status is only tolerable as the
		// fast-tier compatibility behavior (an older/other daemon answering a
		// plain "ok"). Under normal/certify the policy claims proof of the
		// render, so a daemon whose reply cannot be parsed cannot be credited
		// with having rendered: fail closed. Fast keeps the tolerance but never
		// silently — a daemon that starts answering corrupt payloads must leave
		// a trace instead of passing unobserved.
		if RequiresStructuredReply(req.ReceiptVerify) {
			return fmt.Errorf("ipc render: daemon replied ok with an unparsable body under verification policy %q: %w", req.ReceiptVerify, err)
		}
		log.Printf("[chronon ipc WARN] render reply status=ok but body is not JSON (tolerated under the fast tier): %v", err)
		return nil
	}
	if reply.Status != "" && reply.Status != "ok" {
		return fmt.Errorf("ipc render: %s", reply.Status)
	}
	return nil
}

// BatchRenderResult is one job's outcome inside a RENDER_BATCH reply.
//
// The daemon executes the jobs in order over ONE connection and reports every
// entry, including the failed ones: a batch is not atomic, and a caller must be
// able to attribute a failure to the request that caused it.
type BatchRenderResult struct {
	Index   int
	Status  string
	Message string
}

// RenderBatch sends N RENDER_JOB bodies in one RENDER_BATCH frame.
//
// Why it is a separate method instead of a loop of Render calls: the daemon
// keeps the connection, the render domain and the encoder warm for the whole
// sequence, and the client pays one dial/teardown instead of N. The daemon
// still serializes execution per socket (m_render_job_mutex) and still gates
// VRAM through its own admission, so batching does not multiply GPU sessions.
//
// Results are returned whenever the reply parsed, even when the daemon answered
// with an error status, because the per-job outcomes are the actionable part.
// The error then names the failed indices. Transport, validation and body-shape
// failures return no results.
func (c *IPCClient) RenderBatch(ctx context.Context, requests []RenderRequest) ([]BatchRenderResult, error) {
	if len(requests) == 0 {
		return nil, fmt.Errorf("ipc render batch: at least one request is required")
	}
	if len(requests) > ipcMaxBatchJobs {
		return nil, fmt.Errorf("ipc render batch: %d requests exceed the daemon limit of %d", len(requests), ipcMaxBatchJobs)
	}
	jobs := make([]renderJobPayload, 0, len(requests))
	for i, req := range requests {
		if err := validateRenderRequest(req); err != nil {
			return nil, fmt.Errorf("ipc render batch: request %d: %w", i, err)
		}
		jobs = append(jobs, renderJobPayloadFor(req))
	}
	payload, err := json.Marshal(struct {
		Jobs []renderJobPayload `json:"jobs"`
	}{Jobs: jobs})
	if err != nil {
		return nil, fmt.Errorf("ipc render batch: marshal payload: %w", err)
	}
	frames := int64(0)
	for _, req := range requests {
		frames += requestedFrames(req)
	}
	renderCtx, cancel := ipcRenderContextForFrames(ctx, frames)
	defer cancel()
	outputs := make([]string, 0, len(requests))
	jobIDs := make([]string, 0, len(requests))
	for _, req := range requests {
		outputs = append(outputs, req.OutputPath)
		for _, seg := range req.OutputSegments {
			outputs = append(outputs, seg.OutputPath)
		}
		jobIDs = append(jobIDs, req.JobID)
	}
	status, message, err := c.requestRenderWithProgress(renderCtx, ipcCommandRenderBatch, payload, jobIDs, outputs)
	if err != nil {
		return nil, err
	}
	var reply struct {
		Results []struct {
			Index   int    `json:"index"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(message), &reply); err != nil {
		return nil, fmt.Errorf("ipc render batch: unparsable reply body under status %d: %w", status, err)
	}
	if len(reply.Results) != len(requests) {
		return nil, fmt.Errorf("ipc render batch: daemon reported %d result(s) for %d request(s)", len(reply.Results), len(requests))
	}
	results := make([]BatchRenderResult, 0, len(reply.Results))
	failures := make([]string, 0)
	for _, entry := range reply.Results {
		results = append(results, BatchRenderResult{
			Index: entry.Index, Status: entry.Status, Message: entry.Message,
		})
		if entry.Status != "ok" {
			failures = append(failures, fmt.Sprintf("#%d: %s", entry.Index, entry.Message))
		}
	}
	if status != ipcStatusOk && len(failures) == 0 {
		return results, ipcReplyError("render batch", status, message)
	}
	if len(failures) > 0 {
		return results, fmt.Errorf("ipc render batch: %d/%d job(s) failed: %s",
			len(failures), len(requests), strings.Join(failures, "; "))
	}
	return results, nil
}

// ipcRenderContext imposes a hard bound on an IPC render. Existing caller
// deadlines remain authoritative because context.WithTimeout keeps the earlier
// deadline.
func ipcRenderContext(ctx context.Context, req RenderRequest) (context.Context, context.CancelFunc) {
	return ipcRenderContextForFrames(ctx, requestedFrames(req))
}

func ipcRenderContextForFrames(ctx context.Context, frames int64) (context.Context, context.CancelFunc) {
	// Unknown whole-plan duration gets the maximum bounded budget; using the
	// minimum in that case would punish plans whose duration metadata could
	// not be read by this client.
	timeout := maxIPCRenderTimeout
	maxFrames := int64(maxIPCRenderTimeout / ipcRenderBudgetPerFrame)
	if frames > 0 {
		budgetFrames := frames
		if budgetFrames > maxFrames {
			budgetFrames = maxFrames
		}
		timeout = time.Duration(budgetFrames) * ipcRenderBudgetPerFrame
		if timeout < defaultIPCRenderTimeout {
			timeout = defaultIPCRenderTimeout
		}
	}
	return context.WithTimeout(ctx, timeout)
}

func requestedFrames(req RenderRequest) int64 {
	if req.RangeEnabled {
		if req.LastFrame >= req.FirstFrame {
			return req.LastFrame - req.FirstFrame + 1
		}
		return 0
	}
	if strings.TrimSpace(req.PlanPath) == "" {
		return 0
	}
	data, err := os.ReadFile(req.PlanPath)
	if err != nil {
		return 0
	}
	var plan struct {
		Canvas struct {
			DurationFrames int64 `json:"duration_frames"`
		} `json:"canvas"`
	}
	if err := json.Unmarshal(data, &plan); err != nil || plan.Canvas.DurationFrames < 0 {
		return 0
	}
	return plan.Canvas.DurationFrames
}

type ipcRequestResult struct {
	status  uint32
	message string
	err     error
}

type outputStamp struct {
	size    int64
	modTime time.Time
	valid   bool
}

const ipcStatusPollTimeout = 5 * time.Second

// requestRenderWithProgress keeps the non-idempotent render request on its own
// connection while a second connection observes liveness. The signals are the
// daemon lifecycle (GET_JOB_STATUS) and the bytes the daemon writes to the
// output or its .chronon.tmp sibling. The daemon does not expose a frame
// counter today, so output growth is the frame-progress proxy; a running job
// that produces neither is aborted with ErrIPCStallTimeout.
func (c *IPCClient) requestRenderWithProgress(ctx context.Context, command uint32, payload []byte, jobIDs, outputs []string) (uint32, string, error) {
	stallTimeout := c.stallTimeout
	if stallTimeout <= 0 {
		stallTimeout = DefaultStallTimeout
	}
	result := make(chan ipcRequestResult, 1)
	go func() {
		status, message, err := c.request(ctx, command, payload)
		result <- ipcRequestResult{status: status, message: message, err: err}
	}()
	lastProgress := time.Now()
	stamps := make([]outputStamp, len(outputs))
	states := make(map[string]string, len(jobIDs))
	framesSeen := make(map[string]int64, len(jobIDs))
	sawStatus := false
	running := false
	pollInterval := 2 * time.Second
	if stallTimeout < pollInterval {
		pollInterval = stallTimeout
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case response := <-result:
			return response.status, response.message, response.err
		case <-ctx.Done():
			return 0, "", fmt.Errorf("ipc render request ended: %w", ctx.Err())
		case now := <-ticker.C:
			progress := outputFilesChanged(outputs, stamps)
			running = false
			for _, jobID := range jobIDs {
				if strings.TrimSpace(jobID) == "" {
					continue
				}
				snapshot, ok := c.ipcJobState(ctx, jobID)
				if !ok {
					continue
				}
				sawStatus = true
				if states[jobID] != snapshot.state {
					states[jobID] = snapshot.state
					progress = true
				}
				// A newer frame counter is real progress even when the output file
				// has not grown yet (the encoder may be buffering). Older daemons
				// omit the field, so zero is indistinguishable from "not reported"
				// and must never count as advancement.
				if snapshot.framesDone > framesSeen[jobID] {
					framesSeen[jobID] = snapshot.framesDone
					progress = true
				}
				// prepared is a live admission wait, not a render stall.
				if snapshot.state == "prepared" || snapshot.state == "completed" || snapshot.state == "failed" {
					progress = true
				}
				if snapshot.state == "running" {
					running = true
				}
			}
			if progress {
				lastProgress = now
				continue
			}
			// With a job id, a prepared admission wait is live and must not be
			// mistaken for a stall; only a running job is held to the window.
			// Without a job id (legacy callers) there is no lifecycle signal,
			// so output growth is the only observable and silence still aborts.
			if (!sawStatus || running) && now.Sub(lastProgress) >= stallTimeout {
				return 0, "", fmt.Errorf("%w: job(s) %s made no observable progress for %s", ErrIPCStallTimeout, jobIDList(jobIDs), stallTimeout)
			}
		}
	}
}

// ipcJobSnapshot is the liveness projection of one GET_JOB_STATUS reply. The
// frame counters are optional on the wire: a daemon built before this field
// simply reports zero, which the watchdog treats as "not reported".
type ipcJobSnapshot struct {
	state       string
	framesDone  int64
	framesTotal int64
}

// parseIPCJobStatus decodes the reply body. It is a pure function so the frame
// counter contract is testable without a socket.
func parseIPCJobStatus(message string) (ipcJobSnapshot, bool) {
	var reply struct {
		State       string `json:"state"`
		FramesDone  int64  `json:"frames_done"`
		FramesTotal int64  `json:"frames_total"`
	}
	if err := json.Unmarshal([]byte(message), &reply); err != nil || reply.State == "" {
		return ipcJobSnapshot{}, false
	}
	return ipcJobSnapshot{
		state:       reply.State,
		framesDone:  reply.FramesDone,
		framesTotal: reply.FramesTotal,
	}, true
}

// ipcJobState reads one daemon lifecycle record on its own connection. A
// missing/expired job or an older daemon is not a stall signal: the err/ok
// result keeps that distinction available to the watchdog.
func (c *IPCClient) ipcJobState(ctx context.Context, jobID string) (ipcJobSnapshot, bool) {
	pollCtx, cancel := context.WithTimeout(ctx, ipcStatusPollTimeout)
	defer cancel()
	status, message, err := c.request(pollCtx, ipcCommandGetJobStatus, []byte(jobID))
	if err != nil || status != ipcStatusOk {
		return ipcJobSnapshot{}, false
	}
	return parseIPCJobStatus(message)
}

func jobIDList(jobIDs []string) string {
	ids := make([]string, 0, len(jobIDs))
	for _, id := range jobIDs {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "untracked"
	}
	return strings.Join(ids, ",")
}

// outputFilesChanged reports whether the bytes visible for any output changed.
// It includes the daemon's atomic-write sibling (output + ".chronon.tmp.*")
// because the final path does not exist until the mux is published.
func outputFilesChanged(paths []string, previous []outputStamp) bool {
	changed := false
	for i, path := range paths {
		if i >= len(previous) {
			break
		}
		current := outputStampFor(path)
		if current != previous[i] {
			previous[i] = current
			changed = true
		}
	}
	return changed
}

func outputStampFor(path string) outputStamp {
	if strings.TrimSpace(path) == "" {
		return outputStamp{}
	}
	var stamp outputStamp
	record := func(candidate string) {
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		stamp.size += info.Size()
		if info.ModTime().After(stamp.modTime) {
			stamp.modTime = info.ModTime()
		}
		stamp.valid = true
	}
	record(path)
	if matches, err := filepath.Glob(path + ".chronon.tmp.*"); err == nil {
		for _, match := range matches {
			record(match)
		}
	}
	return stamp
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
