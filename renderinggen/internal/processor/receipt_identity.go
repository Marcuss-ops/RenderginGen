// receipt_identity.go owns the byte IDENTITY of a finalized artifact: the
// content address the artifact is stored and recorded under, and whether that
// address was proven against the bytes.
//
// Why it lives in the gate. Hashing the rendered output costs a full read of a
// video-sized file, so the worker prefers Chronon's own SHA-256 from the media
// receipt — the engine already read the bytes while encoding. That preference
// used to be implemented in the store phase as "if the receipt agrees on SIZE,
// trust its hash", which is a second, policy-blind verification decision sitting
// next to the gate that owns every other verification decision: a receipt whose
// hash was wrong (engine bug, truncated receipt, a different file of the same
// length) produced an artifact whose content address did not match the bytes it
// was stored under, breaking the pipeline's local_sha == objectstore_sha ==
// db_sha invariant with no test able to see it.
//
// The gate now resolves the identity at EVERY policy, and says which of three
// things happened:
//
//	proven      the digest was recomputed from the bytes and matched the claim
//	unproven    the claim is accepted on a size match (fast only, recorded)
//	re-hash     no usable claim, so the store phase must read the bytes
//
// The policy decides which is acceptable, not the store phase: fast keeps its
// documented single-pass behaviour but can no longer be *mistaken* for proof
// (the artifact carries receipt_identity_unverified), while normal/certify
// require the digest to be proven and reject a receipt that does not describe
// the file in front of them.
package processor

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
)

// artifactIdentity is the content address of the rendered artifact plus how it
// was established.
type artifactIdentity struct {
	// SHA256 is the content address. Empty means no usable claim was available
	// and the store phase must compute it from the bytes.
	SHA256 string
	// Proven is true only when SHA256 was recomputed from the artifact's bytes
	// and matched the engine's claim.
	Proven bool
	// Source names how the identity was established, for the job log.
	Source string
}

// receiptOutcome carries the gate's verdict on an artifact from the verification
// step into the store phase, so the store phase never reads the receipt again
// and never forms its own trust decision.
type receiptOutcome struct {
	Receipt  chronon.MediaReceipt
	Err      error
	Identity artifactIdentity
}

// outputSize is the artifact's size on disk.
//
// A failure here is not a verification verdict but a broken pipeline invariant:
// every caller reaches this after the render succeeded, so a file that cannot be
// stat'ed is reported as an operational error rather than turned into a
// "verification failed" message that would send a reader looking at Chronon.
func outputSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("processor: stat output %s: %w", path, err)
	}
	return info.Size(), nil
}

// verifyArtifactReceipt is the SINGLE verification authority for a finalized
// artifact: it enforces Chronon's receipt policy and then resolves the
// artifact's byte identity under that policy.
//
// It exists so the two decisions cannot disagree — the receipt gate and the
// identity rule are one function with one policy value, and the store phase
// consumes the verdict instead of recomputing part of it.
func (p *Processor) verifyArtifactReceipt(outputPath string, receipt chronon.MediaReceipt, receiptErr error, metrics map[string]float64) (receiptOutcome, error) {
	if err := p.enforceReceiptVerificationDirect(receipt, receiptErr, metrics); err != nil {
		return receiptOutcome{}, err
	}
	identity, err := p.resolveArtifactIdentity(outputPath, receipt, receiptErr, metrics)
	if err != nil {
		return receiptOutcome{}, err
	}
	return receiptOutcome{Receipt: receipt, Err: receiptErr, Identity: identity}, nil
}

// resolveArtifactIdentity establishes the artifact's content address under the
// resolved policy. The returned identity may be unproven (fast) or empty (the
// store phase must hash), and the metrics map records which case it was so the
// ledger distinguishes "verified" from "accepted".
func (p *Processor) resolveArtifactIdentity(outputPath string, receipt chronon.MediaReceipt, receiptErr error, metrics map[string]float64) (artifactIdentity, error) {
	policy := p.receiptVerifyLevel()
	claim := strings.TrimSpace(receipt.Output.SHA256)

	// No receipt at all. normal/certify already failed in the receipt gate
	// above (a missing receipt means the canonical verifier did not run), so
	// reaching here with no receipt means the fast policy, which deliberately
	// tolerates it: the store phase re-reads the bytes and the absence is
	// recorded.
	if receiptErr != nil {
		if metrics != nil {
			metrics[metricnames.ChrononReceiptMissing] = 1
		}
		log.Printf("[processor WARN] policy=%s: no usable receipt identity (%v); the output must be hashed directly", policy, receiptErr)
		return artifactIdentity{Source: "receipt-absent"}, nil
	}

	// A receipt that disagrees on size does not describe this file. Under a
	// proof policy that is a verification failure; under fast the bytes are
	// re-read, which is what the pipeline did before, but now it is recorded
	// instead of being indistinguishable from a size match.
	size, err := outputSize(outputPath)
	if err != nil {
		return artifactIdentity{}, err
	}
	if receipt.Output.Bytes != size {
		if metrics != nil {
			metrics[metricnames.ReceiptSizeMismatch] = 1
		}
		if policy != renderVerifyFast {
			return artifactIdentity{}, fmt.Errorf(
				"processor: chronon receipt describes %d bytes but the output is %d (policy=%s): the receipt does not describe this artifact",
				receipt.Output.Bytes, size, policy)
		}
		log.Printf("[processor WARN] chronon receipt size %d != output %d; hashing the output directly", receipt.Output.Bytes, size)
		return artifactIdentity{Source: "receipt-size-mismatch"}, nil
	}

	// A receipt with no digest cannot prove anything.
	if claim == "" {
		if metrics != nil {
			metrics[metricnames.ReceiptIdentityUnusable] = 1
		}
		if policy != renderVerifyFast {
			return artifactIdentity{}, fmt.Errorf(
				"processor: chronon receipt carries no output sha256 (policy=%s), so the artifact's content address cannot be proven", policy)
		}
		log.Printf("[processor WARN] chronon receipt carries no output sha256; hashing the output directly")
		return artifactIdentity{Source: "receipt-no-digest"}, nil
	}

	if policy == renderVerifyFast {
		// fast's documented bargain: one read instead of two. The size match is
		// checked and the acceptance is recorded, so an artifact whose address
		// was never recomputed is visible as such rather than presented as
		// verified.
		if metrics != nil {
			metrics[metricnames.ReceiptIdentityUnverified] = 1
		}
		return artifactIdentity{SHA256: claim, Proven: false, Source: "receipt-size-matched"}, nil
	}

	// normal/certify promise proof, so the digest is recomputed. This is the
	// check the store phase never made: it compared sizes only, so a wrong hash
	// of the right length became the artifact's permanent content address.
	digest, _, hashErr := hashio.File(outputPath)
	if hashErr != nil {
		return artifactIdentity{}, fmt.Errorf("processor: hash output for receipt verification %s: %w", outputPath, hashErr)
	}
	if !strings.EqualFold(digest, claim) {
		if metrics != nil {
			metrics[metricnames.ReceiptIdentityMismatch] = 1
		}
		return artifactIdentity{}, fmt.Errorf(
			"processor: output sha256 %s does not match the chronon receipt %s (policy=%s): the receipt does not describe this artifact",
			digest, claim, policy)
	}
	return artifactIdentity{SHA256: digest, Proven: true, Source: "receipt-verified"}, nil
}
