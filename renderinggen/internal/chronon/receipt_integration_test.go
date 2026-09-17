package chronon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// assertMediaReceiptCertifies is the SINGLE authority for "the render left a
// receipt that certifies this artifact". Both live transports (CLI subprocess
// and warm daemon) call it, so the two cannot drift into different standards
// of proof.
//
// requestedPolicy is the verification policy the render was asked for and must
// be echoed back by the receipt: a receipt that ran no policy, or a different
// one, is not a certificate.
func assertMediaReceiptCertifies(t *testing.T, outputPath, requestedPolicy string) {
	t.Helper()

	// This is the exact check the worker's native gate runs before publishing.
	if err := ReadReceiptPresence(outputPath); err != nil {
		t.Fatalf("receipt missing next to the rendered output (the gate that rejected every clip job): %v", err)
	}
	receipt, err := ReadMediaReceipt(outputPath)
	if err != nil {
		t.Fatalf("receipt not decodable: %v", err)
	}

	// Identity: the receipt must certify THIS artifact, not merely exist.
	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if want := hex.EncodeToString(sum[:]); receipt.Output.SHA256 != want {
		t.Errorf("receipt output.sha256 = %q, want the rendered file's digest %q", receipt.Output.SHA256, want)
	}
	if receipt.Output.Bytes != int64(len(raw)) {
		t.Errorf("receipt output.bytes = %d, want %d", receipt.Output.Bytes, len(raw))
	}
	if receipt.Schema == "" {
		t.Error("receipt schema is empty: a receipt without a declared schema version cannot be validated by a consumer")
	}

	// The receipt must record the verification policy the worker asked for and
	// the verdict it reached: a receipt that ran no policy is not a certificate.
	if receipt.Verification.RequestedPolicy != requestedPolicy {
		t.Errorf("receipt verification.requested_policy = %q, want %q (the policy the render requested)", receipt.Verification.RequestedPolicy, requestedPolicy)
	}
	if receipt.Verification.Status != "pass" {
		t.Errorf("receipt verification.status = %q, want %q", receipt.Verification.Status, "pass")
	}
}

// writeColorSmokePlan materializes the asset-free smoke plan used by the live
// render tests and returns its path, the asset root and the output path.
func writeColorSmokePlan(t *testing.T) (planPath, assetsRoot, outputPath string) {
	t.Helper()
	dir := t.TempDir()
	assetsRoot = filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	planPath = filepath.Join(dir, "plan.json")
	outputPath = filepath.Join(dir, "output", "result.mp4")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte(colorSmokePlan), 0o644); err != nil {
		t.Fatal(err)
	}
	return planPath, assetsRoot, outputPath
}

// TestRenderIntegrationCLIEmitsMediaReceipt certifies the RECEIPT boundary of
// the CLI transport: a render submitted with Report=true must leave a decodable
// `<output>.receipt.json` beside the artifact.
//
// It exists because the strict native gate (strict_native_backend + a source
// video) refuses to publish ANY clip whose output carries no receipt, and the
// receipt is produced by code guarded by the CHRONON3D_ENABLE_VERIFICATION
// preprocessor macro. A build configured with -DCHRONON3D_ENABLE_VERIFICATION=ON
// that never defines that macro compiles the receipt writer out: renders still
// succeed, the log says "render report requested but verification/receipts are
// disabled in this build", and every clip job is rejected downstream with
// "missing Chronon media receipt". Nothing in the offline suites could see that
// (the macro is a build property, not a Go contract), so the proof has to be a
// real render against the real binary.
//
// Gated like the other live tests: set CHRONON_HOME to the install/build prefix
// (release build tree: <Chronon3d>/build/chronon/linux-video-release); without
// it the test skips visibly.
func TestRenderIntegrationCLIEmitsMediaReceipt(t *testing.T) {
	cli := cliAvailable(t)
	planPath, assetsRoot, outputPath := writeColorSmokePlan(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := cli.Render(ctx, RenderRequest{
		PlanPath:      planPath,
		AssetsRoot:    assetsRoot,
		OutputPath:    outputPath,
		Report:        true,
		ReceiptVerify: "fast",
	}); err != nil {
		t.Fatalf("render: %v", err)
	}

	assertMediaReceiptCertifies(t, outputPath, "fast")
}
