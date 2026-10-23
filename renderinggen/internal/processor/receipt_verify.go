// receipt_verify.go owns the single output-verification policy authority on
// the worker: the policy is resolved once (fast/normal/certify/strict) and
// split into two explicit halves at the Chronon boundary —
//
//		enforcement  what the worker demands of the artifact: the receipt gate,
//	            the identity rule and, under a decode-promising policy, one
//	            complete decode of the output
//		request      the wire tier the worker asks Chronon to run (fast | certify)
//
// The split exists so a decode never sits on the GPU lane's critical path.
// Chronon verifies an already-finished render while still holding the render
// device, so asking it for the full-decode tier keeps a lane busy decoding an
// output that is already rendered. Under normal the worker therefore requests
// the FAST tier and owns the promised decode itself — in the post pool
// (enforceDecodableOutput), where it overlaps the next render instead of
// blocking it. The receipt records what Chronon actually ran
// (verification.resolved_policy); the worker records what IT ran
// (worker_decode_verified). Exactly one complete decode happens per artifact:
// Chronon's when the receipt proves it, the worker's otherwise.
package processor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/workerlog"
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
	// renderVerifyStrict is the fail-closed opt-in: like certify it rejects
	// any unverifiable artifact instead of tolerating it. It exists for
	// operators who want the certify failure semantics without the
	// certify decode cost vocabulary (same fail-closed rule, explicit name).
	renderVerifyStrict renderVerifyLevel = "strict"
)

// receiptVerifyLevel is the SINGLE verification-policy authority on the
// worker. fast verifies container/stream metadata without re-decoding the
// freshly muxed output; normal promises one complete decode — performed by
// the worker's post-lane gate (enforceDecodableOutput) whenever the receipt
// proves no in-daemon decode ran; certify is the explicit correctness mode
// used by CI/golden benchmarks and keeps Chronon's in-daemon decode as
// independent evidence.
//
// The value comes from configuration (pipeline.receipt_verify, and through the
// aliased RENDERINGGEN_RECEIPT_VERIFY), never from a read inside this package:
// the historical CHRONON_RECEIPT_VERIFY alias is deliberately NOT consulted, so
// the worker and its Chronon subprocess cannot run different policies. config
// validates the accepted spellings at load, so anything else reaching here is a
// caller that built a Processor directly — which gets the fast default rather
// than an invented policy. The ENFORCEMENT level is what the gates below apply;
// the wire tier Chronon is asked for is a separate, explicit mapping
// (chrononReceiptRequest), never this value forwarded verbatim.
func (p *Processor) receiptVerifyLevel() renderVerifyLevel {
	switch p.receiptVerify {
	case renderVerifyNormal:
		return renderVerifyNormal
	case renderVerifyCertify:
		return renderVerifyCertify
	case renderVerifyStrict:
		return renderVerifyStrict
	default:
		return renderVerifyFast
	}
}

// chrononReceiptRequest maps the enforcement policy onto the wire tier the
// worker asks Chronon to run (chronon.RenderRequest.ReceiptVerify ->
// CHRONON_RECEIPT_VERIFY on the CLI env, receipt_verify on the daemon IPC).
// It is deliberately NOT the policy forwarded verbatim:
//
//   - normal requests fast. The promised full decode is performed by the
//     worker's post-lane gate (enforceDecodableOutput), so the render lane is
//     never held for a decode of an output that is already rendered. The
//     receipt then honestly records that Chronon ran the fast tier.
//   - certify requests certify. CI and golden certification keep Chronon's
//     in-daemon decode as independent evidence — speed is not the constraint
//     there, and the worker gate still backstops a receipt that proves no
//     decode.
//   - fast requests fast: no decode is promised anywhere.
//   - strict requests fast: the wire vocabulary has no strict tier (fast,
//     normal, certify), and strict is documented as the fail-closed rule
//     WITHOUT the certify decode cost. Forwarding the worker-only spelling
//     leaked a non-wire value at the daemon.
func (p *Processor) chrononReceiptRequest() string {
	if p.receiptVerifyLevel() == renderVerifyCertify {
		return chronon.ReceiptVerifyCertify
	}
	return chronon.ReceiptVerifyFast
}

// validateDecode is the full-decode tool behind the worker's post-lane gate,
// wrapped in a variable so the gate's decision logic is testable without
// spawning ffmpeg per case. It resolves its binary through the same media
// configuration as every other tool call (media.Configure).
var validateDecode = media.ValidateDecode

