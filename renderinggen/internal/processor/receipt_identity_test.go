package processor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
)

// The artifact's content address is the pipeline's identity for the bytes it
// published (local_sha == objectstore_sha == db_sha). These tests pin the rule
// that establishes it, under every policy: the digest is either recomputed from
// the bytes, accepted on a size match with that acceptance recorded, or absent
// so the store phase hashes. A wrong digest of the right LENGTH must never
// become the address under a policy that promises proof.

// writeOutput writes an artifact and returns its path and real digest.
func writeOutput(t *testing.T, contents string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "result.mp4")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(contents))
	return path, hex.EncodeToString(sum[:])
}

// writeReceipt writes a media receipt beside the output. size is what the
// receipt CLAIMS the output's length is; digest what it claims the hash is.
func writeReceipt(t *testing.T, outputPath string, size int64, digest string) {
	t.Helper()
	// The schema string is the receipt contract's own name. It is spelled here
	// as a literal because chronon does not export it (ReadMediaReceipt accepts
	// the document without asserting the value), and this fixture is the only
	// place that needs to write one.
	doc := fmt.Sprintf(`{"schema":"chronon3d.render-receipt.v1","output":{"bytes":%d,"sha256":%q},`+
		`"verification":{"resolved_policy":"normal","status":"pass"}}`, size, digest)
	if err := os.WriteFile(outputPath+chronon.MediaReceiptSuffix, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readReceipt(t *testing.T, path string) (chronon.MediaReceipt, error) {
	t.Helper()
	return chronon.ReadMediaReceipt(path)
}

// TestResolveArtifactIdentityRejectsAWrongDigestOfTheRightLength is the case the
// store phase could not see: the receipt agrees on size, so the old code adopted
// its hash verbatim — even when the hash described different bytes.
func TestResolveArtifactIdentityRejectsAWrongDigestOfTheRightLength(t *testing.T) {
	path, real := writeOutput(t, "rendered bytes")
	wrong := strings.Repeat("a", 64)
	if wrong == real {
		t.Fatal("fixture digest must differ from the real one")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt := chronon.MediaReceipt{}
	receipt.Output.Bytes = info.Size()
	receipt.Output.SHA256 = wrong

	for _, level := range []renderVerifyLevel{renderVerifyNormal, renderVerifyCertify} {
		metrics := map[string]float64{}
		p := &Processor{}
		p.SetReceiptVerify(string(level))
		identity, err := p.resolveArtifactIdentity(path, receipt, nil, metrics)
		if err == nil {
			t.Fatalf("policy %s accepted a digest that does not match the bytes (identity=%+v)", level, identity)
		}
		if !strings.Contains(err.Error(), "does not match") {
			t.Errorf("policy %s: error = %q, want a mismatch", level, err)
		}
		if metrics[metricnames.ReceiptIdentityMismatch] != 1 {
			t.Errorf("policy %s: metrics = %v, want %s=1", level, metrics, metricnames.ReceiptIdentityMismatch)
		}
	}
}

// TestResolveArtifactIdentityProvesTheDigestUnderProofPolicies pins the
// verified path: the digest is recomputed and matches, so the identity is marked
// proven and the artifact carries no provenance warning.
func TestResolveArtifactIdentityProvesTheDigestUnderProofPolicies(t *testing.T) {
	path, real := writeOutput(t, "rendered bytes")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt := chronon.MediaReceipt{}
	receipt.Output.Bytes = info.Size()
	receipt.Output.SHA256 = real

	for _, level := range []renderVerifyLevel{renderVerifyNormal, renderVerifyCertify} {
		metrics := map[string]float64{}
		p := &Processor{}
		p.SetReceiptVerify(string(level))
		identity, err := p.resolveArtifactIdentity(path, receipt, nil, metrics)
		if err != nil {
			t.Fatalf("policy %s: %v", level, err)
		}
		if identity.SHA256 != real || !identity.Proven {
			t.Fatalf("policy %s: identity = %+v, want the real digest marked proven", level, identity)
		}
		if metrics[metricnames.ReceiptIdentityUnverified] != 0 {
			t.Errorf("policy %s: a proven digest must not be reported as unverified: %v", level, metrics)
		}
	}
}

// TestResolveArtifactIdentityAcceptsUnverifiedUnderFast pins fast's documented
// bargain and, crucially, that it is RECORDED: one read instead of two, at the
// price of an address that was accepted rather than recomputed.
func TestResolveArtifactIdentityAcceptsUnverifiedUnderFast(t *testing.T) {
	path, _ := writeOutput(t, "rendered bytes")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	claim := strings.Repeat("b", 64)
	receipt := chronon.MediaReceipt{}
	receipt.Output.Bytes = info.Size()
	receipt.Output.SHA256 = claim

	metrics := map[string]float64{}
	p := &Processor{} // no policy set = fast
	identity, err := p.resolveArtifactIdentity(path, receipt, nil, metrics)
	if err != nil {
		t.Fatalf("fast must tolerate an unproven identity: %v", err)
	}
	if identity.SHA256 != claim || identity.Proven {
		t.Fatalf("identity = %+v, want the claim accepted and NOT marked proven", identity)
	}
	if metrics[metricnames.ReceiptIdentityUnverified] != 1 {
		t.Fatalf("metrics = %v, want %s=1 so the acceptance is visible", metrics, metricnames.ReceiptIdentityUnverified)
	}
}

// TestResolveArtifactIdentitySizeMismatch pins both sides of a receipt that does
// not describe the file in front of it: a hard failure under a proof policy, and
// a recorded re-hash under fast.
func TestResolveArtifactIdentitySizeMismatch(t *testing.T) {
	path, _ := writeOutput(t, "rendered bytes")
	receipt := chronon.MediaReceipt{}
	receipt.Output.Bytes = 3
	receipt.Output.SHA256 = strings.Repeat("c", 64)

	metrics := map[string]float64{}
	p := &Processor{}
	identity, err := p.resolveArtifactIdentity(path, receipt, nil, metrics)
	if err != nil {
		t.Fatalf("fast must fall back to hashing: %v", err)
	}
	if identity.SHA256 != "" {
		t.Fatalf("identity = %+v, want an empty address so the store phase reads the bytes", identity)
	}
	if metrics[metricnames.ReceiptSizeMismatch] != 1 {
		t.Errorf("metrics = %v, want %s=1", metrics, metricnames.ReceiptSizeMismatch)
	}

	for _, level := range []renderVerifyLevel{renderVerifyNormal, renderVerifyCertify} {
		metrics := map[string]float64{}
		p := &Processor{}
		p.SetReceiptVerify(string(level))
		if _, err := p.resolveArtifactIdentity(path, receipt, nil, metrics); err == nil {
			t.Fatalf("policy %s must reject a receipt that describes a different size", level)
		}
	}
}

// TestResolveArtifactIdentityWithoutAReceipt pins the two no-receipt cases: fast
// defers the hash to the store phase (the receipt gate above has already failed
// closed for the proof policies), and the absence is recorded either way.
func TestResolveArtifactIdentityWithoutAReceipt(t *testing.T) {
	path, _ := writeOutput(t, "rendered bytes")
	missing := fmt.Errorf("no receipt")

	metrics := map[string]float64{}
	p := &Processor{}
	identity, err := p.resolveArtifactIdentity(path, chronon.MediaReceipt{}, missing, metrics)
	if err != nil {
		t.Fatalf("fast must tolerate a missing receipt: %v", err)
	}
	if identity.SHA256 != "" {
		t.Fatalf("identity = %+v, want an empty address so the store phase hashes", identity)
	}
	if metrics[metricnames.ChrononReceiptMissing] != 1 {
		t.Errorf("metrics = %v, want %s=1", metrics, metricnames.ChrononReceiptMissing)
	}
}

// TestResolveArtifactIdentityRejectsAReceiptWithoutADigest pins that a proof
// policy cannot prove anything from a receipt that carries no digest: failing is
// the only honest answer, because the alternative is publishing an artifact
// whose address was never established.
func TestResolveArtifactIdentityRejectsAReceiptWithoutADigest(t *testing.T) {
	path, _ := writeOutput(t, "rendered bytes")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt := chronon.MediaReceipt{}
	receipt.Output.Bytes = info.Size() // right size, no digest

	metrics := map[string]float64{}
	p := &Processor{}
	if _, err := p.resolveArtifactIdentity(path, receipt, nil, metrics); err != nil {
		t.Fatalf("fast must fall back to hashing: %v", err)
	}
	if metrics[metricnames.ReceiptIdentityUnusable] != 1 {
		t.Errorf("metrics = %v, want %s=1", metrics, metricnames.ReceiptIdentityUnusable)
	}

	p = &Processor{}
	p.SetReceiptVerify("certify")
	if _, err := p.resolveArtifactIdentity(path, receipt, nil, map[string]float64{}); err == nil {
		t.Fatal("certify must reject a receipt with no digest")
	}
}

// TestVerifyArtifactReceiptAppliesBothVerdicts pins that the entry point is one
// gate: a passing receipt whose digest is wrong is still rejected, and a failed
// receipt is rejected before the identity is even considered.
func TestVerifyArtifactReceiptAppliesBothVerdicts(t *testing.T) {
	t.Run("passing receipt with a wrong digest is rejected", func(t *testing.T) {
		path, _ := writeOutput(t, "rendered bytes")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		writeReceipt(t, path, info.Size(), strings.Repeat("d", 64))
		receipt, receiptErr := readReceipt(t, path)

		p := &Processor{}
		p.SetReceiptVerify("normal")
		if _, err := p.verifyArtifactReceipt(path, receipt, receiptErr, map[string]float64{}); err == nil {
			t.Fatal("the gate must reject a receipt whose digest does not match the bytes")
		}
	})

	t.Run("failing receipt is rejected before the identity", func(t *testing.T) {
		path, real := writeOutput(t, "rendered bytes")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		// A valid identity, but the receipt's aggregate verdict is a failure.
		writeReceipt(t, path, info.Size(), real)
		raw, err := os.ReadFile(path + chronon.MediaReceiptSuffix)
		if err != nil {
			t.Fatal(err)
		}
		failing := strings.Replace(string(raw), `"status":"pass"`, `"status":"fail"`, 1)
		if err := os.WriteFile(path+chronon.MediaReceiptSuffix, []byte(failing), 0o600); err != nil {
			t.Fatal(err)
		}
		receipt, receiptErr := readReceipt(t, path)

		p := &Processor{}
		p.SetReceiptVerify("normal")
		metrics := map[string]float64{}
		if _, err := p.verifyArtifactReceipt(path, receipt, receiptErr, metrics); err == nil {
			t.Fatal("a failing receipt verdict must reject the artifact")
		}
		if metrics[metricnames.ReceiptIdentityUnverified] != 0 {
			t.Errorf("the identity must not be resolved for a rejected receipt: %v", metrics)
		}
	})

	t.Run("verified receipt carries a proven identity forward", func(t *testing.T) {
		path, real := writeOutput(t, "rendered bytes")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		writeReceipt(t, path, info.Size(), real)
		receipt, receiptErr := readReceipt(t, path)

		p := &Processor{}
		p.SetReceiptVerify("normal")
		outcome, err := p.verifyArtifactReceipt(path, receipt, receiptErr, map[string]float64{})
		if err != nil {
			t.Fatalf("gate: %v", err)
		}
		if !outcome.Identity.Proven || outcome.Identity.SHA256 != real {
			t.Fatalf("outcome identity = %+v, want the real digest proven", outcome.Identity)
		}
		if outcome.Err != nil {
			t.Fatalf("outcome should carry the readable receipt: %v", outcome.Err)
		}
	})
}

// TestReceiptIdentityMetricNamesAreDeclared pins that the provenance counters
// reach the ledger vocabulary: an undeclared name is dropped by the queue's
// projection, which would make the whole distinction invisible in production.
func TestReceiptIdentityMetricNamesAreDeclared(t *testing.T) {
	for _, name := range []string{
		metricnames.ReceiptIdentityUnverified,
		metricnames.ReceiptIdentityMismatch,
		metricnames.ReceiptIdentityUnusable,
		metricnames.ReceiptSizeMismatch,
	} {
		if _, ok := metricnames.Unit(name); !ok {
			t.Errorf("metric %q is not declared in the metric vocabulary", name)
		}
	}
}
