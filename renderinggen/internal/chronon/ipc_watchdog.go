package chronon

// IPC render watchdog: the non-idempotent render request owns one connection
// while a second connection observes daemon lifecycle and output progress.
// A render that produces neither is aborted with ErrIPCStallTimeout.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