// enforceDecodableOutput makes good on the decode promise of the enforcement
// policy, OFF the render lane: it runs in the post pool, inside FinalizeJob,
// after the render device has already been released to the next job.
//
// Exactly one complete decode per artifact. When the receipt PROVES Chronon
// decoded the output in-daemon (receiptProvesDecode), there is nothing to
// repeat — the receipt gate above already required that receipt to pass.
// Otherwise (the fast tier was resolved: under normal that is the expected
// case, because the worker requested it to keep the lane free) the worker runs
// the same full-decode gate itself before the bytes can be stored or
// published. A decode failure rejects the artifact exactly like a failing
// receipt check: nothing is published from a bitstream that does not decode.
//
// fast and strict promise no decode at all and never pay for one here.
func (p *Processor) enforceDecodableOutput(ctx context.Context, outputPath string, receipt chronon.MediaReceipt, receiptErr error, metrics map[string]float64, jobIDParts ...string) error {
	switch p.receiptVerifyLevel() {
	case renderVerifyNormal, renderVerifyCertify:
	default:
		return nil
	}
	if receiptErr == nil && receiptProvesDecode(receipt) {
		return nil
	}
	jobID := ""
	if len(jobIDParts) > 0 {
		jobID = jobIDParts[0]
	}
	start := time.Now()
	err := validateDecode(ctx, outputPath)
	elapsedUS := float64(time.Since(start).Microseconds())
	if metrics != nil {
		metrics[metricnames.WorkerDecodeUS] = elapsedUS
		metrics[metricnames.WorkerDecodeMS] = elapsedUS / 1000
	}
	if err != nil {
		return fmt.Errorf("processor: full-decode verification failed (policy=%s, worker post-lane gate): %w", p.receiptVerifyLevel(), err)
	}
	if metrics != nil {
		metrics[metricnames.WorkerDecodeVerified] = 1
	}
	workerlog.ByJobID(jobID).Infof("post-lane decode gate passed (policy=%s, worker-decoded in %.0fms): the receipt proved no in-daemon decode", p.receiptVerifyLevel(), elapsedUS/1000)
	return nil
}

// receiptProvesDecode reports whether the receipt is evidence that a complete
// decode of the output ran inside Chronon. The resolved policy is the
// contract (normal/certify decode, fast does not); the granular decode check
// covers receipts that ran the check without naming a decode-promising tier.
func receiptProvesDecode(receipt chronon.MediaReceipt) bool {
	if strings.EqualFold(strings.TrimSpace(receipt.Verification.Decode), "pass") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(receipt.ResolvedVerificationPolicy())) {
	case chronon.ReceiptVerifyNormal, chronon.ReceiptVerifyCertify:
		return true
	default:
		return false
	}
}

// enforceReceiptVerificationDirect makes the worker enforce — never duplicate —
// Chronon's canonical output verification. Under normal/certify the receipt
// is mandatory evidence: it is the render's canonical record (identity claim,
// media facts and the verification that actually ran), and a missing receipt
// means the canonical verifier did not run at all, so the artifact is
// rejected (fail closed) rather than silently accepted on the worker's own
// probe. Under fast a missing receipt is tolerated: the gate is the
// encoder/muxer success plus the profile/overlay probe validation below. When
// the receipt is present the aggregate verification status must be pass at
// every policy — Chronon's media checks (container, codec, pixel format,
// resolution, fps, audio, optional decode) are the verdict.
// The identity half of the gate is not optional and not policy-blind: it runs
// here, for every policy, and the store phase consumes its verdict. See
// receipt_identity.go.
func (p *Processor) enforceReceiptVerificationDirect(receipt chronon.MediaReceipt, err error, metrics map[string]float64, jobIDParts ...string) error {
	policy := p.receiptVerifyLevel()
	jobID := ""
	if len(jobIDParts) > 0 {
		jobID = jobIDParts[0]
	}
	if err != nil {
		if policy == renderVerifyFast {
			// Fast fail-open is BY DESIGN: fast never promises a decode, the
			// output is certified by the in-process encoder/muxer success
			// plus the probe validation below. The tolerance is deliberate,
			// but it must not be invisible: a Chronon that silently stopped
			// writing receipts would otherwise leave zero trace. Record the
			// absence on the artifact metrics so "receipt verified" and
			// "receipt absent" are distinguishable in the ledger.
			if metrics != nil {
				metrics[metricnames.ChrononReceiptMissing] = 1
			}
			workerlog.ByJobID(jobID).Warnf("policy=%s but Chronon media receipt is unavailable (tolerated fail-open by design, hash identity falls back to re-read): %v", policy, err)
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
