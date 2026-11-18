package processor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
)

// TestCanonicalReceiptLegacySchemaNeverCertifiesSkip pins the receipt boundary:
// only renderinggen.canonical-overlay-receipt.v1 describes final canonical
// bytes. A legacy/unknown schema is not proof, even when its avcC and size
// happen to match; the pass falls through to the probe/encode path.
func TestCanonicalReceiptLegacySchemaNeverCertifiesSkip(t *testing.T) {
	payload := []byte("legacy receipt bytes")
	path := filepath.Join(t.TempDir(), "legacy.mp4")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	canonical := strings.Repeat("c", 64)
	receipt := chronon.MediaReceipt{Schema: "renderinggen.canonical-overlay-receipt.v0"}
	receipt.Verification.Status = "pass"
	receipt.Output.SHA256 = strings.Repeat("a", 64)
	receipt.Output.Bytes = int64(len(payload))
	receipt.Media.CodecConfigSHA256 = canonical
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+chronon.MediaReceiptSuffix, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if receiptCertifiesCanonicalIdentity(path, canonical) {
		t.Fatal("a legacy receipt schema certified the canonical skip")
	}
	if !needsCanonicalEncodeFromRenderReceipt(path, "", false) {
		t.Fatal("a legacy receipt suppressed the forced canonical encode")
	}
}
