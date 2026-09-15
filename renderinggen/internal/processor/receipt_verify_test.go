package processor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/metricnames"
)

// TestEnforceReceiptVerificationFastRecordsMissingReceipt pins the
// observability half of the fast-policy fail-open: tolerating an absent
// receipt is deliberate, but it must leave a metric behind so a Chronon that
// silently stopped writing receipts is distinguishable from a verified one.
func TestEnforceReceiptVerificationFastRecordsMissingReceipt(t *testing.T) {
	output := filepath.Join(t.TempDir(), "result.mp4")
	metrics := map[string]float64{}

	// The policy is a configured value, not an environment read: an unset
	// policy (what config.Load produces for an empty pipeline.receipt_verify)
	// resolves to fast.
	p := &Processor{}
	if err := p.enforceReceiptVerification(output, metrics); err != nil {
		t.Fatalf("fast policy must tolerate a missing receipt: %v", err)
	}
	if metrics[metricnames.ChrononReceiptMissing] != 1 {
		t.Fatalf("want %s=1, got %v", metricnames.ChrononReceiptMissing, metrics)
	}
}

// TestEnforceReceiptVerificationNormalFailsClosedOnMissingReceipt pins that the
// stricter policies still fail closed, and do NOT register the tolerated-
// degradation metric for a hard failure.
func TestEnforceReceiptVerificationNormalFailsClosedOnMissingReceipt(t *testing.T) {
	output := filepath.Join(t.TempDir(), "result.mp4")
	metrics := map[string]float64{}

	p := &Processor{}
	p.SetReceiptVerify("normal")
	if err := p.enforceReceiptVerification(output, metrics); err == nil {
		t.Fatal("normal policy must fail closed when the receipt is missing")
	}
	if metrics[metricnames.ChrononReceiptMissing] != 0 {
		t.Fatalf("a hard failure must not set %s: %v", metricnames.ChrononReceiptMissing, metrics)
	}
}

// TestEnforceReceiptVerificationPassesWithValidReceipt pins the happy path and
// that a verified receipt does not emit the missing-receipt metric.
//
// The fixture is a REAL receipt: an output file exists and the receipt's
// size and digest describe it. It previously declared bytes=10/sha256="deadbeef"
// with no output file at all, which the gate accepted because it never checked
// the identity — the same gap receipt_identity_test.go now covers. A fixture
// that cannot be true is not a weaker test, it is a test of nothing.
func TestEnforceReceiptVerificationPassesWithValidReceipt(t *testing.T) {
	output := filepath.Join(t.TempDir(), "result.mp4")
	contents := []byte("rendered bytes")
	if err := os.WriteFile(output, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	receipt := fmt.Sprintf(`{"schema":"chronon3d.render-receipt.v1","output":{"bytes":%d,"sha256":%q},`+
		`"verification":{"resolved_policy":"normal","status":"pass"}}`, len(contents), hex.EncodeToString(sum[:]))
	if err := os.WriteFile(output+".receipt.json", []byte(receipt), 0o600); err != nil {
		t.Fatal(err)
	}
	metrics := map[string]float64{}

	p := &Processor{}
	p.SetReceiptVerify("normal")
	if err := p.enforceReceiptVerification(output, metrics); err != nil {
		t.Fatalf("verified receipt rejected: %v", err)
	}
	if metrics[metricnames.ChrononReceiptMissing] != 0 {
		t.Fatalf("verified receipt must not set %s: %v", metricnames.ChrononReceiptMissing, metrics)
	}
	// A proof policy must also have PROVEN the identity, so the artifact is not
	// carrying an address that was merely accepted.
	if metrics[metricnames.ReceiptIdentityUnverified] != 0 {
		t.Fatalf("a proven identity must not be reported as unverified: %v", metrics)
	}
}
