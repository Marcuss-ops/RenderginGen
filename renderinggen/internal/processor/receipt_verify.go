// receipt_verify.go owns the single output-verification policy authority on
// the worker: the policy is resolved once (fast/normal/certify), requested
// explicitly from Chronon and enforced from Chronon's receipt — never
// duplicated by a second worker-side decode.
package processor

import (
	"fmt"
	"log"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
)

type renderVerifyLevel string

// The policy strings are aliases of chronon's wire vocabulary, which is also
// the spelling Chronon writes into the receipt and the spelling any consumer
// (queue, reports, an operator's config) sees. Declaring them once there keeps
// the worker's policy authority and the daemon boundary from drifting apart.
const (
	renderVerifyFast    renderVerifyLevel = chronon.ReceiptVerifyFast
	renderVerifyNormal  renderVerifyLevel = chronon.ReceiptVerifyNormal
	renderVerifyCertify renderVerifyLevel = chronon.ReceiptVerifyCertify
)

// receiptVerifyLevel is the SINGLE verification-policy authority on the
// worker. fast (the production default) verifies container/stream metadata
// without re-decoding the freshly muxed output; normal performs one complete
// decode; certify is the explicit correctness mode used by CI/golden
// benchmarks.
//
// The value comes from configuration (pipeline.receipt_verify, and through the
// aliased RENDERINGGEN_RECEIPT_VERIFY), never from a read inside this package:
// the historical CHRONON_RECEIPT_VERIFY alias is deliberately NOT consulted, so
// the worker and its Chronon subprocess cannot run different policies. config
// validates the accepted spellings at load, so anything else reaching here is a
// caller that built a Processor directly — which gets the fast default rather
// than an invented policy. The resolved level is forwarded explicitly to the CLI
// (chronon.RenderRequest.ReceiptVerify -> CHRONON_RECEIPT_VERIFY on the
// subprocess env) by RunGPU.
func (p *Processor) receiptVerifyLevel() renderVerifyLevel {
	switch p.receiptVerify {
	case renderVerifyNormal:
		return renderVerifyNormal
	case renderVerifyCertify:
		return renderVerifyCertify
	default:
		return renderVerifyFast
	}
}

// verifyArtifactReceipt (receipt_identity.go) is the gate's entry point; this
// is its policy half.
//
// enforceReceiptVerificationDirect makes the worker enforce — never duplicate —
// Chronon's canonical output verification. Under normal/certify the receipt
// is mandatory evidence: Chronon promised a full decode there, and a missing
// receipt means the canonical verifier did not run, so the artifact is
// rejected (fail closed) rather than silently accepted on the worker's own
// probe. Under fast a missing receipt is tolerated: the gate is the
// encoder/muxer success plus the profile/overlay probe validation below. When
// the receipt is present the aggregate verification status must be pass at
// every policy — Chronon's media checks (container, codec, pixel format,
// resolution, fps, audio, optional decode) are the verdict.
// The identity half of the gate is not optional and not policy-blind: it runs
// here, for every policy, and the store phase consumes its verdict. See
// receipt_identity.go.
func (p *Processor) enforceReceiptVerification(outputPath string, metrics map[string]float64) error {
	receipt, err := chronon.ReadMediaReceipt(outputPath)
	_, gateErr := p.verifyArtifactReceipt(outputPath, receipt, err, metrics)
	return gateErr
}

func (p *Processor) enforceReceiptVerificationDirect(receipt chronon.MediaReceipt, err error, metrics map[string]float64) error {
	policy := p.receiptVerifyLevel()
	if err != nil {
		if policy == renderVerifyFast {
			// Fast never promises a decode; the output is certified by the
			// in-process encoder/muxer success and the probe validation below.
			// The tolerance is deliberate (fail-open by policy), but it must
			// not be invisible: a Chronon that silently stopped writing
			// receipts would otherwise leave zero trace. Record the absence on
			// the artifact metrics so "receipt verified" and "receipt absent"
			// are distinguishable in the ledger.
			if metrics != nil {
				metrics[metricnames.ChrononReceiptMissing] = 1
			}
			log.Printf("[processor WARN] policy=%s but Chronon media receipt is unavailable (tolerated, hash identity falls back to re-read): %v", policy, err)
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
// validation should run for this job. Opt-in through configuration
// (pipeline.deep_visual_validation, aliased by RENDERINGGEN_DEEP_VISUAL=1): CI
// and certification runs enable it; the production hot path relies on the
// Chronon receipt gate (requireNativeVulkan + media receipt) and pays no extra
// ffmpeg processes per render.
func (p *Processor) deepVisualValidationEnabled() bool {
	return p.deepVisualValidation
}
