package processor

import (
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
	t.Setenv("RENDERINGGEN_RECEIPT_VERIFY", "") // empty resolves to fast
	output := filepath.Join(t.TempDir(), "result.mp4")
	metrics := map[string]float64{}

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
	t.Setenv("RENDERINGGEN_RECEIPT_VERIFY", "normal")
	output := filepath.Join(t.TempDir(), "result.mp4")
	metrics := map[string]float64{}

	p := &Processor{}
	if err := p.enforceReceiptVerification(output, metrics); err == nil {
		t.Fatal("normal policy must fail closed when the receipt is missing")
	}
	if metrics[metricnames.ChrononReceiptMissing] != 0 {
		t.Fatalf("a hard failure must not set %s: %v", metricnames.ChrononReceiptMissing, metrics)
	}
}

// TestEnforceReceiptVerificationPassesWithValidReceipt pins the happy path and
// that a verified receipt does not emit the missing-receipt metric.
func TestEnforceReceiptVerificationPassesWithValidReceipt(t *testing.T) {
	t.Setenv("RENDERINGGEN_RECEIPT_VERIFY", "normal")
	output := filepath.Join(t.TempDir(), "result.mp4")
	receipt := `{"schema":"chronon3d.render-receipt.v1","output":{"bytes":10,"sha256":"deadbeef"},` +
		`"verification":{"resolved_policy":"normal","status":"pass"}}`
	if err := os.WriteFile(output+".receipt.json", []byte(receipt), 0o600); err != nil {
		t.Fatal(err)
	}
	metrics := map[string]float64{}

	p := &Processor{}
	if err := p.enforceReceiptVerification(output, metrics); err != nil {
		t.Fatalf("verified receipt rejected: %v", err)
	}
	if metrics[metricnames.ChrononReceiptMissing] != 0 {
		t.Fatalf("verified receipt must not set %s: %v", metricnames.ChrononReceiptMissing, metrics)
	}
}
