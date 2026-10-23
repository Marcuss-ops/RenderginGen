package processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
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
	if _, err := p.verifyArtifactReceipt(context.Background(), output, chronon.MediaReceipt{}, os.ErrNotExist, metrics); err != nil {
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

	p := &Processor{receiptVerify: renderVerifyLevel("normal")}
	if _, err := p.verifyArtifactReceipt(context.Background(), output, chronon.MediaReceipt{}, os.ErrNotExist, metrics); err == nil {
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

	p := &Processor{receiptVerify: renderVerifyLevel("normal")}
	parsedReceipt, err := chronon.ReadMediaReceipt(output)
	if err != nil {
		t.Fatalf("read valid receipt: %v", err)
	}
	if _, err := p.verifyArtifactReceipt(context.Background(), output, parsedReceipt, nil, metrics); err != nil {
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

// writeTierReceipt writes a real media receipt whose verification block
// records the given resolved tier and granular decode verdict, describing the
// output file's TRUE size and digest. A fixture that cannot be true is not a
// weaker test, it is a test of nothing.
func writeTierReceipt(t *testing.T, outputPath, resolvedPolicy, decodeCheck string) {
	t.Helper()
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	doc := fmt.Sprintf(`{"schema":"chronon3d.render-receipt.v1","output":{"bytes":%d,"sha256":%q},`+
		`"verification":{"requested_policy":%q,"resolved_policy":%q,"status":"pass","decode":%q}}`,
		len(contents), hex.EncodeToString(sum[:]), resolvedPolicy, resolvedPolicy, decodeCheck)
	if err := os.WriteFile(outputPath+chronon.MediaReceiptSuffix, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// stubValidateDecode replaces the worker's decode tool for one test and
// returns a call counter. The seam is what makes the gate's DECISION — who
// decodes, and when — testable without spawning ffmpeg per case.
func stubValidateDecode(t *testing.T, err error) *int {
	t.Helper()
	original := validateDecode
	t.Cleanup(func() { validateDecode = original })
	calls := 0
	validateDecode = func(context.Context, string) error {
		calls++
		return err
	}
	return &calls
}

// TestChrononReceiptRequestDefersTheDecodeUnderNormal pins the wire mapping:
// under normal the worker owns the promised decode (post-lane), so Chronon is
// asked for the fast tier and the render lane never pays the decode. certify
// keeps the in-daemon decode; strict has no wire tier of its own and must not
// leak its worker-only spelling at the daemon.
func TestChrononReceiptRequestDefersTheDecodeUnderNormal(t *testing.T) {
	cases := map[renderVerifyLevel]string{
		"":                  chronon.ReceiptVerifyFast,
		renderVerifyFast:    chronon.ReceiptVerifyFast,
		renderVerifyNormal:  chronon.ReceiptVerifyFast,
		renderVerifyCertify: chronon.ReceiptVerifyCertify,
		renderVerifyStrict:  chronon.ReceiptVerifyFast,
	}
	for policy, want := range cases {
		p := &Processor{receiptVerify: policy}
		if got := p.chrononReceiptRequest(); got != want {
			t.Errorf("policy %q: wire request = %q, want %q", policy, got, want)
		}
	}
}

// TestWorkerDecodeGateRunsWhenTheReceiptProvesNoDecode is the core deferral
// case: the receipt says the fast tier ran (no decode), so the worker must run
// the promised full decode itself and record that it did.
func TestWorkerDecodeGateRunsWhenTheReceiptProvesNoDecode(t *testing.T) {
	path, _ := writeOutput(t, "rendered bytes")
	writeTierReceipt(t, path, chronon.ReceiptVerifyFast, "skip")
	receipt, receiptErr := readReceipt(t, path)

	calls := stubValidateDecode(t, nil)
	metrics := map[string]float64{}
	p := &Processor{receiptVerify: renderVerifyLevel("normal")}
	if _, err := p.verifyArtifactReceipt(context.Background(), path, receipt, receiptErr, metrics); err != nil {
		t.Fatalf("artifact must pass when the worker decode passes: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("the worker decode gate ran %d times, want exactly 1", *calls)
	}
	if metrics[metricnames.WorkerDecodeVerified] != 1 {
		t.Fatalf("want %s=1, got %v", metricnames.WorkerDecodeVerified, metrics)
	}
}

// TestWorkerDecodeGateSkipsADecodeTheReceiptProves pins the other half of
// "exactly one decode": when the receipt already proves Chronon decoded the
// output in-daemon, the worker must NOT decode it a second time.
func TestWorkerDecodeGateSkipsADecodeTheReceiptProves(t *testing.T) {
	for _, policy := range []renderVerifyLevel{renderVerifyNormal, renderVerifyCertify} {
		path, _ := writeOutput(t, "rendered bytes")
		writeTierReceipt(t, path, chronon.ReceiptVerifyNormal, "pass")
		receipt, receiptErr := readReceipt(t, path)

		calls := stubValidateDecode(t, nil)
		p := &Processor{receiptVerify: policy}
		if _, err := p.verifyArtifactReceipt(context.Background(), path, receipt, receiptErr, map[string]float64{}); err != nil {
			t.Fatalf("policy %s: %v", policy, err)
		}
		if *calls != 0 {
			t.Fatalf("policy %s: the receipt proved an in-daemon decode but the worker decoded again", policy)
		}
	}
}

// TestWorkerDecodeGateIsNotPromisedUnderFastOrStrict: those policies promise
// no decode at all and must never pay for one.
func TestWorkerDecodeGateIsNotPromisedUnderFastOrStrict(t *testing.T) {
	for _, policy := range []renderVerifyLevel{renderVerifyFast, renderVerifyStrict} {
		path, _ := writeOutput(t, "rendered bytes")
		writeTierReceipt(t, path, chronon.ReceiptVerifyFast, "skip")
		receipt, receiptErr := readReceipt(t, path)

		calls := stubValidateDecode(t, nil)
		p := &Processor{receiptVerify: policy}
		if _, err := p.verifyArtifactReceipt(context.Background(), path, receipt, receiptErr, map[string]float64{}); err != nil {
			t.Fatalf("policy %s: %v", policy, err)
		}
		if *calls != 0 {
			t.Fatalf("policy %s promises no decode but the worker decoded", policy)
		}
	}
}

// TestWorkerDecodeFailureRejectsTheArtifact pins the fail-closed direction: a
// bitstream that does not decode is never stored or published.
func TestWorkerDecodeFailureRejectsTheArtifact(t *testing.T) {
	path, _ := writeOutput(t, "rendered bytes")
	writeTierReceipt(t, path, chronon.ReceiptVerifyFast, "skip")
	receipt, receiptErr := readReceipt(t, path)

	stubValidateDecode(t, fmt.Errorf("ffmpeg: corrupt tail frames"))
	p := &Processor{receiptVerify: renderVerifyLevel("normal")}
	if _, err := p.verifyArtifactReceipt(context.Background(), path, receipt, receiptErr, map[string]float64{}); err == nil {
		t.Fatal("a decode failure must reject the artifact before it is stored or published")
	}
}

// TestWorkerDecodeGateDecodesARealArtifact exercises the gate end to end with
// the real ffmpeg tool: a real artifact passes, and a file that is not media
// is rejected even though its receipt identity is consistent.
func TestWorkerDecodeGateDecodesARealArtifact(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:d=0.2", "-c:v", "mpeg4", "-y", good).CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v: %s", err, out)
	}
	writeTierReceipt(t, good, chronon.ReceiptVerifyFast, "skip")
	receipt, receiptErr := readReceipt(t, good)
	p := &Processor{receiptVerify: renderVerifyLevel("normal")}
	if _, err := p.verifyArtifactReceipt(context.Background(), good, receipt, receiptErr, map[string]float64{}); err != nil {
		t.Fatalf("a real decodable artifact must pass the post-lane gate: %v", err)
	}

	corrupt := filepath.Join(dir, "corrupt.mp4")
	if err := os.WriteFile(corrupt, []byte("this is not an MP4"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTierReceipt(t, corrupt, chronon.ReceiptVerifyFast, "skip")
	receipt, receiptErr = readReceipt(t, corrupt)
	if _, err := p.verifyArtifactReceipt(context.Background(), corrupt, receipt, receiptErr, map[string]float64{}); err == nil {
		t.Fatal("an undecodable file must be rejected by the post-lane gate")
	}
}
