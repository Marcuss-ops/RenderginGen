package processor

import (
	"errors"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
)

// isFinalJobComposite recognizes the local scene-composite jobs emitted by
// PipelineGen's final_job pipeline. The namespace is part of the queue job ID
// contract (e.g. <plan>:final-composite:<scene>); the exception is limited to
// these intermediate scene renders and does not alter other worker traffic.
func isFinalJobComposite(job *queue.Job) bool {
	return job != nil && strings.Contains(job.ID, ":final-composite:")
}

// overlayDefaultEncodePreset is the throughput preset for overlay artifacts:
// silent, video-only intermediates whose encode tail is on the critical path.
// The B2 1080p fixture measured 285-335 fps at p4 (mean 317) against 234 at slow.
// Final composites are deliverables and keep the engine default instead.
const overlayDefaultEncodePreset = "p4"

// encodePresetForJob resolves the GPU lane preset. An explicit worker/plan
// preset always wins; only an unset overlay inherits the throughput default.
func encodePresetForJob(configured string, finalComposite bool) string {
	if configured != "" || finalComposite {
		return configured
	}
	return overlayDefaultEncodePreset
}

func softwareRetryableGPUFailure(err error) bool {
	if err == nil {
		return false
	}
	// A transport stall is recoverable: the daemon session is aborted and the
	// same plan can be retried on the software lane instead of freezing the
	// job until the hard deadline. This is the IPC-02 recovery path.
	if errors.Is(err, chronon.ErrIPCStallTimeout) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"device lost",
		"segmentation fault",
		"unsupportedcapability",
		"no legacy-node fallback",
		"native residency violation",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
