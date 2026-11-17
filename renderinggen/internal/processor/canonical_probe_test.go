package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/chronon"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/media"
)

// needsCanonicalEncodeFromRenderReceipt is the decision that removes two
// ffprobe processes per canonicalized artifact. It may only take the shortcut
// when the encode is unconditionally required: a PASSING renderer receipt, no
// NVENC allowlist, and no canonical avcC already certified by that receipt.
// Everything else keeps the probe, which is what preserves idempotence for
// retries, for recertified native artifacts, and for a future renderer lane
// that emits the canonical x264 stream directly.
func TestNeedsCanonicalEncodeFromRenderReceipt(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, receipt *chronon.MediaReceipt) string {
		t.Helper()
		path := filepath.Join(dir, name+".mp4")
		if err := os.WriteFile(path, []byte("rendered bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		if receipt == nil {
			return path
		}
		data, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+chronon.MediaReceiptSuffix, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	digest := strings.Repeat("a", 64)
	canonicalAVCC := strings.Repeat("c", 64)
	const fixtureBytes = 14 // len("rendered bytes")
	renderPass := chronon.MediaReceipt{Schema: "chronon3d.render-receipt.v1"}
	renderPass.Verification.Status = "pass"
	renderPass.Output.SHA256 = digest
	renderPass.Output.Bytes = fixtureBytes

	renderFail := renderPass
	renderFail.Verification.Status = "fail"

	renderNoDigest := renderPass
	renderNoDigest.Output.SHA256 = ""

	renderCanonical := renderPass
	renderCanonical.Media.CodecConfigSHA256 = canonicalAVCC

	workerCanonical := renderPass
	workerCanonical.Schema = canonicalOverlayReceiptSchema

	cases := []struct {
		name  string
		path  string
		allow bool
		want  bool
	}{
		{
			name: "a passing render receipt with no allowlist needs no source probe",
			path: write("render-pass", &renderPass),
			want: true,
		},
		{
			name:  "an allowlist keeps the probe: a native avcC may be accepted as-is",
			path:  write("render-pass-allowlist", &renderPass),
			allow: true,
			want:  false,
		},
		{
			name: "the worker's own canonical receipt keeps the probe",
			path: write("worker-canonical", &workerCanonical),
			want: false,
		},
		{
			name: "a failing render receipt keeps the probe (the gate decides the verdict)",
			path: write("render-fail", &renderFail),
			want: false,
		},
		{
			name: "a render receipt without a digest keeps the probe",
			path: write("render-no-digest", &renderNoDigest),
			want: false,
		},
		{
			name: "a render receipt that already certifies the canonical avcC keeps the probe (the probe then skips the encode)",
			path: write("render-canonical", &renderCanonical),
			want: false,
		},
		{
			name: "no receipt keeps the probe (a retry may already be canonical)",
			path: write("no-receipt", nil),
			want: false,
		},
	}
	for _, tc := range cases {
		if got := needsCanonicalEncodeFromRenderReceipt(tc.path, canonicalAVCC, tc.allow); got != tc.want {
			t.Errorf("%s: = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// receiptCertifiesCanonicalIdentity is the decision that deletes the LAST
// ffprobe from the canonical finalize: a receipt that certifies the profile's
// exact avcC for the file on disk makes both the confirmation probe and the
// re-encode unnecessary. It must be narrow — every uncertainty keeps the probe.
func TestReceiptCertifiesCanonicalIdentity(t *testing.T) {
	dir := t.TempDir()
	const payload = "rendered bytes"

	canonical := strings.Repeat("c", 64)
	write := func(name string, receipt *chronon.MediaReceipt, reportedBytes int) string {
		t.Helper()
		path := filepath.Join(dir, name+".mp4")
		if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
			t.Fatal(err)
		}
		if receipt == nil {
			return path
		}
		value := *receipt
		value.Output.Bytes = int64(reportedBytes)
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+chronon.MediaReceiptSuffix, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	passing := func(avcc string) *chronon.MediaReceipt {
		receipt := &chronon.MediaReceipt{Schema: "chronon3d.render-receipt.v1"}
		receipt.Verification.Status = "pass"
		receipt.Output.SHA256 = strings.Repeat("a", 64)
		receipt.Media.CodecConfigSHA256 = avcc
		return receipt
	}

	worker := passing(canonical)
	worker.Schema = canonicalOverlayReceiptSchema

	receiptFailing := passing(canonical)
	receiptFailing.Verification.Status = "fail"

	receiptNoDigest := passing(canonical)
	receiptNoDigest.Output.SHA256 = ""

	cases := []struct {
		name      string
		path      string
		canonical string
		want      bool
	}{
		{
			name:      "a passing renderer receipt with the canonical avcC and the file's size is proof",
			path:      write("renderer-canonical", passing(canonical), len(payload)),
			canonical: canonical,
			want:      true,
		},
		{
			name:      "the worker's own canonical receipt is the same proof",
			path:      write("worker-canonical", worker, len(payload)),
			canonical: canonical,
			want:      true,
		},
		{
			name:      "a size mismatch means the receipt describes another file",
			path:      write("size-mismatch", passing(canonical), len(payload)+1),
			canonical: canonical,
			want:      false,
		},
		{
			name:      "a different avcC keeps the probe and the encode",
			path:      write("different-avcc", passing(strings.Repeat("d", 64)), len(payload)),
			canonical: canonical,
			want:      false,
		},
		{
			name:      "a failing receipt is never proof",
			path:      write("failing", receiptFailing, len(payload)),
			canonical: canonical,
			want:      false,
		},
		{
			name:      "a receipt without a digest is never proof",
			path:      write("no-digest", receiptNoDigest, len(payload)),
			canonical: canonical,
			want:      false,
		},
		{
			name:      "no receipt keeps the probe",
			path:      write("no-receipt", nil, len(payload)),
			canonical: canonical,
			want:      false,
		},
		{
			name:      "a profile that does not pin the avcC never takes the shortcut",
			path:      write("unpinned", passing(canonical), len(payload)),
			canonical: "",
			want:      false,
		},
	}
	for _, tc := range cases {
		if got := receiptCertifiesCanonicalIdentity(tc.path, tc.canonical); got != tc.want {
			t.Errorf("%s: = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// The artifact is deliberately not a valid MP4: if the pass falls through to
// the probe (or the encode) the test fails, so it proves the receipt path never
// spawns ffprobe or rewrites the bytes.
func TestCanonicalizeOutputMP4SkipsEncodeWhenReceiptCertifiesCanonical(t *testing.T) {
	profile, err := media.ResolveProfile(media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatalf("resolve V3: %v", err)
	}
	if profile.CodecConfigSHA256 == "" {
		t.Fatal("V3 must pin the canonical avcC")
	}
	payload := []byte("not an mp4: a probe or encode would fail here")
	dir := t.TempDir()
	path := filepath.Join(dir, "certified.mp4")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	receipt := chronon.MediaReceipt{Schema: "chronon3d.render-receipt.v1"}
	receipt.Verification.Status = "pass"
	receipt.Output.SHA256 = strings.Repeat("b", 64)
	receipt.Output.Bytes = int64(len(payload))
	receipt.Media.CodecConfigSHA256 = profile.CodecConfigSHA256
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+chronon.MediaReceiptSuffix, data, 0o644); err != nil {
		t.Fatal(err)
	}

	encoded, err := canonicalizeOutputMP4(context.Background(), path, media.ProfileVeloxOverlayVideoOnlyV3)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if encoded {
		t.Fatal("a receipt-certified canonical artifact was re-encoded")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("the artifact bytes changed on the receipt-certified path")
	}
	if _, err := os.Stat(path + ".canonical.tmp.mp4"); !os.IsNotExist(err) {
		t.Fatalf("a canonical temp file exists: %v", err)
	}
}
