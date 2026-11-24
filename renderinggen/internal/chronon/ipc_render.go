package chronon

// RENDER_JOB and RENDER_BATCH over IPC: payload shape, output-spec stamping,
// reply handling and the per-render time budget.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// IPC cannot observe frame milestones today, so it cannot use the CLI
// no-progress watchdog. Bound each blocking daemon call by a generous budget
// proportional to the requested frame count. This is a hard render deadline,
// not a claim that the transport can detect a stalled frame.
const (
	defaultIPCRenderTimeout = 90 * time.Second
	maxIPCRenderTimeout     = 15 * time.Minute
	// Image-heavy overlays need more headroom than the former 500 ms/frame
	// budget when several Chronon daemons share a GPU. A 120-frame overlay
	// gets three minutes instead of timing out while valid work is in flight.
	ipcRenderBudgetPerFrame = 1500 * time.Millisecond
)

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

type renderJobReply struct {
	Status string `json:"status"`
	Output string `json:"output"`
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
