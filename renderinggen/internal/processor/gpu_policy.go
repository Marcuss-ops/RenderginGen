package processor

import (
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/queue"
	"strings"
)

// isFinalJobComposite recognizes the local scene-composite jobs emitted by
// PipelineGen's final_job pipeline. The namespace is part of the queue job ID
// contract (e.g. <plan>:final-composite:<scene>); the exception is limited to
// these intermediate scene renders and does not alter other worker traffic.
func isFinalJobComposite(job *queue.Job) bool {
	return job != nil && strings.Contains(job.ID, ":final-composite:")
}
func softwareRetryableGPUFailure(err error) bool {
	if err == nil {
		return false
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
