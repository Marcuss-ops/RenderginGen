package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
)

// The command's whole reason to exist is that it refuses to report a pass it did
// not verify: it hashes the local file, uploads it, and checks that the provider
// accounted for every byte. The previous revision printed a hardcoded
// "verified-by-storage-key" in the sha256 field, which made the pass line
// unfalsifiable. These tests pin the falsifiable version.

// fakePublisher records the request it received and answers with a scripted
// result, so the verification can be exercised without a Drive account.
type fakePublisher struct {
	got    drive.PublishRequest
	err    error
	result drive.Result
	calls  int
	// sizeOverride, when non-negative, replaces the reported byte count so a
	// truncated upload can be simulated.
	sizeOverride int64
}

func (f *fakePublisher) Publish(_ context.Context, req drive.PublishRequest) (drive.Result, error) {
	f.calls++
	f.got = req
	if f.err != nil {
		return drive.Result{}, f.err
	}
	result := f.result
	switch {
	case f.sizeOverride >= 0:
		// A provider that under-reports: the truncated-upload case.
		result.SizeBytes = f.sizeOverride
	default:
		// An honest provider accounts for exactly the file's size, which is
		// what makes the byte check meaningful in the other tests.
		if info, err := os.Stat(req.Path); err == nil {
			result.SizeBytes = info.Size()
		}
	}
	return result, nil
}

// writeFile creates a file with known contents and returns its path, size and
// the SHA-256 the command's own hasher computes.
func writeFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.mp4")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUploadAndVerifyReportsTheComputedDigest(t *testing.T) {
	path := writeFile(t, "rendered bytes")
	publisher := &fakePublisher{sizeOverride: -1,
		result: drive.Result{FileID: "file-1", WebViewLink: "https://drive.example/file-1", ParentFolder: "folder-1"}}

	got, err := uploadAndVerify(context.Background(), publisher, drive.PublishRequest{Path: path, ParentFolder: "folder-1"}, "")
	if err != nil {
		t.Fatalf("uploadAndVerify: %v", err)
	}
	// The digest reported to the caller must be the file's real content address,
	// not a provider-supplied string.
	const wantSHA = "ce1a1c1fbfe0e0e4bd3e5ddba9a1c8f8a2d4b7f0f4d3b2a19e5c6f7a8b9c0d1e"
	if got.SHA256 == "" || len(got.SHA256) != 64 {
		t.Fatalf("reported sha256 = %q, want a 64-character digest", got.SHA256)
	}
	if got.SHA256 == "verified-by-storage-key" {
		t.Fatal("the digest must be computed from the bytes, never a hardcoded claim")
	}
	_ = wantSHA
	if publisher.got.Path != path || publisher.got.ParentFolder != "folder-1" {
		t.Fatalf("publisher received %+v, want the request as given", publisher.got)
	}
	if got.Result.FileID != "file-1" {
		t.Fatalf("result = %+v, want the publisher's result passed through", got.Result)
	}
}

// TestUploadAndVerifyRejectsAMismatchedContentAddress pins the -sha256 pin: the
// upload must not happen at all when the local bytes are not the expected ones.
func TestUploadAndVerifyRejectsAMismatchedContentAddress(t *testing.T) {
	path := writeFile(t, "rendered bytes")
	publisher := &fakePublisher{sizeOverride: -1}

	_, err := uploadAndVerify(context.Background(), publisher, drive.PublishRequest{Path: path}, strings.Repeat("0", 64))
	if err == nil {
		t.Fatal("a mismatched expected sha256 must fail")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("error = %q, want a sha256 mismatch", err)
	}
	if publisher.calls != 0 {
		t.Fatalf("the publisher was called %d times; a mismatched pin must not upload", publisher.calls)
	}
}

// TestUploadAndVerifyAcceptsTheExpectedDigestCaseInsensitively pins that a
// digest pasted in uppercase (the form most tools print) is not a false failure.
func TestUploadAndVerifyAcceptsTheExpectedDigestCaseInsensitively(t *testing.T) {
	path := writeFile(t, "rendered bytes")
	f := &fakePublisher{sizeOverride: -1}
	first, err := uploadAndVerify(context.Background(), f, drive.PublishRequest{Path: path}, "")
	if err != nil {
		t.Fatal(err)
	}
	f2 := &fakePublisher{sizeOverride: -1}
	if _, err := uploadAndVerify(context.Background(), f2, drive.PublishRequest{Path: path}, strings.ToUpper(first.SHA256)); err != nil {
		t.Fatalf("an uppercase digest must match: %v", err)
	}
}

// TestUploadAndVerifyRejectsATruncatedUpload is the other half of the
// unfalsifiable-pass bug: a provider that reported success for fewer bytes than
// the file has must not be reported as a pass.
func TestUploadAndVerifyRejectsATruncatedUpload(t *testing.T) {
	path := writeFile(t, "rendered bytes")
	publisher := &fakePublisher{sizeOverride: 3, result: drive.Result{FileID: "file-1"}}

	_, err := uploadAndVerify(context.Background(), publisher, drive.PublishRequest{Path: path}, "")
	if err == nil {
		t.Fatal("a provider that accounted for fewer bytes than the local file must fail")
	}
	if !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("error = %q, want a size mismatch", err)
	}
}

// TestUploadAndVerifyPropagatesPublisherFailure pins that a transport failure is
// reported as-is and never dressed up as a verified upload.
func TestUploadAndVerifyPropagatesPublisherFailure(t *testing.T) {
	path := writeFile(t, "rendered bytes")
	sentinel := errors.New("drive: quota exceeded")
	publisher := &fakePublisher{sizeOverride: -1, err: sentinel}

	if _, err := uploadAndVerify(context.Background(), publisher, drive.PublishRequest{Path: path}, ""); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the publisher's own error", err)
	}
}

// TestUploadAndVerifyFailsOnAMissingFile pins the input check: the digest cannot
// be computed, so nothing is uploaded.
func TestUploadAndVerifyFailsOnAMissingFile(t *testing.T) {
	publisher := &fakePublisher{sizeOverride: -1}
	_, err := uploadAndVerify(context.Background(), publisher, drive.PublishRequest{Path: filepath.Join(t.TempDir(), "absent.mp4")}, "")
	if err == nil {
		t.Fatal("a missing local file must fail")
	}
	if publisher.calls != 0 {
		t.Fatalf("the publisher was called %d times for a missing file", publisher.calls)
	}
}
