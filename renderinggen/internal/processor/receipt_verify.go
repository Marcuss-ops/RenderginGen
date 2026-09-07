// receipt_verify.go owns the single output-verification policy authority on
// the worker: the policy is resolved once (fast/normal/certify), requested
// explicitly from Chronon and enforced from Chronon's receipt — never
// duplicated by a second worker-side decode.
package processor

import (
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/chronon"
)

type renderVerifyLevel string

const (
	renderVerifyFast    renderVerifyLevel = "fast"
	renderVerifyNormal  renderVerifyLevel = "normal"
	renderVerifyCertify renderVerifyLevel = "certify"
)

// renderVerificationLevel is the SINGLE verification-policy authority on the
// worker. fast (the production default) verifies container/stream metadata
// without re-decoding the freshly muxed output; normal performs one complete
// decode; certify is the explicit correctness mode used by CI/golden
// benchmarks. Only RENDERINGGEN_RECEIPT_VERIFY is read here — the historical
// CHRONON_RECEIPT_VERIFY alias is deliberately NOT consulted, so the worker
// and its Chronon subprocess cannot run different policies. The resolved
// level is forwarded explicitly to the CLI (chronon.RenderRequest.
// ReceiptVerify -> CHRONON_RECEIPT_VERIFY on the subprocess env) by RunGPU.
func renderVerificationLevel() renderVerifyLevel {
	switch renderVerifyLevel(os.Getenv("RENDERINGGEN_RECEIPT_VERIFY")) {
	case renderVerifyNormal:
		return renderVerifyNormal
	case renderVerifyCertify:
		return renderVerifyCertify
	default:
		return renderVerifyFast
	}
}

// enforceReceiptVerification makes the worker enforce — never duplicate —
// Chronon's canonical output verification. Under normal/certify the receipt
// is mandatory evidence: Chronon promised a full decode there, and a missing
// receipt means the canonical verifier did not run, so the artifact is
// rejected (fail closed) rather than silently accepted on the worker's own
// probe. Under fast a missing receipt is tolerated: the gate is the
// encoder/muxer success plus the profile/overlay probe validation below. When
// the receipt is present the aggregate verification status must be pass at
// every policy — Chronon's media checks (container, codec, pixel format,
// resolution, fps, audio, optional decode) are the verdict.
func (p *Processor) enforceReceiptVerification(outputPath string) error {
	receipt, err := chronon.ReadMediaReceipt(outputPath)
	policy := renderVerificationLevel()
	if err != nil {
		if policy == renderVerifyFast {
			// Fast never promises a decode; the output is certified by the
			// in-process encoder/muxer success and the probe validation below.
			return nil
		}
		return fmt.Errorf("processor: canonical verification did not run (policy=%s): %w", policy, err)
	}
	if !receipt.VerificationPassed() {
		// A failing aggregate without a granular explanation is the worst
		// failure mode to debug ("render finished, receipt failed, why?").
		// The receipt JSON carries per-check verdicts; surface every failing
		// check in the job error so a re-render is never needed just to learn
		// which contract check rejected the output.
		if failures := receipt.VerificationFailures(); len(failures) > 0 {
			return fmt.Errorf("processor: Chronon receipt verification failed (policy=%s status=%q failing_checks=%s)",
				policy, receipt.Verification.Status, strings.Join(failures, ","))
		}
		return fmt.Errorf("processor: Chronon receipt verification failed (policy=%s status=%q)", policy, receipt.Verification.Status)
	}
	return nil
}

// deepVisualValidationEnabled reports whether the sampled ffmpeg visual
// validation should run for this job. Opt-in via RENDERINGGEN_DEEP_VISUAL=1:
// CI and certification runs enable it; the production hot path relies on the
// Chronon receipt gate (requireNativeVulkan + media receipt) and pays no
// extra ffmpeg processes per render.
func deepVisualValidationEnabled() bool {
	return os.Getenv("RENDERINGGEN_DEEP_VISUAL") == "1"
}
